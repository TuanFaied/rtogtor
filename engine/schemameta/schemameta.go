// Package schemameta defines the inspected schema metadata document
// (specs/schema-metadata.schema.json), produced by adapters' Inspect methods
// and by `rtogtor inspect`.
//
// These types mirror the JSON Schema by hand until the codegen pipeline
// (Milestone 1, step 6) generates them; the tests validate their JSON output
// against the schema to keep them aligned.
package schemameta

import "github.com/TuanFaied/rtogtor/engine/ir"

// Kind of database described.
type Kind string

// Kinds.
const (
	KindRelational Kind = "relational"
	KindGraph      Kind = "graph"
)

// Document is the root of the metadata document.
type Document struct {
	SpecVersion       string             `json:"specVersion"`
	Kind              Kind               `json:"kind"`
	Database          Database           `json:"database"`
	Schemas           []Schema           `json:"schemas,omitempty"`
	Labels            []Label            `json:"labels,omitempty"`
	RelationshipTypes []RelationshipType `json:"relationshipTypes,omitempty"`
	Warnings          []Warning          `json:"warnings,omitempty"`
}

// Database identifies the inspected server. It never holds credentials or
// host names.
type Database struct {
	Product string `json:"product"`
	Version string `json:"version,omitempty"`
	Name    string `json:"name,omitempty"`
}

// TypeInfo is type metadata (specs/values.md §4). A nil Type means the native
// type has no logical equivalent in this spec version.
type TypeInfo struct {
	Type             *ir.LogicalType `json:"type"`
	NativeType       string          `json:"nativeType,omitempty"`
	Precision        *int            `json:"precision,omitempty"`
	Scale            *int            `json:"scale,omitempty"`
	FractionalDigits *int            `json:"fractionalDigits,omitempty"`
	MaxLength        *int            `json:"maxLength,omitempty"`
	Element          *TypeInfo       `json:"element,omitempty"`
	EnumValues       []string        `json:"enumValues,omitempty"`
}

// Schema is a relational schema (namespace).
type Schema struct {
	Name   string  `json:"name"`
	Tables []Table `json:"tables"`
}

// Table is a relational table.
type Table struct {
	Name              string          `json:"name"`
	Columns           []Column        `json:"columns"`
	PrimaryKey        *KeyConstraint  `json:"primaryKey,omitempty"`
	ForeignKeys       []ForeignKey    `json:"foreignKeys"`
	UniqueConstraints []KeyConstraint `json:"uniqueConstraints"`
	EstimatedRows     *int64          `json:"estimatedRows,omitempty"`
}

// Column is a table column.
type Column struct {
	Name       string   `json:"name"`
	Ordinal    int      `json:"ordinal"`
	Nullable   bool     `json:"nullable"`
	HasDefault bool     `json:"hasDefault,omitempty"`
	Generated  bool     `json:"generated,omitempty"`
	TypeInfo   TypeInfo `json:"typeInfo"`
}

// KeyConstraint is a primary-key or unique constraint.
type KeyConstraint struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

// FKAction is a referential action.
type FKAction string

// Referential actions.
const (
	FKNoAction   FKAction = "no-action"
	FKRestrict   FKAction = "restrict"
	FKCascade    FKAction = "cascade"
	FKSetNull    FKAction = "set-null"
	FKSetDefault FKAction = "set-default"
)

// ForeignKey is a foreign-key constraint.
type ForeignKey struct {
	Name              string   `json:"name"`
	Columns           []string `json:"columns"`
	ReferencedSchema  string   `json:"referencedSchema"`
	ReferencedTable   string   `json:"referencedTable"`
	ReferencedColumns []string `json:"referencedColumns"`
	OnDelete          FKAction `json:"onDelete"`
	OnUpdate          FKAction `json:"onUpdate"`
}

// Label is a graph node label.
type Label struct {
	Name       string          `json:"name"`
	Properties []GraphProperty `json:"properties"`
}

// RelationshipType is a graph relationship type.
type RelationshipType struct {
	Name       string          `json:"name"`
	Properties []GraphProperty `json:"properties"`
}

// GraphProperty is a property observed on a label or relationship type.
type GraphProperty struct {
	Name      string     `json:"name"`
	Types     []TypeInfo `json:"types"`
	Mandatory bool       `json:"mandatory"`
}

// Warning reports an object that cannot be represented.
type Warning struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}
