package planning

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph/neo4j"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational/postgresql"
	"github.com/TuanFaied/rtogtor/engine/internal/jsonschemas"
	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

func readMeta(t *testing.T, parts ...string) *schemameta.Document {
	t.Helper()
	b, err := os.ReadFile(testutil.RepoPath(t, parts...))
	if err != nil {
		t.Fatal(err)
	}
	var doc schemameta.Document
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return &doc
}

// reverseInputs loads the reverse e-commerce mapping, the graph the forward
// mapping produces, and an empty destination schema.
func reverseInputs(t *testing.T) (*mapping.Mapping, *schemameta.Document, *schemameta.Document) {
	t.Helper()
	m, issues := mapping.LoadFile(testutil.RepoPath(t, "examples", "ecommerce", "mapping-reverse.yaml"))
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	return m,
		readMeta(t, "conformance", "cases", "ecommerce-reverse", "source-metadata.json"),
		readMeta(t, "conformance", "cases", "ecommerce-reverse", "destination-metadata.json")
}

func buildReverse(m *mapping.Mapping, src, dst *schemameta.Document) (*Plan, rtgerr.List) {
	return Build(Input{Mapping: m, Source: src, Destination: neo4j.TypeSupport{}, DestinationMeta: dst,
		SourceTypes: neo4j.TypeSupport{}, DestinationTypes: postgresql.TypeSupport{}})
}

func stepIDs(p *Plan) []string {
	ids := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		ids[i] = s.ID
	}
	return ids
}

func TestReversePlanMatchesGolden(t *testing.T) {
	m, src, dst := reverseInputs(t)
	p, issues := buildReverse(m, src, dst)
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
	golden := testutil.RepoPath(t, "conformance", "cases", "ecommerce-reverse", "expected-plan.json")
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
	p2, _ := buildReverse(m, src, dst)
	if again, _ := p2.Canonical(); string(again) != string(got) {
		t.Error("two plans from the same inputs differ")
	}
}

func TestReversePlanCreatesTablesInOrder(t *testing.T) {
	m, src, dst := reverseInputs(t)
	p, issues := buildReverse(m, src, dst)
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	want := []string{
		"table:public.customers", "table:public.products", "table:public.orders", "table:public.order_items",
		"rows:Customer", "rows:Product", "rows:Order", "rows:OrderItem",
		"foreign-keys:CustomerReferredBy", "foreign-keys:ProductVariantOf",
	}
	if got := stepIDs(p); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	items, _ := p.StepByID("table:public.order_items")
	for _, frag := range []string{
		`"order_id" bigint NOT NULL`, `"product_id" bigint`, `"unit_price" numeric`,
		`PRIMARY KEY ("order_id", "line_no")`,
		`FOREIGN KEY ("order_id") REFERENCES "public"."orders" ("id")`,
		`FOREIGN KEY ("product_id") REFERENCES "public"."products" ("id")`,
	} {
		if !strings.Contains(items.SQL, frag) {
			t.Errorf("order_items DDL lacks %q:\n%s", frag, items.SQL)
		}
	}
	customers, _ := p.StepByID("table:public.customers")
	// Only key columns are NOT NULL: graph inspection samples whether a
	// property is on every node, which does not prove it always will be.
	if !strings.Contains(customers.SQL, `"referred_by" bigint,`) || !strings.Contains(customers.SQL, `"created_at" timestamp(6) with time zone,`) ||
		!strings.Contains(customers.SQL, `"id" bigint NOT NULL`) {
		t.Errorf("customers DDL:\n%s", customers.SQL)
	}
	rows, _ := p.StepByID("rows:OrderItem")
	if len(rows.ForeignKeys) != 2 || rows.ForeignKeys[0].Deferred || rows.ForeignKeys[0].Direction != "incoming" {
		t.Errorf("rows:OrderItem foreign keys = %+v", rows.ForeignKeys)
	}
	if strings.Join(rows.DependsOn, " ") != "table:public.order_items rows:Order rows:Product" {
		t.Errorf("rows:OrderItem depends on %v", rows.DependsOn)
	}
	fk, _ := p.StepByID("foreign-keys:CustomerReferredBy")
	if len(fk.ForeignKeys) != 1 || !fk.ForeignKeys[0].Deferred || fk.ForeignKeys[0].Direction != "outgoing" {
		t.Errorf("deferred step = %+v", fk)
	}
	if len(p.Transformations) != 0 {
		t.Errorf("transformations = %+v; created columns keep every value", p.Transformations)
	}
}

