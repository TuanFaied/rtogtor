// Package graph defines the interface every graph database adapter
// implements. All database-specific behavior lives behind it.
//
// STABILITY: unstable until 1.0.
package graph

import (
	"context"

	"github.com/TuanFaied/rtogtor/engine/ir"
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
type InspectOptions struct{}

// LossKind is a kind of lossy transformation (specs/values.md §5), or "" for
// none.
type LossKind string

// Loss kinds.
const (
	LossNone           LossKind = ""
	LossPrecision      LossKind = "precision"
	LossRange          LossKind = "range"
	LossRepresentation LossKind = "representation"
	LossStructure      LossKind = "structure"
)

// TypeSupport describes how a graph database stores logical types. It needs
// no connection, so planning can run before connecting.
type TypeSupport interface {
	// Product is the database product name, e.g. "neo4j".
	Product() string
	// Supports reports whether values of the type can be written. elem is
	// the element type for arrays.
	Supports(t, elem ir.LogicalType) bool
	// StorageLoss reports the loss incurred by storing the type, if any.
	StorageLoss(t, elem ir.LogicalType) LossKind
	// StoredAs names the database type values are stored as, for plans.
	StoredAs(t, elem ir.LogicalType) string
}

// Property is a value to store, with its logical type.
type Property struct {
	Name    string
	Type    ir.LogicalType
	Element ir.LogicalType
	Value   ir.Value
}

// Encoded is a set of properties converted for one adapter. Only the adapter
// that produced it may consume it.
type Encoded map[string]any

// NodeRow is one node to merge.
type NodeRow struct {
	ID    string
	Props Encoded
}

// NodeBatch merges nodes by identity: nodes are matched on
// (Labels[0], IDProperty), created if missing, given every label, and
// updated with the properties. A property whose value is null is removed.
type NodeBatch struct {
	Labels     []string
	IDProperty string
	Rows       []NodeRow
}

// RelationshipRow is one relationship to merge.
type RelationshipRow struct {
	From  string // identity of the start node
	To    string // identity of the end node
	Props Encoded
}

// RelationshipBatch merges relationships of one type between nodes found
// by identity.
type RelationshipBatch struct {
	Type       string
	FromLabel  string
	ToLabel    string
	IDProperty string
	Rows       []RelationshipRow
}

// ServerInfo describes the connected server. It never holds credentials.
type ServerInfo struct {
	Product  string
	Version  string
	Edition  string
	Database string
}

// Adapter is a connection to one graph database.
//
// Implementations must never include credentials or connection URIs in
// errors, logs or returned metadata. Errors that may succeed on retry carry
// a retryable error code.
type Adapter interface {
	TypeSupport
	// Capabilities reports the adapter's capabilities.
	Capabilities() Capabilities
	// ServerInfo reports the server version and database.
	ServerInfo(ctx context.Context) (ServerInfo, error)
	// Inspect returns metadata conforming to specs/schema-metadata.schema.json.
	Inspect(ctx context.Context, opts InspectOptions) (*schemameta.Document, error)
	// EnsureIdentityConstraint creates, if absent, a uniqueness constraint on
	// property for label. It must be called before loading data.
	EnsureIdentityConstraint(ctx context.Context, label, property string) error
	// EncodeProperties converts properties for writing. It fails with
	// RTG-EXEC-001 for values the database cannot store.
	EncodeProperties(props []Property) (Encoded, error)
	// MergeNodes merges one batch in a single transaction.
	MergeNodes(ctx context.Context, batch NodeBatch) error
	// MergeRelationships merges one batch in a single transaction and
	// returns the indexes of rows whose start or end node does not exist;
	// those rows are not written.
	MergeRelationships(ctx context.Context, batch RelationshipBatch) (missing []int, err error)
	// CountNodes counts nodes with the label.
	CountNodes(ctx context.Context, label string) (int64, error)
	// CountRelationships counts relationships of the type between nodes with
	// the given labels.
	CountRelationships(ctx context.Context, relType, fromLabel, toLabel string) (int64, error)
	// MissingIDs returns the ids that have no node with the label.
	MissingIDs(ctx context.Context, label, property string, ids []string) ([]string, error)
	// Close releases the connection.
	Close() error
}
