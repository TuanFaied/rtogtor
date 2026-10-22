// Package postgresql implements the relational adapter for PostgreSQL.
//
// Schema inspection uses explicit pg_catalog queries inside one read-only,
// repeatable-read transaction, so the metadata is a consistent snapshot and
// inspection can never modify the database.
package postgresql

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/internal/redact"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// Product is the schema-metadata product name.
const Product = "postgresql"

// DefaultSchema is inspected when no schema is requested.
const DefaultSchema = "public"

// Adapter is a PostgreSQL connection.
type Adapter struct {
	conn    *pgx.Conn
	secrets []string
}

var _ relational.Adapter = (*Adapter)(nil)

// Open connects to PostgreSQL. connURL is a postgres:// URL or keyword/value
// connection string. Errors never contain the URL, password or query
// parameters.
func Open(ctx context.Context, connURL string) (*Adapter, error) {
	secrets := redact.URLSecrets(connURL)
	cfg, err := pgx.ParseConfig(connURL)
	if err != nil {
		return nil, rtgerr.New(rtgerr.ConnConfig, "invalid PostgreSQL connection settings: %s",
			redact.Secrets(err.Error(), secrets...))
	}
	if cfg.Password != "" {
		secrets = append(secrets, cfg.Password)
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["application_name"] = "rtogtor"

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, rtgerr.New(rtgerr.ConnFailed, "cannot connect to PostgreSQL: %s",
			redact.Secrets(err.Error(), secrets...))
	}
	return &Adapter{conn: conn, secrets: secrets}, nil
}

// Capabilities reports PostgreSQL's capabilities.
func (a *Adapter) Capabilities() relational.Capabilities {
	return relational.Capabilities{KeysetPagination: true, Upsert: true, TransactionalBatches: true}
}

// Close closes the connection.
func (a *Adapter) Close() error {
	return a.conn.Close(context.Background())
}

// queryError converts a driver error to RTG-PG-001 without leaking secrets.
func (a *Adapter) queryError(what string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	msg := redact.Secrets(err.Error(), a.secrets...)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return rtgerr.New(rtgerr.PGQuery, "%s failed (SQLSTATE %s): %s", what, pgErr.Code, redact.Secrets(pgErr.Message, a.secrets...))
	}
	return rtgerr.New(rtgerr.PGQuery, "%s failed: %s", what, msg)
}

