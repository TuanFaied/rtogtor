// Package relational defines the interface every relational database adapter
// implements. All database-specific behavior lives behind it.
//
// STABILITY: unstable until 1.0. Adapters read in key order (relational →
// graph) and write batched upserts and updates (graph → relational).
package relational

import (
	"context"

	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
)

// TypeSupport describes how a relational database declares columns for
// logical types. It needs no connection, so planning can run before
// connecting.
type TypeSupport interface {
	// Product is the database product name, e.g. "postgresql".
	Product() string
	// ColumnType returns the column type used when the plan creates a
	// table, or "" if the type cannot be stored.
	ColumnType(t, elem ir.LogicalType) string
	// DefaultFractionalDigits is the fractional-second precision of the
	// temporal column types ColumnType returns.
	DefaultFractionalDigits() int
}

// Capabilities reports what an adapter supports, so the planner can reject
// mappings it cannot execute instead of failing mid-run.
type Capabilities struct {
	// KeysetPagination: rows can be read in key order after a cursor, which
	// resumable reads require.
	KeysetPagination bool
	// Upsert: idempotent INSERT … ON CONFLICT writes.
	Upsert bool
	// TransactionalBatches: a batch of writes commits atomically.
	TransactionalBatches bool
}

// InspectOptions selects what to inspect.
type InspectOptions struct {
	// Schemas to inspect. Empty means the adapter's default schema
	// ("public" for PostgreSQL).
	Schemas []string
}

// Column is a column to read, with the logical type to produce.
type Column struct {
	Name string
	Type ir.LogicalType
	// Element is the element type when Type is array.
	Element ir.LogicalType
	// NativeType is the column's database type as reported by inspection.
	// Adapters use it to bind cursor values.
	NativeType string
}

// Table names a table.
type Table struct {
	Schema string
	Name   string
}

// ReadRequest reads one batch of rows in key order.
type ReadRequest struct {
	Table Table
	// Key columns define the order and the cursor. Rows whose key contains
	// NULL are never returned (see CountNullKeys).
	Key []Column
	// Columns are read in addition to the key.
	Columns []Column
	// After is the exclusive cursor: the key of the last row already read.
	// Nil starts at the beginning.
	After []ir.Value
	// Limit is the maximum number of rows (the batch size).
	Limit int
}

// Row is one source row.
type Row struct {
	// Key holds the key values in ReadRequest.Key order. It is always set.
	Key []ir.Value
	// Values holds the values of ReadRequest.Columns, in order. It is nil
	// when Err is set.
	Values []ir.Value
	// Err reports a value that could not be converted to its logical type
	// (RTG-EXEC-001). The row is still returned so the caller can record it
	// and advance the cursor past it.
	Err error
}

// Reference describes rows of an owner table that reference a target table
// through a set of columns.
type Reference struct {
	Owner         Table
	Columns       []string
	Target        Table
	TargetColumns []string
}

// ReferenceCount counts owner rows whose reference columns are all non-null.
type ReferenceCount struct {
	// Matched rows reference an existing target row.
	Matched int64
	// Dangling rows reference a key that does not exist in the target.
	Dangling int64
}

// UpsertBatch inserts rows, or updates the existing row with the same key.
type UpsertBatch struct {
	Table Table
	// Key columns name the conflict target: a primary key or unique
	// constraint on exactly these columns. They must be among Columns.
	Key     []string
	Columns []Column
	// Rows hold one value per column, in Columns order.
	Rows [][]ir.Value
}

// UpdateBatch sets columns of existing rows found by key. Rows whose key
// does not exist are not changed.
type UpdateBatch struct {
	Table   Table
	Key     []Column
	Columns []Column
	// Rows hold the key values followed by the column values.
	Rows [][]ir.Value
}

// Adapter is a connection to one relational database.
//
// Implementations must never include credentials or connection strings in
// errors, logs or returned metadata.
type Adapter interface {
	// Capabilities reports the adapter's capabilities.
	Capabilities() Capabilities
	// Inspect returns schema metadata conforming to
	// specs/schema-metadata.schema.json, with every array in the order the
	// schema documents, so output is deterministic.
	Inspect(ctx context.Context, opts InspectOptions) (*schemameta.Document, error)
	// ReadBatch returns up to req.Limit rows after req.After in key order.
	// Fewer rows than the limit means the end of the table was reached.
	ReadBatch(ctx context.Context, req ReadRequest) ([]Row, error)
	// CountRows counts rows whose key columns are all non-null.
	CountRows(ctx context.Context, table Table, key []string) (int64, error)
	// CountNullKeys counts rows with at least one NULL key column.
	CountNullKeys(ctx context.Context, table Table, key []string) (int64, error)
	// CountReferences counts matched and dangling references.
	CountReferences(ctx context.Context, ref Reference) (ReferenceCount, error)
	// ExecDDL runs a schema statement from a plan, such as
	// CREATE TABLE IF NOT EXISTS.
	ExecDDL(ctx context.Context, sql string) error
	// UpsertRows writes one batch in a single transaction. A batch rejected
	// because of its data fails with RTG-PG-002, so the caller can retry its
	// rows one at a time to find the rejected ones.
	UpsertRows(ctx context.Context, batch UpsertBatch) error
	// UpdateRows updates one batch in a single transaction. Errors are as
	// for UpsertRows.
	UpdateRows(ctx context.Context, batch UpdateBatch) error
	// MissingKeys returns the keys (in canonical text) that have no row.
	MissingKeys(ctx context.Context, table Table, key []Column, keys [][]ir.Value) ([][]ir.Value, error)
	// Close releases the connection.
	Close() error
}
