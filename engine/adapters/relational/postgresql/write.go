package postgresql

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/internal/redact"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

// Writes for graph → relational migrations (ADR-0006). Every batch is one
// statement over unnest()ed text arrays, so it is atomic and needs one round
// trip. Values are sent as PostgreSQL input text and cast to the base type
// of their logical type; assigning that to the column applies the column's
// own checks (length, range, constraints) instead of truncating silently.

// castType is the type a value is cast to before assignment to column c.
// Type modifiers are left out on purpose: an explicit cast to varchar(n)
// would truncate, while assignment rejects. Native types come from the
// catalog or the plan, never from data.
func castType(c relational.Column) string {
	base := func(t ir.LogicalType, native string) string {
		switch t {
		case ir.TypeBool:
			return "boolean"
		case ir.TypeInt64:
			return "bigint"
		case ir.TypeDecimal:
			return "numeric"
		case ir.TypeFloat64:
			return "double precision"
		case ir.TypeUUID:
			return "uuid"
		case ir.TypeDate:
			return "date"
		case ir.TypeTimestamp:
			return "timestamp"
		case ir.TypeTimestampTZ:
			return "timestamptz"
		case ir.TypeBytes:
			return "bytea"
		case ir.TypeJSON:
			if native == "json" {
				return "json"
			}
			return "jsonb"
		default:
			// Strings: text, unless the column has a type with its own
			// input syntax, such as an enum.
			if native == "" || native == "text" || strings.HasPrefix(native, "character") {
				return "text"
			}
			return native
		}
	}
	if c.Type == ir.TypeArray {
		return base(c.Element, strings.TrimSuffix(c.NativeType, "[]")) + "[]"
	}
	return base(c.Type, c.NativeType)
}

// inputText converts a canonical value to PostgreSQL input text; nil is NULL.
func inputText(t, elem ir.LogicalType, v ir.Value) (*string, error) {
	if v.Null {
		return nil, nil
	}
	var s string
	switch t {
	case ir.TypeJSON:
		s = string(v.JSON)
	case ir.TypeBytes:
		b, err := base64.StdEncoding.DecodeString(v.Text)
		if err != nil {
			return nil, err
		}
		s = `\x` + hex.EncodeToString(b)
	case ir.TypeArray:
		if elem == ir.TypeBytes || elem == ir.TypeJSON || elem == ir.TypeArray {
			return nil, fmt.Errorf("arrays of %s cannot be written by this engine version", elem)
		}
		var sb strings.Builder
		sb.WriteByte('{')
		for i, e := range v.Elements {
			if i > 0 {
				sb.WriteByte(',')
			}
			if e.Null {
				sb.WriteString("NULL")
				continue
			}
			sb.WriteByte('"')
			sb.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(e.Text))
			sb.WriteByte('"')
		}
		sb.WriteByte('}')
		s = sb.String()
	default:
		s = v.Text
	}
	return &s, nil
}

// columnArrays transposes rows into one text array per column.
func columnArrays(cols []relational.Column, rows [][]ir.Value) ([]any, error) {
	arrays := make([][]*string, len(cols))
	for i := range arrays {
		arrays[i] = make([]*string, len(rows))
	}
	for r, row := range rows {
		if len(row) != len(cols) {
			return nil, rtgerr.New(rtgerr.Internal, "row %d has %d values for %d columns", r, len(row), len(cols))
		}
		for i, c := range cols {
			s, err := inputText(c.Type, c.Element, row[i])
			if err != nil {
				return nil, rtgerr.New(rtgerr.ExecConversion, "column %s: %v", c.Name, err)
			}
			arrays[i][r] = s
		}
	}
	out := make([]any, len(arrays))
	for i, a := range arrays {
		out[i] = a
	}
	return out, nil
}