// existingTables is the forward example's schema, as the reverse destination.
func existingTables(t *testing.T) *schemameta.Document {
	return readMeta(t, "conformance", "cases", "ecommerce", "expected-schema-metadata.json")
}

func TestReversePlanAgainstExistingTables(t *testing.T) {
	m, src, _ := reverseInputs(t)
	m.Destination.CreateTables = false
	p, issues := buildReverse(m, src, existingTables(t))
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	if got := stepIDs(p); strings.HasPrefix(got[0], "table:") || len(got) != 6 {
		t.Fatalf("steps = %v", got)
	}
	products, _ := p.StepByID("rows:Product")
	for _, f := range products.Properties {
		if f.Column == "price" && (f.Scale == nil || *f.Scale != 2 || f.NativeType != "numeric(12,2)") {
			t.Errorf("price field = %+v", f)
		}
	}
	items, _ := p.StepByID("rows:OrderItem")
	for _, f := range items.Properties {
		if f.Column == "line_no" && f.NativeType != "integer" {
			t.Errorf("line_no field = %+v", f)
		}
	}
	// Strict policy without allow rules: precision is checked per value
	// at execution, so the plan lists no transformation.
	if len(p.Transformations) != 0 {
		t.Errorf("transformations = %+v", p.Transformations)
	}

	m.Policies.Loss = mapping.LossFlexible
	p, issues = buildReverse(m, src, existingTables(t))
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	kinds := map[string]bool{}
	for _, tr := range p.Transformations {
		if tr.LossKind != "precision" || tr.AllowedBy != "flexible-policy" {
			t.Errorf("transformation %+v", tr)
		}
		kinds[tr.Entity+"."+tr.Property] = true
	}
	for _, want := range []string{"Product.price", "Customer.createdAt", "OrderItem.unitPrice"} {
		if !kinds[want] {
			t.Errorf("no precision transformation for %s: %v", want, kinds)
		}
	}
}

func codesOf(l rtgerr.List) []string {
	out := make([]string, len(l))
	for i, e := range l {
		out[i] = string(e.Code) + "@" + e.Path
	}
	return out
}

func hasCode(l rtgerr.List, code rtgerr.Code, path string) bool {
	for _, e := range l {
		if e.Code == code && (path == "" || e.Path == path) {
			return true
		}
	}
	return false
}

