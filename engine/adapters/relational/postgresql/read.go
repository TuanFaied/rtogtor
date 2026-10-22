package postgresql

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/internal/canonjson"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

// Values are read with to_json(column). PostgreSQL's JSON output is
// type-faithful and locale-independent: numbers keep their exact digits,
// timestamps use ISO 8601 with 'T', bpchar keeps its padding, and json/jsonb
// arrive as embedded documents. Each value is then canonicalized per
// specs/values.md.

func quoteIdent(parts ...string) string {
	return pgx.Identifier(parts).Sanitize()
}

// tableRef renders a relational.Table as a quoted SQL name.
type tableRef relational.Table

func (t tableRef) sql() string { return quoteIdent(t.Schema, t.Name) }

// ReadBatch implements relational.Adapter.
func (a *Adapter) ReadBatch(ctx context.Context, req relational.ReadRequest) ([]relational.Row, error) {
	if len(req.Key) == 0 || req.Limit <= 0 {
		return nil, rtgerr.New(rtgerr.Internal, "ReadBatch needs key columns and a positive limit")
	}
	if req.After != nil && len(req.After) != len(req.Key) {
		return nil, rtgerr.New(rtgerr.Internal, "cursor has %d values for %d key columns", len(req.After), len(req.Key))
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	all := append(append([]relational.Column{}, req.Key...), req.Columns...)
	for i, c := range all {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("to_json(" + quoteIdent(c.Name) + ")::text")
	}
	sb.WriteString(" FROM " + tableRef(req.Table).sql() + " WHERE ")
	keyCols := make([]string, len(req.Key))
	for i, k := range req.Key {
		keyCols[i] = quoteIdent(k.Name)
		if i > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString(keyCols[i] + " IS NOT NULL")
	}
	var args []any
	if req.After != nil {
		params := make([]string, len(req.Key))
		for i, k := range req.Key {
			v, err := cursorParam(k, req.After[i])
			if err != nil {
				return nil, err
			}
			args = append(args, v)
			// The native type comes from the catalog (format_type), never
			// from user input.
			params[i] = fmt.Sprintf("CAST($%d AS %s)", i+1, k.NativeType)
		}
		sb.WriteString(" AND (" + strings.Join(keyCols, ", ") + ") > (" + strings.Join(params, ", ") + ")")
	}
	sb.WriteString(" ORDER BY " + strings.Join(keyCols, ", "))
	fmt.Fprintf(&sb, " LIMIT %d", req.Limit)

	rows, err := a.conn.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, a.queryError("reading "+req.Table.Schema+"."+req.Table.Name, err)
	}
	defer rows.Close()

	var out []relational.Row
	raw := make([]*string, len(all))
	dest := make([]any, len(all))
	for i := range raw {
		dest[i] = &raw[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, a.queryError("reading "+req.Table.Schema+"."+req.Table.Name, err)
		}
		row := relational.Row{Key: make([]ir.Value, len(req.Key))}
		for i, k := range req.Key {
			v, err := decodeValue(k, raw[i])
			if err != nil {
				// Key values come from NOT NULL keyset columns with
				// identity-permitted types; failing here is a defect.
				return nil, rtgerr.New(rtgerr.Internal, "key column %s: %v", k.Name, err)
			}
			row.Key[i] = v
		}
		vals := make([]ir.Value, len(req.Columns))
		for j, c := range req.Columns {
			v, err := decodeValue(c, raw[len(req.Key)+j])
			if err != nil {
				row.Err = rtgerr.New(rtgerr.ExecConversion, "column %s: %v", c.Name, err)
				break
			}
			vals[j] = v
		}
		if row.Err == nil {
			row.Values = vals
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, a.queryError("reading "+req.Table.Schema+"."+req.Table.Name, err)
	}
	return out, nil
}

// cursorParam converts a canonical key value to PostgreSQL input text.
func cursorParam(c relational.Column, v ir.Value) (string, error) {
	if v.Null {
		return "", rtgerr.New(rtgerr.Internal, "cursor value for %s is null", c.Name)
	}
	if c.Type == ir.TypeBytes {
		b, err := base64.StdEncoding.DecodeString(v.Text)
		if err != nil {
			return "", rtgerr.New(rtgerr.Internal, "cursor value for %s: %v", c.Name, err)
		}
		return `\x` + hex.EncodeToString(b), nil
	}
	return v.Text, nil
}

// decodeValue converts a to_json() text into a canonical value.
func decodeValue(c relational.Column, raw *string) (ir.Value, error) {
	if raw == nil {
		return ir.NullValue(), nil
	}
	return decodeJSON(c.Type, c.Element, []byte(*raw))
}

func decodeJSON(t, elem ir.LogicalType, data []byte) (ir.Value, error) {
	if t == ir.TypeJSON {
		canon, err := canonjson.Canonicalize(data)
		if err != nil {
			return ir.Value{}, err
		}
		return ir.Value{JSON: canon}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return ir.Value{}, err
	}
	return fromJSON(t, elem, v)
}

func fromJSON(t, elem ir.LogicalType, v any) (ir.Value, error) {
	if v == nil {
		return ir.NullValue(), nil
	}
	if t == ir.TypeArray {
		items, ok := v.([]any)
		if !ok {
			return ir.Value{}, fmt.Errorf("expected an array, got %T", v)
		}
		out := ir.Value{Elements: make([]ir.Value, len(items))}
		for i, item := range items {
			if _, nested := item.([]any); nested {
				return ir.Value{}, errors.New("multi-dimensional arrays are not supported")
			}
			e, err := fromJSON(elem, "", item)
			if err != nil {
				return ir.Value{}, fmt.Errorf("element %d: %w", i, err)
			}
			out.Elements[i] = e
		}
		return out, nil
	}
	if t == ir.TypeJSON {
		b, err := json.Marshal(v)
		if err != nil {
			return ir.Value{}, err
		}
		canon, err := canonjson.Canonicalize(b)
		if err != nil {
			return ir.Value{}, err
		}
		return ir.Value{JSON: canon}, nil
	}

	var text string
	switch x := v.(type) {
	case json.Number:
		text = string(x)
	case bool:
		text = "false"
		if x {
			text = "true"
		}
	case string:
		text = x
		if t == ir.TypeBytes {
			// bytea arrives as "\x<hex>".
			if !strings.HasPrefix(x, `\x`) {
				return ir.Value{}, errors.New(`bytea value is not in hex format`)
			}
			b, err := hex.DecodeString(x[2:])
			if err != nil {
				return ir.Value{}, err
			}
			text = base64.StdEncoding.EncodeToString(b)
		}
	default:
		return ir.Value{}, fmt.Errorf("unexpected JSON %T for %s", v, t)
	}
	canon, err := ir.Canonicalize(t, text)
	if err != nil {
		return ir.Value{}, err
	}
	return ir.TextValue(canon), nil
}

// allNotNull returns "c1 IS NOT NULL AND c2 IS NOT NULL ...".
func allNotNull(cols []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = quoteIdent(c) + " IS NOT NULL"
	}
	return strings.Join(parts, " AND ")
}