// Inspect returns metadata for the requested schemas.
func (a *Adapter) Inspect(ctx context.Context, opts relational.InspectOptions) (*schemameta.Document, error) {
	names := uniqueSorted(opts.Schemas)
	if len(names) == 0 {
		names = []string{DefaultSchema}
	}

	tx, err := a.conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, a.queryError("starting a read-only transaction", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	doc := &schemameta.Document{SpecVersion: spec.Version, Kind: schemameta.KindRelational}
	doc.Database.Product = Product
	if err := tx.QueryRow(ctx, `SELECT current_setting('server_version'), current_database()`).
		Scan(&doc.Database.Version, &doc.Database.Name); err != nil {
		return nil, a.queryError("reading the server version", err)
	}

	in := &inspector{tx: tx, adapter: a, types: map[uint32]*pgType{}}
	for _, name := range names {
		s, err := in.schema(ctx, name)
		if err != nil {
			return nil, err
		}
		doc.Schemas = append(doc.Schemas, *s)
	}
	doc.Warnings = in.warnings
	sort.Slice(doc.Warnings, func(i, j int) bool {
		if doc.Warnings[i].Code != doc.Warnings[j].Code {
			return doc.Warnings[i].Code < doc.Warnings[j].Code
		}
		return doc.Warnings[i].Path < doc.Warnings[j].Path
	})
	return doc, nil
}

type inspector struct {
	tx       pgx.Tx
	adapter  *Adapter
	types    map[uint32]*pgType
	warnings []schemameta.Warning
}

func (in *inspector) warn(code rtgerr.Code, path, format string, args ...any) {
	in.warnings = append(in.warnings, schemameta.Warning{Code: string(code), Path: path, Message: fmt.Sprintf(format, args...)})
}

type columnRow struct {
	table      string
	col        schemameta.Column
	typeOID    uint32
	typmod     int32
	nativeType string
}

func (in *inspector) schema(ctx context.Context, name string) (*schemameta.Schema, error) {
	var exists bool
	if err := in.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, name).Scan(&exists); err != nil {
		return nil, in.adapter.queryError("looking up schema", err)
	}
	if !exists {
		return nil, rtgerr.New(rtgerr.SchemaNotFound, "schema %q does not exist or is not visible", name)
	}

	// Tables: ordinary and partitioned tables, excluding individual partitions.
	tables := map[string]*schemameta.Table{}
	rows, err := in.tx.Query(ctx, `
		SELECT c.relname::text, c.reltuples::float8
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition`, name)
	if err != nil {
		return nil, in.adapter.queryError("listing tables", err)
	}
	for rows.Next() {
		var tname string
		var reltuples float64
		if err := rows.Scan(&tname, &reltuples); err != nil {
			rows.Close()
			return nil, in.adapter.queryError("listing tables", err)
		}
		t := &schemameta.Table{Name: tname, ForeignKeys: []schemameta.ForeignKey{}, UniqueConstraints: []schemameta.KeyConstraint{}}
		if reltuples >= 0 { // -1 means "never analyzed"
			t.EstimatedRows = ptr(int64(math.Round(reltuples)))
		}
		tables[tname] = t
	}
	if err := rows.Err(); err != nil {
		return nil, in.adapter.queryError("listing tables", err)
	}

	// Columns. Collected first: type lookups need the connection.
	var cols []columnRow
	rows, err = in.tx.Query(ctx, `
		SELECT c.relname::text, a.attnum, a.attname::text, NOT a.attnotnull, a.atthasdef,
		       (a.attidentity <> '' OR a.attgenerated <> ''),
		       a.atttypid, a.atttypmod, format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		  AND a.attnum > 0 AND NOT a.attisdropped`, name)
	if err != nil {
		return nil, in.adapter.queryError("listing columns", err)
	}
	for rows.Next() {
		var r columnRow
		var ordinal int16
		if err := rows.Scan(&r.table, &ordinal, &r.col.Name, &r.col.Nullable, &r.col.HasDefault,
			&r.col.Generated, &r.typeOID, &r.typmod, &r.nativeType); err != nil {
			rows.Close()
			return nil, in.adapter.queryError("listing columns", err)
		}
		r.col.Ordinal = int(ordinal)
		cols = append(cols, r)
	}
	if err := rows.Err(); err != nil {
		return nil, in.adapter.queryError("listing columns", err)
	}
	for _, r := range cols {
		info, reason, err := mapType(func(oid uint32) (*pgType, error) { return in.lookupType(ctx, oid) }, r.typeOID, r.typmod, 0)
		if err != nil {
			return nil, err
		}
		info.NativeType = r.nativeType
		if info.Type == nil {
			info = schemameta.TypeInfo{NativeType: r.nativeType}
			in.warn(rtgerr.SchemaUnsupportedType, name+"."+r.table+"."+r.col.Name, "%s", reason)
		}
		r.col.TypeInfo = info
		if t, ok := tables[r.table]; ok {
			t.Columns = append(t.Columns, r.col)
		}
	}

	// Primary-key, unique and foreign-key constraints, with columns in
	// constraint order.
	rows, err = in.tx.Query(ctx, `
		SELECT con.conname::text, con.contype::text, cls.relname::text,
		       ARRAY(SELECT a.attname::text
		             FROM unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
		             JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
		             ORDER BY k.ord),
		       COALESCE(fns.nspname::text, ''), COALESCE(fcls.relname::text, ''),
		       ARRAY(SELECT a.attname::text
		             FROM unnest(con.confkey) WITH ORDINALITY AS k(attnum, ord)
		             JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum
		             ORDER BY k.ord),
		       con.confdeltype::text, con.confupdtype::text
		FROM pg_constraint con
		JOIN pg_class cls ON cls.oid = con.conrelid
		JOIN pg_namespace ns ON ns.oid = cls.relnamespace
		LEFT JOIN pg_class fcls ON fcls.oid = con.confrelid
		LEFT JOIN pg_namespace fns ON fns.oid = fcls.relnamespace
		WHERE ns.nspname = $1 AND con.contype IN ('p', 'u', 'f')`, name)
	if err != nil {
		return nil, in.adapter.queryError("listing constraints", err)
	}
	for rows.Next() {
		var cname, ctype, tname, refSchema, refTable, onDel, onUpd string
		var columns, refColumns []string
		if err := rows.Scan(&cname, &ctype, &tname, &columns, &refSchema, &refTable, &refColumns, &onDel, &onUpd); err != nil {
			rows.Close()
			return nil, in.adapter.queryError("listing constraints", err)
		}
		t, ok := tables[tname]
		if !ok {
			continue // constraint on a partition or non-table relation
		}
		switch ctype {
		case "p":
			t.PrimaryKey = &schemameta.KeyConstraint{Name: cname, Columns: columns}
		case "u":
			t.UniqueConstraints = append(t.UniqueConstraints, schemameta.KeyConstraint{Name: cname, Columns: columns})
		case "f":
			t.ForeignKeys = append(t.ForeignKeys, schemameta.ForeignKey{
				Name: cname, Columns: columns,
				ReferencedSchema: refSchema, ReferencedTable: refTable, ReferencedColumns: refColumns,
				OnDelete: fkAction(onDel), OnUpdate: fkAction(onUpd),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, in.adapter.queryError("listing constraints", err)
	}

	// Assemble in the order the schema documents.
	s := &schemameta.Schema{Name: name, Tables: []schemameta.Table{}}
	for _, t := range tables {
		sort.Slice(t.Columns, func(i, j int) bool { return t.Columns[i].Ordinal < t.Columns[j].Ordinal })
		sort.Slice(t.ForeignKeys, func(i, j int) bool { return t.ForeignKeys[i].Name < t.ForeignKeys[j].Name })
		sort.Slice(t.UniqueConstraints, func(i, j int) bool { return t.UniqueConstraints[i].Name < t.UniqueConstraints[j].Name })
		if t.Columns == nil {
			t.Columns = []schemameta.Column{}
		}
		if t.PrimaryKey == nil {
			in.warn(rtgerr.SchemaNoPrimaryKey, name+"."+t.Name, "table has no primary key")
		}
		s.Tables = append(s.Tables, *t)
	}
	sort.Slice(s.Tables, func(i, j int) bool { return s.Tables[i].Name < s.Tables[j].Name })
	return s, nil
}

func (in *inspector) lookupType(ctx context.Context, oid uint32) (*pgType, error) {
	if t, ok := in.types[oid]; ok {
		return t, nil
	}
	t := &pgType{OID: oid}
	var typtype, category string
	err := in.tx.QueryRow(ctx, `
		SELECT n.nspname::text, t.typname::text, t.typtype::text, t.typcategory::text,
		       t.typelem, t.typbasetype, t.typtypmod
		FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE t.oid = $1`, oid).Scan(&t.Namespace, &t.Name, &typtype, &category, &t.Elem, &t.BaseType, &t.BaseMod)
	if err != nil {
		return nil, in.adapter.queryError("reading type information", err)
	}
	t.Type, t.Category = typtype[0], category[0]
	if t.Type == 'e' {
		rows, err := in.tx.Query(ctx, `SELECT enumlabel::text FROM pg_enum WHERE enumtypid = $1 ORDER BY enumsortorder`, oid)
		if err != nil {
			return nil, in.adapter.queryError("reading enum values", err)
		}
		labels, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, in.adapter.queryError("reading enum values", err)
		}
		t.EnumVals = labels
	}
	in.types[oid] = t
	return t, nil
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
