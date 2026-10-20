package schemameta

import (
	"encoding/json"
	"testing"

	"github.com/TuanFaied/rtogtor/engine/internal/jsonschemas"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

func ptr[T any](v T) *T { return &v }

// The hand-written Go types must produce documents that satisfy the schema.
func TestRelationalDocumentMatchesSchema(t *testing.T) {
	doc := Document{
		SpecVersion: spec.Version,
		Kind:        KindRelational,
		Database:    Database{Product: "postgresql", Version: "17.2", Name: "shop"},
		Schemas: []Schema{{
			Name: "public",
			Tables: []Table{{
				Name: "order_items",
				Columns: []Column{
					{Name: "order_id", Ordinal: 1, TypeInfo: TypeInfo{Type: ptr(ir.TypeInt64), NativeType: "bigint"}},
					{Name: "line_no", Ordinal: 2, TypeInfo: TypeInfo{Type: ptr(ir.TypeInt64), NativeType: "integer"}},
					{Name: "unit_price", Ordinal: 3, TypeInfo: TypeInfo{Type: ptr(ir.TypeDecimal), NativeType: "numeric(12,2)", Precision: ptr(12), Scale: ptr(2)}},
					{Name: "location", Ordinal: 4, Nullable: true, TypeInfo: TypeInfo{Type: nil, NativeType: "point"}},
				},
				PrimaryKey: &KeyConstraint{Name: "order_items_pkey", Columns: []string{"order_id", "line_no"}},
				ForeignKeys: []ForeignKey{{
					Name: "order_items_order_id_fkey", Columns: []string{"order_id"},
					ReferencedSchema: "public", ReferencedTable: "orders", ReferencedColumns: []string{"id"},
					OnDelete: FKNoAction, OnUpdate: FKNoAction,
				}},
				UniqueConstraints: []KeyConstraint{},
			}},
		}},
		Warnings: []Warning{{Code: "RTG-VAL-002", Path: "public.order_items.location", Message: "type point is not supported"}},
	}
	assertValid(t, doc)
}

func TestGraphDocumentMatchesSchema(t *testing.T) {
	doc := Document{
		SpecVersion: spec.Version,
		Kind:        KindGraph,
		Database:    Database{Product: "neo4j", Version: "5.26.0"},
		Labels: []Label{{Name: "Customer", Properties: []GraphProperty{
			{Name: "id", Types: []TypeInfo{{Type: ptr(ir.TypeInt64)}}, Mandatory: true},
		}}},
		RelationshipTypes: []RelationshipType{{Name: "PLACED", Properties: []GraphProperty{}}},
	}
	assertValid(t, doc)
}

func TestKindMismatchIsRejected(t *testing.T) {
	doc := Document{SpecVersion: spec.Version, Kind: KindGraph, Database: Database{Product: "neo4j"}, Schemas: []Schema{}}
	b, _ := json.Marshal(doc)
	// Schemas is empty and omitted, and labels are missing: still invalid.
	v, err := jsonschemas.ValidateJSON(spec.SchemaMetadataSchemaFile, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) == 0 {
		t.Error("a graph document without labels must be rejected")
	}
}

func assertValid(t *testing.T, doc Document) {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonschemas.ValidateJSON(spec.SchemaMetadataSchemaFile, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range v {
		t.Errorf("%s: %s", x.Path, x.Message)
	}
}
