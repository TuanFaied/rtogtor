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
	// ReadableFrom lists the database property types a logical type can be
	// read from (graph → relational), as named by schema inspection.
	ReadableFrom(t, elem ir.LogicalType) []string
	// KeyStoredAs names the only property type a key property of the
	// logical type may have, so key-ordered reads compare like with like.
	// ok is false when the type cannot be a key in key-ordered reads.
	KeyStoredAs(t ir.LogicalType) (storedAs string, ok bool)
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

// NodeField is a node property to read, with the logical type to produce.
type NodeField struct {
	Property string
	Type     ir.LogicalType
	Element  ir.LogicalType
}

// RelatedRead reads, for every node, the nodes at the other end of its
// relationships of one type.
type RelatedRead struct {
	Type string
	// Outgoing reads (n)-[:Type]->(m); otherwise (n)<-[:Type]-(m).
	Outgoing bool
	// Labels the related node must carry to match.
	Labels []string
	// Key properties of the related node. They must have the type
	// TypeSupport.KeyStoredAs names to match.
	Key []NodeField
}

// NodeCursor is the exclusive position of a key-ordered read.
type NodeCursor struct {
	Key       []ir.Value
	ElementID string
}

// NodeReadRequest reads one batch of nodes ordered by key, then element ID.
type NodeReadRequest struct {
	// Labels the nodes must all carry.
	Labels []string
	// Key properties define the order and the cursor. Nodes missing a key
	// property, or holding it with another type than TypeSupport.KeyStoredAs
	// names, are never returned (see CountNodesWithoutKey).
	Key        []NodeField
	Properties []NodeField
	Related    []RelatedRead
	// After is the exclusive cursor; nil starts at the beginning.
	After *NodeCursor
	Limit int
}

// RelatedNode is one node at the other end of a relationship.
type RelatedNode struct {
	// Matches reports whether the node has every RelatedRead label and key
	// property. Key is set only when it matches.
	Matches bool
	Key     []ir.Value
}

// NodeRecord is one node read from a graph source.
type NodeRecord struct {
	ElementID string
	// Key holds the key values in request order. It is always set.
	Key []ir.Value
	// Values holds the request's properties, in order. It is nil when Err
	// is set.
	Values []ir.Value
	// Related holds, per RelatedRead, up to two related nodes: enough to
	// tell none, one, and more than one apart.
	Related [][]RelatedNode
	// Err reports a value that could not be converted to its logical type
	// (RTG-EXEC-001). The record is still returned, so the caller can
	// record it and advance the cursor past it.
	Err error
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
	// ReadNodes returns up to req.Limit nodes after req.After, ordered by
	// key then element ID, in one read transaction. Fewer nodes than the
	// limit means the end was reached.
	ReadNodes(ctx context.Context, req NodeReadRequest) ([]NodeRecord, error)
	// CountKeyedNodes counts nodes with all labels that ReadNodes returns.
	CountKeyedNodes(ctx context.Context, labels []string, key []NodeField) (int64, error)
	// CountNodesWithoutKey counts nodes with all labels that ReadNodes skips.
	CountNodesWithoutKey(ctx context.Context, labels []string, key []NodeField) (int64, error)
	// CountRelated counts nodes with all labels and a key that have exactly
	// one related node of rel, which matches.
	CountRelated(ctx context.Context, labels []string, key []NodeField, rel RelatedRead) (int64, error)
	// Close releases the connection.
	Close() error
}
