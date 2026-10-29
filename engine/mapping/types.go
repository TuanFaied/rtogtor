// Package mapping loads and validates RtoGtoR mappings
// (specs/mapping.schema.json).
//
// Validation runs in three stages:
//
//  1. parsing (JSON or YAML, rejecting duplicate keys) — RTG-MAP-002;
//  2. specVersion check — RTG-SPEC-001, then JSON Schema validation — RTG-MAP-001;
//  3. semantic checks that a schema cannot express (unique names, key
//     coverage, foreign-key arity, …) — RTG-MAP-003 and later.
//
// Checks against a live database's schema belong to the planner.
//
// The Go types mirror the JSON Schema by hand until the codegen pipeline
// (Milestone 1, step 6) generates them.
package mapping

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/TuanFaied/rtogtor/engine/internal/canonjson"
	"github.com/TuanFaied/rtogtor/engine/ir"
)

// Direction of a migration.
type Direction string

// Directions.
const (
	RelationalToGraph Direction = "relational-to-graph"
	GraphToRelational Direction = "graph-to-relational"
)

// Mapping is a validated mapping with defaults applied.
type Mapping struct {
	SpecVersion   string         `json:"specVersion"`
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	Direction     Direction      `json:"direction"`
	Source        Endpoint       `json:"source"`
	Destination   Endpoint       `json:"destination"`
	Identity      Identity       `json:"identity"`
	Policies      Policies       `json:"policies"`
	Entities      []Entity       `json:"entities"`
	Relationships []Relationship `json:"relationships,omitempty"`
}

// Endpoint describes one side of the migration. It never holds credentials.
type Endpoint struct {
	Kind     string `json:"kind"`
	Schema   string `json:"schema,omitempty"`
	Database string `json:"database,omitempty"`
}

// Identity strategies.
const (
	StrategyHash    = "hash"
	StrategyNatural = "natural"
)

// DefaultIdentityProperty is the default graph identity property.
const DefaultIdentityProperty = "rtg_id"

// Identity configures how entity identities are stored.
type Identity struct {
	Strategy string `json:"strategy"`
	Property string `json:"property"`
}

// Loss policies.
const (
	LossStrict   = "strict"
	LossFlexible = "flexible"
)

// Policies configures loss handling.
type Policies struct {
	Loss  string          `json:"loss"`
	Allow []LossAllowance `json:"allow,omitempty"`
}

// LossAllowance explicitly allows one kind of lossy transformation.
type LossAllowance struct {
	Kind     string         `json:"kind"`
	Type     ir.LogicalType `json:"type,omitempty"`
	Entity   string         `json:"entity,omitempty"`
	Property string         `json:"property,omitempty"`
	Reason   string         `json:"reason"`
}

// Property maps a column to a property.
type Property struct {
	Column   string         `json:"column"`
	Property string         `json:"property"`
	Type     ir.LogicalType `json:"type"`
	Element  ir.LogicalType `json:"element,omitempty"`
}

// Entity maps a table to nodes with labels.
type Entity struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Schema      string     `json:"schema,omitempty"`
	Table       string     `json:"table"`
	Labels      []string   `json:"labels"`
	Key         []string   `json:"key"`
	Properties  []Property `json:"properties"`
}

// PropertyByColumn returns the property mapped from column, if any.
func (e *Entity) PropertyByColumn(column string) (Property, bool) {
	for _, p := range e.Properties {
		if p.Column == column {
			return p, true
		}
	}
	return Property{}, false
}

// Foreign-key relationship directions.
const (
	OwnerToReferenced = "owner-to-referenced"
	ReferencedToOwner = "referenced-to-owner"
)

// ForeignKey describes a relationship derived from a foreign key.
type ForeignKey struct {
	Entity     string   `json:"entity"`
	Columns    []string `json:"columns"`
	References string   `json:"references"`
	Direction  string   `json:"direction"`
}

// EndpointRef points from junction-table columns to an entity's key.
type EndpointRef struct {
	Entity  string   `json:"entity"`
	Columns []string `json:"columns"`
}

// Junction describes a relationship derived from a junction table.
type Junction struct {
	Schema string      `json:"schema,omitempty"`
	Table  string      `json:"table"`
	From   EndpointRef `json:"from"`
	To     EndpointRef `json:"to"`
}

// Relationship maps a foreign key or junction table to graph relationships.
// Exactly one of ForeignKey and Junction is set.
type Relationship struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Type        string      `json:"type"`
	ForeignKey  *ForeignKey `json:"foreignKey,omitempty"`
	Junction    *Junction   `json:"junction,omitempty"`
	Properties  []Property  `json:"properties,omitempty"`
}

// Entity returns the entity with the given name.
func (m *Mapping) Entity(name string) (*Entity, bool) {
	for i := range m.Entities {
		if m.Entities[i].Name == name {
			return &m.Entities[i], true
		}
	}
	return nil, false
}

func (m *Mapping) applyDefaults() {
	if m.Identity.Strategy == "" {
		m.Identity.Strategy = StrategyHash
	}
	if m.Identity.Property == "" {
		m.Identity.Property = DefaultIdentityProperty
	}
	if m.Policies.Loss == "" {
		m.Policies.Loss = LossStrict
	}
	for _, ep := range []*Endpoint{&m.Source, &m.Destination} {
		if ep.Kind == "postgresql" && ep.Schema == "" {
			ep.Schema = "public"
		}
	}
}

// Hash returns the lowercase hex SHA-256 of the mapping's RFC 8785 canonical
// JSON, with defaults applied. Equivalent JSON and YAML documents have the
// same hash; resuming a run requires an identical hash.
func (m *Mapping) Hash() (string, error) {
	b, err := canonjson.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
