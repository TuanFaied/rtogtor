package planning

import (
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph/neo4j"
	"github.com/TuanFaied/rtogtor/engine/internal/jsonschemas"
	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

var update = flag.Bool("update", false, "rewrite golden files")

func loadInputs(t *testing.T) (*mapping.Mapping, *schemameta.Document) {
	t.Helper()
	m, issues := mapping.LoadFile(testutil.RepoPath(t, "examples", "ecommerce", "mapping.yaml"))
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	b, err := os.ReadFile(testutil.RepoPath(t, "conformance", "cases", "ecommerce", "expected-schema-metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc schemameta.Document
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return m, &doc
}

func build(t *testing.T, m *mapping.Mapping, doc *schemameta.Document) (*Plan, rtgerr.List) {
	t.Helper()
	return Build(Input{Mapping: m, Source: doc, Destination: neo4j.TypeSupport{}})
}

func TestEcommercePlanMatchesGolden(t *testing.T) {
	m, doc := loadInputs(t)
	p, issues := build(t, m, doc)
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	got, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if v, err := jsonschemas.ValidateJSON(spec.PlanSchemaFile, got); err != nil {
		t.Fatal(err)
	} else {
		for _, x := range v {
			t.Errorf("plan violates schema at %s: %s", x.Path, x.Message)
		}
	}

	golden := testutil.RepoPath(t, "conformance", "cases", "ecommerce", "expected-plan.json")
	if *update {
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(got)+"\n" != string(want) {
		t.Errorf("plan differs from %s; rerun with -update if intended.\n%s", golden, p.Text())
	}

	// Deterministic.
	p2, _ := build(t, m, doc)
	again, _ := p2.Canonical()
	if string(again) != string(got) {
		t.Error("two plans from the same inputs differ")
	}
}

func TestEcommercePlanShape(t *testing.T) {
	m, doc := loadInputs(t)
	p, _ := build(t, m, doc)

	var kinds []string
	for _, s := range p.Steps {
		kinds = append(kinds, string(s.Kind))
	}
	// Constraints, then nodes, then relationships.
	want := "ensure-identity-constraint ×4, load-nodes ×4, load-relationships ×5"
	if got := summarize(kinds); got != want {
		t.Errorf("steps: %s, want %s", got, want)
	}

	placed, _ := p.StepByID("relationships:CustomerPlacedOrder")
	if placed.From.Entity != "Customer" || placed.To.Entity != "Order" || placed.Entity != "Order" {
		t.Errorf("referenced-to-owner direction: %+v %+v (read %s)", placed.From, placed.To, placed.Entity)
	}
	if strings.Join(placed.DependsOn, ",") != "nodes:Customer,nodes:Order" {
		t.Errorf("dependencies: %v", placed.DependsOn)
	}
	referred, _ := p.StepByID("relationships:CustomerReferredBy")
	if !referred.SelfReference || len(referred.DependsOn) != 1 {
		t.Errorf("self-reference: %+v", referred)
	}
	items, _ := p.StepByID("nodes:OrderItem")
	if len(items.Key) != 2 || items.Key[0].Column != "order_id" || items.Key[1].Column != "line_no" {
		t.Errorf("composite key: %+v", items.Key)
	}
	if len(p.Transformations) != 2 {
		t.Errorf("expected price and unitPrice decimal transformations, got %+v", p.Transformations)
	}
	for _, tr := range p.Transformations {
		if tr.LossKind != "representation" || tr.AllowedBy != "allow-rule" || tr.Reason == "" {
			t.Errorf("transformation: %+v", tr)
		}
	}
}

func summarize(kinds []string) string {
	var out []string
	for i := 0; i < len(kinds); {
		j := i
		for j < len(kinds) && kinds[j] == kinds[i] {
			j++
		}
		out = append(out, kinds[i]+" ×"+itoa(j-i))
		i = j
	}
	return strings.Join(out, ", ")
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func codes(list rtgerr.List) map[string]bool {
	out := map[string]bool{}
	for _, e := range list {
		out[string(e.Code)+"@"+e.Path] = true
	}
	return out
}

func TestPlanErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m *mapping.Mapping, doc *schemameta.Document)
		want   string
	}{
		{"missing table", func(m *mapping.Mapping, _ *schemameta.Document) {
			m.Entities[0].Table = "clients"
		}, "RTG-PLAN-001@/entities/0/table"},
		{"missing column", func(m *mapping.Mapping, _ *schemameta.Document) {
			m.Entities[0].Properties[2].Column = "fullname"
		}, "RTG-PLAN-002@/entities/0/properties/2/column"},
		{"declared type differs", func(m *mapping.Mapping, _ *schemameta.Document) {
			m.Entities[0].Properties[1].Type = ir.TypeUUID
		}, "RTG-PLAN-003@/entities/0/properties/1/column"},
		{"strict policy without allow rule", func(m *mapping.Mapping, _ *schemameta.Document) {
			m.Policies.Allow = nil
		}, "RTG-PLAN-005@/entities/1/properties/3"},
		{"allow rule scoped to another entity", func(m *mapping.Mapping, _ *schemameta.Document) {
			m.Policies.Allow[0].Entity = "Product"
		}, "RTG-PLAN-005@/entities/3/properties/3"},
		{"junction", func(m *mapping.Mapping, _ *schemameta.Document) {
			m.Relationships[0].ForeignKey = nil
			m.Relationships[0].Junction = &mapping.Junction{Table: "x"}
		}, "RTG-PLAN-006@/relationships/0/junction"},
		{"natural identity", func(m *mapping.Mapping, _ *schemameta.Document) {
			m.Identity.Strategy = mapping.StrategyNatural
		}, "RTG-PLAN-006@/identity/strategy"},
		{"foreign key type mismatch", func(_ *mapping.Mapping, doc *schemameta.Document) {
			col := column(doc, "orders", "customer_id")
			dec := ir.TypeDecimal
			col.TypeInfo.Type = &dec
		}, "RTG-PLAN-007@/relationships/0/foreignKey/columns/0"},
		{"unsupported column type", func(_ *mapping.Mapping, doc *schemameta.Document) {
			column(doc, "customers", "email").TypeInfo.Type = nil
		}, "RTG-PLAN-003@/entities/0/properties/1/column"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, doc := loadInputs(t)
			c.mutate(m, doc)
			p, issues := build(t, m, doc)
			if p != nil {
				t.Fatal("expected no plan")
			}
			if got := codes(issues); !got[c.want] {
				t.Errorf("issues %v do not include %s", issues, c.want)
			}
		})
	}
}

func column(doc *schemameta.Document, table, name string) *schemameta.Column {
	for i := range doc.Schemas[0].Tables {
		tb := &doc.Schemas[0].Tables[i]
		if tb.Name == table {
			for j := range tb.Columns {
				if tb.Columns[j].Name == name {
					return &tb.Columns[j]
				}
			}
		}
	}
	panic("column not found: " + table + "." + name)
}

func TestFlexiblePolicyRecordsLosses(t *testing.T) {
	m, doc := loadInputs(t)
	m.Policies.Loss = mapping.LossFlexible
	m.Policies.Allow = nil
	p, issues := build(t, m, doc)
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	if len(p.Transformations) != 2 || p.Transformations[0].AllowedBy != "flexible-policy" {
		t.Errorf("transformations: %+v", p.Transformations)
	}
}

func TestNullableKeyWarnsOnce(t *testing.T) {
	m, doc := loadInputs(t)
	column(doc, "customers", "id").Nullable = true
	p, issues := build(t, m, doc)
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	n := 0
	for _, w := range p.Warnings {
		if w.Code == string(rtgerr.PlanNullableKey) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d RTG-PLAN-004 warnings, want 1: %+v", n, p.Warnings)
	}
}