// unnest returns "unnest($1::text[], …) AS u(c0, …)" for n columns,
// numbering parameters from 1.
func unnest(n int, ordinality bool) string {
	params := make([]string, n)
	names := make([]string, n)
	for i := range n {
		params[i] = fmt.Sprintf("$%d::text[]", i+1)
		names[i] = fmt.Sprintf("c%d", i)
	}
	ord := ""
	if ordinality {
		ord = " WITH ORDINALITY"
		names = append(names, "i")
	}
	return "unnest(" + strings.Join(params, ", ") + ")" + ord + " AS u(" + strings.Join(names, ", ") + ")"
}

// writeError classifies a write error: data the database rejects
// (SQLSTATE classes 22 and 23) is RTG-PG-002, transient failures are the
// retryable RTG-EXEC-003, and anything else is RTG-PG-001.
func (a *Adapter) writeError(what string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		msg := redact.Secrets(pgErr.Message, a.secrets...)
		if pgErr.Detail != "" {
			msg += " (" + redact.Secrets(pgErr.Detail, a.secrets...) + ")"
		}
		switch {
		case strings.HasPrefix(pgErr.Code, "22"), strings.HasPrefix(pgErr.Code, "23"):
			return rtgerr.New(rtgerr.PGRowRejected, "%s: PostgreSQL rejected the data (SQLSTATE %s): %s", what, pgErr.Code, msg)
		case pgErr.Code == "40001", pgErr.Code == "40P01", strings.HasPrefix(pgErr.Code, "08"), pgErr.Code == "57P01":
			return rtgerr.New(rtgerr.ExecBatchFailed, "%s failed with a transient error (SQLSTATE %s): %s", what, pgErr.Code, msg)
		}
		return rtgerr.New(rtgerr.PGQuery, "%s failed (SQLSTATE %s): %s", what, pgErr.Code, msg)
	}
	if pgconn.SafeToRetry(err) || pgconn.Timeout(err) {
		return rtgerr.New(rtgerr.ExecBatchFailed, "%s failed with a transient error: %s", what, redact.Secrets(err.Error(), a.secrets...))
	}
	return a.queryError(what, err)
}

// ExecDDL implements relational.Adapter.
func (a *Adapter) ExecDDL(ctx context.Context, sql string) error {
	_, err := a.conn.Exec(ctx, sql)
	return a.writeError("running a plan's schema statement", err)
}

// UpsertRows implements relational.Adapter.
func (a *Adapter) UpsertRows(ctx context.Context, b relational.UpsertBatch) error {
	if len(b.Rows) == 0 {
		return nil
	}
	args, err := columnArrays(b.Columns, b.Rows)
	if err != nil {
		return err
	}
	isKey := map[string]bool{}
	for _, k := range b.Key {
		isKey[k] = true
	}
	cols := make([]string, len(b.Columns))
	vals := make([]string, len(b.Columns))
	var set []string
	for i, c := range b.Columns {
		cols[i] = quoteIdent(c.Name)
		vals[i] = fmt.Sprintf("CAST(u.c%d AS %s)", i, castType(c))
		if !isKey[c.Name] {
			set = append(set, cols[i]+" = EXCLUDED."+cols[i])
		}
	}
	keys := make([]string, len(b.Key))
	for i, k := range b.Key {
		keys[i] = quoteIdent(k)
	}
	action := "DO NOTHING"
	if len(set) > 0 {
		action = "DO UPDATE SET " + strings.Join(set, ", ")
	}
	sql := "INSERT INTO " + tableRef(b.Table).sql() + " (" + strings.Join(cols, ", ") + ") SELECT " +
		strings.Join(vals, ", ") + " FROM " + unnest(len(b.Columns), false) +
		" ON CONFLICT (" + strings.Join(keys, ", ") + ") " + action
	_, err = a.conn.Exec(ctx, sql, args...)
	return a.writeError("writing rows to "+b.Table.Schema+"."+b.Table.Name, err)
}

