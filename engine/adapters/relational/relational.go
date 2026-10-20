// Package relational defines the interface every relational database adapter
// implements. All database-specific behavior lives behind it.
//
// STABILITY: unstable. Milestone 1 defines inspection only; reading, batched
// upserts and transactions are added with the execution engine in Milestone 2,
// when their shape is driven by a real implementation.
package relational

import (
	"context"

	"github.com/TuanFaied/rtogtor/engine/schemameta"
)

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
	// Close releases the connection.
	Close() error
}
