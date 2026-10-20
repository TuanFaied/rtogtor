// Package graph defines the interface every graph database adapter
// implements. All database-specific behavior lives behind it.
//
// STABILITY: unstable. Milestone 1 defines inspection and identity
// constraints; batched node and relationship writes (UNWIND + MERGE) are added
// with the execution engine in Milestone 2.
package graph

import (
	"context"

	"github.com/TuanFaied/rtogtor/engine/schemameta"
)

// Capabilities reports what an adapter supports.
type Capabilities struct {
	// UniquenessConstraints: the database can enforce a unique identity
	// property per label, which idempotent MERGE writes require.
	UniquenessConstraints bool
	// TransactionalBatches: a batch of writes commits atomically.
	TransactionalBatches bool
	// SchemaInspection: labels and relationship types can be listed with
	// their properties.
	SchemaInspection bool
}

// InspectOptions controls graph inspection.
type InspectOptions struct {
	// SampleSize bounds how many nodes/relationships per label/type are
	// sampled to infer property types. Zero means the adapter default.
	SampleSize int
}

// Adapter is a connection to one graph database.
//
// Implementations must never include credentials or connection URIs in
// errors, logs or returned metadata.
type Adapter interface {
	// Capabilities reports the adapter's capabilities.
	Capabilities() Capabilities
	// Inspect returns metadata conforming to specs/schema-metadata.schema.json.
	Inspect(ctx context.Context, opts InspectOptions) (*schemameta.Document, error)
	// EnsureIdentityConstraint creates, if absent, a uniqueness constraint on
	// property for label. It must be called before loading data.
	EnsureIdentityConstraint(ctx context.Context, label, property string) error
	// Close releases the connection.
	Close() error
}