// UpdateRows implements relational.Adapter.
func (a *Adapter) UpdateRows(ctx context.Context, b relational.UpdateBatch) error {
	if len(b.Rows) == 0 {
		return nil
	}
	all := append(append([]relational.Column{}, b.Key...), b.Columns...)
	args, err := columnArrays(all, b.Rows)
	if err != nil {
		return err
	}
	set := make([]string, len(b.Columns))
	for i, c := range b.Columns {
		set[i] = fmt.Sprintf("%s = CAST(u.c%d AS %s)", quoteIdent(c.Name), len(b.Key)+i, castType(c))
	}
	where := make([]string, len(b.Key))
	for i, k := range b.Key {
		where[i] = fmt.Sprintf("t.%s = CAST(u.c%d AS %s)", quoteIdent(k.Name), i, castType(k))
	}
	sql := "UPDATE " + tableRef(b.Table).sql() + " AS t SET " + strings.Join(set, ", ") +
		" FROM " + unnest(len(all), false) + " WHERE " + strings.Join(where, " AND ")
	_, err = a.conn.Exec(ctx, sql, args...)
	return a.writeError("updating rows of "+b.Table.Schema+"."+b.Table.Name, err)
}

// MissingKeys implements relational.Adapter.
func (a *Adapter) MissingKeys(ctx context.Context, table relational.Table, key []relational.Column, keys [][]ir.Value) ([][]ir.Value, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	args, err := columnArrays(key, keys)
	if err != nil {
		return nil, err
	}
	where := make([]string, len(key))
	for i, k := range key {
		where[i] = fmt.Sprintf("t.%s = CAST(u.c%d AS %s)", quoteIdent(k.Name), i, castType(k))
	}
	sql := "SELECT u.i FROM " + unnest(len(key), true) + " WHERE NOT EXISTS (SELECT 1 FROM " +
		tableRef(table).sql() + " AS t WHERE " + strings.Join(where, " AND ") + ") ORDER BY u.i"
	rows, err := a.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, a.queryError("checking keys of "+table.Schema+"."+table.Name, err)
	}
	defer rows.Close()
	var out [][]ir.Value
	for rows.Next() {
		var i int64
		if err := rows.Scan(&i); err != nil {
			return nil, a.queryError("checking keys of "+table.Schema+"."+table.Name, err)
		}
		out = append(out, keys[i-1])
	}
	if err := rows.Err(); err != nil {
		return nil, a.queryError("checking keys of "+table.Schema+"."+table.Name, err)
	}
	return out, nil
}

// RowsByKey implements relational.Adapter.
func (a *Adapter) RowsByKey(ctx context.Context, table relational.Table, key []relational.Column, columns []relational.Column, keys [][]ir.Value) ([][]ir.Value, error) {
	out := make([][]ir.Value, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	args, err := columnArrays(key, keys)
	if err != nil {
		return nil, err
	}
	sel := make([]string, len(columns))
	for i, c := range columns {
		sel[i] = "to_json(t." + quoteIdent(c.Name) + ")::text"
	}
	where := make([]string, len(key))
	for i, k := range key {
		where[i] = fmt.Sprintf("t.%s = CAST(u.c%d AS %s)", quoteIdent(k.Name), i, castType(k))
	}
	what := "reading rows of " + table.Schema + "." + table.Name
	sql := "SELECT u.i" + strings.Repeat(", ", min(1, len(sel))) + strings.Join(sel, ", ") +
		" FROM " + unnest(len(key), true) + " JOIN " + tableRef(table).sql() + " AS t ON " + strings.Join(where, " AND ")
	rows, err := a.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, a.queryError(what, err)
	}
	defer rows.Close()
	raw := make([]*string, len(columns))
	dest := make([]any, len(columns)+1)
	var i int64
	dest[0] = &i
	for j := range raw {
		dest[j+1] = &raw[j]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, a.queryError(what, err)
		}
		vals := make([]ir.Value, len(columns))
		for j, c := range columns {
			v, err := decodeValue(c, raw[j])
			if err != nil {
				return nil, rtgerr.New(rtgerr.ExecConversion, "%s: column %s: %v", what, c.Name, err)
			}
			vals[j] = v
		}
		out[i-1] = vals
	}
	if err := rows.Err(); err != nil {
		return nil, a.queryError(what, err)
	}
	return out, nil
}
