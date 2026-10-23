// Package planning builds deterministic execution plans
// (specs/plan.schema.json) from a mapping, inspected source metadata and the
// destination's type rules.
//
// Planning never connects to a database and never writes anything, so the
// same inputs always yield byte-identical canonical plans.
package planning

import (
	"github.com/TuanFaied/rtogtor/engine/internal/canonjson"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/mapping"
)

// StepKind is the kind of a plan step.
type StepKind string

// Step kinds, in execution order.
const (
	StepConstraint    StepKind = "ensure-identity-constraint"
	StepNodes         StepKind = "load-nodes"
	StepRelationships StepKind = "load-relationships"

	// Graph → relational, in execution order.
	StepTable       StepKind = "ensure-table"
	StepRows        StepKind = "load-rows"
	StepForeignKeys StepKind = "update-foreign-keys"
)

// Loads reports whether the step reads and writes data (and so has a run
// stream with a checkpoint).
func (k StepKind) Loads() bool {
	return k == StepNodes || k == StepRelationships || k == StepRows || k == StepForeignKeys
}

// Plan is an execution plan.
type Plan struct {
	SpecVersion       string            `json:"specVersion"`
	IdentityAlgorithm string            `json:"identityAlgorithm"`
	Mapping           MappingRef        `json:"mapping"`
	Direction         mapping.Direction `json:"direction"`
	Source            Endpoint          `json:"source"`
	Destination       Endpoint          `json:"destination"`
	IdentityProperty  string            `json:"identityProperty"`
	LossPolicy        string            `json:"lossPolicy"`
	Steps             []Step            `json:"steps"`
	Transformations   []Transformation  `json:"transformations"`
	Warnings          []Diagnostic      `json:"warnings"`
}

// MappingRef identifies the mapping a plan was built from.
type MappingRef struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// Endpoint names a database product. Plans never contain connection details.
type Endpoint struct {
	Product  string `json:"product"`
	Database string `json:"database,omitempty"`
}

// TableRef names a source table.
type TableRef struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

// Field is a source column with its logical type.
type Field struct {
	Column     string         `json:"column"`
	Property   string         `json:"property,omitempty"`
	Type       ir.LogicalType `json:"type"`
	Element    ir.LogicalType `json:"element,omitempty"`
	NativeType string         `json:"nativeType"`
	Nullable   bool           `json:"nullable"`
	// Scale and FractionalDigits describe a relational destination column:
	// values with more fractional digits would be rounded.
	Scale            *int `json:"scale,omitempty"`
	FractionalDigits *int `json:"fractionalDigits,omitempty"`
}

// ForeignKeyLoad describes how a graph → relational step fills foreign-key
// columns from a relationship.
type ForeignKeyLoad struct {
	Relationship     string `json:"relationship"`
	RelationshipType string `json:"relationshipType"`
	// Direction is seen from the owner node: "outgoing" for
	// (owner)-[:TYPE]->(referenced), "incoming" for the reverse.
	Direction        string   `json:"direction"`
	References       string   `json:"references"`
	ReferencedLabels []string `json:"referencedLabels"`
	ReferencedKey    []Field  `json:"referencedKey"`
	Columns          []Field  `json:"columns"`
	Deferred         bool     `json:"deferred"`
}

// NodeRef is one end of a relationship.
type NodeRef struct {
	Entity string `json:"entity"`
	Label  string `json:"label"`
}

// ForeignKeyRef describes how a relationship step derives its end node.
type ForeignKeyRef struct {
	// Columns hold the foreign-key columns, typed as the referenced key.
	Columns    []Field `json:"columns"`
	References string  `json:"references"`
	Direction  string  `json:"direction"`
}

// Step is one plan step. Fields are set according to Kind.
type Step struct {
	ID        string   `json:"id"`
	Kind      StepKind `json:"kind"`
	DependsOn []string `json:"dependsOn"`

	// ensure-identity-constraint
	Label    string `json:"label,omitempty"`
	Property string `json:"property,omitempty"`

	// load-nodes and load-relationships: the entity whose table is read.
	Entity string    `json:"entity,omitempty"`
	Table  *TableRef `json:"table,omitempty"`
	// Key is the entity's key: the read order, the resume cursor, and the
	// identity of the entity's rows.
	Key        []Field `json:"key,omitempty"`
	Properties []Field `json:"properties,omitempty"`

	// load-nodes
	Labels []string `json:"labels,omitempty"`

	// load-relationships
	Relationship     string         `json:"relationship,omitempty"`
	RelationshipType string         `json:"relationshipType,omitempty"`
	From             *NodeRef       `json:"from,omitempty"`
	To               *NodeRef       `json:"to,omitempty"`
	ForeignKey       *ForeignKeyRef `json:"foreignKey,omitempty"`
	SelfReference    bool           `json:"selfReference,omitempty"`

	// ensure-table
	SQL string `json:"sql,omitempty"`

	// load-rows (non-deferred foreign keys) and update-foreign-keys (one
	// deferred foreign key)
	ForeignKeys []ForeignKeyLoad `json:"foreignKeys,omitempty"`
}

// Transformation records a lossy conversion the plan will perform and what
// allowed it.
type Transformation struct {
	Entity       string         `json:"entity,omitempty"`
	Relationship string         `json:"relationship,omitempty"`
	Property     string         `json:"property"`
	Column       string         `json:"column"`
	Type         ir.LogicalType `json:"type"`
	Element      ir.LogicalType `json:"element,omitempty"`
	StoredAs     string         `json:"storedAs"`
	LossKind     string         `json:"lossKind"`
	// AllowedBy is "flexible-policy" or "allow-rule".
	AllowedBy string `json:"allowedBy"`
	Reason    string `json:"reason,omitempty"`
}

// Diagnostic is a warning attached to a plan.
type Diagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Canonical returns the plan's RFC 8785 canonical JSON.
func (p *Plan) Canonical() ([]byte, error) { return canonjson.Marshal(p) }

// StepByID returns the step with the given ID.
func (p *Plan) StepByID(id string) (*Step, bool) {
	for i := range p.Steps {
		if p.Steps[i].ID == id {
			return &p.Steps[i], true
		}
	}
	return nil, false
}