func TestReversePlanRejections(t *testing.T) {
	t.Run("missing table", func(t *testing.T) {
		m, src, dst := reverseInputs(t)
		m.Destination.CreateTables = false
		_, issues := buildReverse(m, src, dst)
		if !hasCode(issues, rtgerr.PlanTableMissing, "/entities/0/table") {
			t.Errorf("got %v", codesOf(issues))
		}
	})
	t.Run("no unique key", func(t *testing.T) {
		m, src, _ := reverseInputs(t)
		dst := existingTables(t)
		dst.Schemas[0].Tables[0].PrimaryKey = nil // customers
		dst.Schemas[0].Tables[0].UniqueConstraints = nil
		_, issues := buildReverse(m, src, dst)
		if !hasCode(issues, rtgerr.PlanNoUniqueKey, "/entities/0/key") {
			t.Errorf("got %v", codesOf(issues))
		}
	})
	t.Run("deferred not null", func(t *testing.T) {
		m, src, _ := reverseInputs(t)
		dst := existingTables(t)
		for i, c := range dst.Schemas[0].Tables[0].Columns {
			if c.Name == "referred_by" {
				dst.Schemas[0].Tables[0].Columns[i].Nullable = false
			}
		}
		_, issues := buildReverse(m, src, dst)
		if !hasCode(issues, rtgerr.PlanDeferredNotNull, "/relationships/3/foreignKey/columns/0") {
			t.Errorf("got %v", codesOf(issues))
		}
	})
	t.Run("deferred column mapped as property", func(t *testing.T) {
		m, src, dst := reverseInputs(t)
		e := &m.Entities[0]
		e.Properties = append(e.Properties, mapping.Property{Column: "referred_by", Property: "referredBy", Type: "int64"})
		_, issues := buildReverse(m, src, dst)
		if !hasCode(issues, rtgerr.PlanUnsupported, "/relationships/3/foreignKey/columns/0") {
			t.Errorf("got %v", codesOf(issues))
		}
	})
	t.Run("type mismatch", func(t *testing.T) {
		m, src, dst := reverseInputs(t)
		m.Entities[0].Properties[1].Type = "int64" // email holds strings
		_, issues := buildReverse(m, src, dst)
		if !hasCode(issues, rtgerr.PlanTypeMismatch, "/entities/0/properties/1") {
			t.Errorf("got %v", codesOf(issues))
		}
	})
	t.Run("column type mismatch", func(t *testing.T) {
		m, src, _ := reverseInputs(t)
		m.Entities[0].Properties[2].Column = "created_at" // fullName into a timestamptz column
		m.Entities[0].Properties = m.Entities[0].Properties[:3]
		_, issues := buildReverse(m, src, existingTables(t))
		if !hasCode(issues, rtgerr.PlanTypeMismatch, "/entities/0/properties/2/column") {
			t.Errorf("got %v", codesOf(issues))
		}
	})
	t.Run("unknown label and property warn", func(t *testing.T) {
		m, src, dst := reverseInputs(t)
		m.Entities[0].Labels = []string{"Customer", "Vip"}
		m.Entities[0].Properties = append(m.Entities[0].Properties, mapping.Property{Column: "nickname", Property: "nickname", Type: "string"})
		p, issues := buildReverse(m, src, dst)
		if issues.HasErrors() {
			t.Fatal(issues)
		}
		if !hasCode(issues, rtgerr.PlanSourceShape, "/entities/0/labels") || !hasCode(issues, rtgerr.PlanSourceShape, "/entities/0/properties/4") {
			t.Errorf("got %v", codesOf(issues))
		}
		// Two shape warnings, plus one missing-index warning per entity.
		if len(p.Warnings) != 6 {
			t.Errorf("warnings = %+v", p.Warnings)
		}
	})
	t.Run("bytes key", func(t *testing.T) {
		m, src, dst := reverseInputs(t)
		m.Entities[0].Properties[0].Type = "bytes"
		_, issues := buildReverse(m, src, dst)
		if !issues.HasErrors() {
			t.Error("a bytes key must be rejected")
		}
	})
}

func TestReversePlanWarnsWithoutKeyIndex(t *testing.T) {
	m, src, dst := reverseInputs(t)
	p, issues := buildReverse(m, src, dst)
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	// The forward migration only indexes rtg_id.
	for i := range m.Entities {
		if !hasCode(issues, rtgerr.PlanSourceShape, fmt.Sprintf("/entities/%d/key", i)) {
			t.Errorf("entity %d: no missing-index warning in %v", i, codesOf(issues))
		}
	}
	if !strings.Contains(p.Warnings[3].Message, "CREATE INDEX FOR (n:`OrderItem`) ON (n.`orderId`, n.`lineNo`)") {
		t.Errorf("warning = %s", p.Warnings[3].Message)
	}

	// An index whose leading properties are the key, in key order, removes it.
	for i := range src.Labels {
		if src.Labels[i].Name == "OrderItem" {
			idx := append(*src.Labels[i].Indexes, schemameta.LabelIndex{Properties: []string{"orderId", "lineNo", "quantity"}})
			src.Labels[i].Indexes = &idx
		}
		if src.Labels[i].Name == "Customer" {
			idx := []schemameta.LabelIndex{{Properties: []string{"email", "id"}}} // wrong order
			src.Labels[i].Indexes = &idx
		}
	}
	_, issues = buildReverse(m, src, dst)
	if hasCode(issues, rtgerr.PlanSourceShape, "/entities/3/key") {
		t.Errorf("OrderItem has an index on its key: %v", codesOf(issues))
	}
	if !hasCode(issues, rtgerr.PlanSourceShape, "/entities/0/key") {
		t.Errorf("an index on (email, id) does not serve key (id): %v", codesOf(issues))
	}

	// Unknown indexes (older metadata): no warning.
	for i := range src.Labels {
		src.Labels[i].Indexes = nil
	}
	if _, issues = buildReverse(m, src, dst); hasCode(issues, rtgerr.PlanSourceShape, "/entities/0/key") {
		t.Errorf("indexes unknown, but warned: %v", codesOf(issues))
	}
}