// anyNull returns "c1 IS NULL OR c2 IS NULL ...".
func anyNull(cols []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = quoteIdent(c) + " IS NULL"
	}
	return strings.Join(parts, " OR ")
}

// CountRows implements relational.Adapter.
func (a *Adapter) CountRows(ctx context.Context, table relational.Table, key []string) (int64, error) {
	var n int64
	q := "SELECT count(*) FROM " + tableRef(table).sql() + " WHERE " + allNotNull(key)
	if err := a.conn.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, a.queryError("counting "+table.Name, err)
	}
	return n, nil
}

// CountNullKeys implements relational.Adapter.
func (a *Adapter) CountNullKeys(ctx context.Context, table relational.Table, key []string) (int64, error) {
	var n int64
	q := "SELECT count(*) FROM " + tableRef(table).sql() + " WHERE " + anyNull(key)
	if err := a.conn.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, a.queryError("counting null keys in "+table.Name, err)
	}
	return n, nil
}

// CountReferences implements relational.Adapter.
func (a *Adapter) CountReferences(ctx context.Context, ref relational.Reference) (relational.ReferenceCount, error) {
	if len(ref.Columns) == 0 || len(ref.Columns) != len(ref.TargetColumns) {
		return relational.ReferenceCount{}, rtgerr.New(rtgerr.Internal, "reference column counts differ")
	}
	on := make([]string, len(ref.Columns))
	for i := range ref.Columns {
		on[i] = "o." + quoteIdent(ref.Columns[i]) + " = t." + quoteIdent(ref.TargetColumns[i])
	}
	ownerNotNull := make([]string, len(ref.Columns))
	for i, c := range ref.Columns {
		ownerNotNull[i] = "o." + quoteIdent(c) + " IS NOT NULL"
	}
	// EXISTS avoids multiplying rows if the target key is not unique.
	q := "SELECT count(*) FILTER (WHERE m), count(*) FILTER (WHERE NOT m) FROM (" +
		"SELECT EXISTS (SELECT 1 FROM " + tableRef(ref.Target).sql() + " t WHERE " + strings.Join(on, " AND ") + ") AS m" +
		" FROM " + tableRef(ref.Owner).sql() + " o WHERE " + strings.Join(ownerNotNull, " AND ") + ") s"
	var rc relational.ReferenceCount
	if err := a.conn.QueryRow(ctx, q).Scan(&rc.Matched, &rc.Dangling); err != nil {
		return rc, a.queryError("counting references from "+ref.Owner.Name, err)
	}
	return rc, nil
}
