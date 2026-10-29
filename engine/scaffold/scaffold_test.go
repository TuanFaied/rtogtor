package scaffold

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph/neo4j"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational/postgresql"
	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/planning"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
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

func TestNames(t *testing.T) {
	cases := map[string][3]string{ // input: pascal(singular), camel, upperSnake
		"order_items":       {"OrderItem", "orderItems", "ORDER_ITEMS"},
		"customers":         {"Customer", "customers", "CUSTOMERS"},
		"categories":        {"Category", "categories", "CATEGORIES"},
		"addresses":         {"Address", "addresses", "ADDRESSES"},
		"status":            {"Status", "status", "STATUS"},
		"analysis":          {"Analysis", "analysis", "ANALYSIS"},
		"parent_product_id": {"ParentProductId", "parentProductId", "PARENT_PRODUCT_ID"},
		"OrderLine":         {"OrderLine", "orderLine", "ORDER_LINE"},
	}
	for in, want := range cases {
		if got := pascal(singular(in)); got != want[0] {
			t.Errorf("pascal(singular(%q)) = %q, want %q", in, got, want[0])
		}
		if got := camel(in); got != want[1] {
			t.Errorf("camel(%q) = %q, want %q", in, got, want[1])
		}
		if got := upperSnake(in); got != want[2] {
			t.Errorf("upperSnake(%q) = %q, want %q", in, got, want[2])
		}
	}
	for in, want := range map[string]string{"yes": `"yes"`, "id": "id", "full name": `"full name"`, "1x": `"1x"`, "a-b": "a-b"} {
		if got := quote(in, false); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestFromRelationalEcommerce(t *testing.T) {
	doc := readMeta(t, "conformance", "cases", "ecommerce", "expected-schema-metadata.json")
	d, err := FromRelational(doc, Options{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	y, err := d.YAML()
	if err != nil {
		t.Fatal(err)
	}
	m, issues := mapping.Decode(y, mapping.FormatYAML)
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	// The draft plans without errors against the schema it came from.
	if _, issues := planning.Build(planning.Input{Mapping: m, Source: doc, Destination: neo4j.TypeSupport{}}); issues.HasErrors() {
		t.Fatalf("draft does not plan: %v\n%s", issues, y)
	}

	var names []string
	for _, e := range m.Entities {
		names = append(names, e.Name+"("+strings.Join(e.Key, ",")+")")
	}
	if got := strings.Join(names, " "); got != "Customer(id) OrderItem(order_id,line_no) Order(id) Product(id)" {
		t.Errorf("entities = %s", got)
	}
	var rels []string
	for _, r := range m.Relationships {
		rels = append(rels, r.Name+":"+r.Type+":"+r.ForeignKey.Entity+"->"+r.ForeignKey.References)
	}
	want := "CustomerReferredBy:REFERRED_BY:Customer->Customer OrderItemOrder:ORDER:OrderItem->Order " +
		"OrderItemProduct:PRODUCT:OrderItem->Product OrderCustomer:CUSTOMER:Order->Customer " +
		"ProductParentProduct:PARENT_PRODUCT:Product->Product"
	if got := strings.Join(rels, " "); got != want {
		t.Errorf("relationships = %s\nwant %s", got, want)
	}
	items, _ := m.Entity("OrderItem")
	if _, ok := items.PropertyByColumn("order_id"); !ok {
		t.Error("order_id is part of the key and must stay a property")
	}
	if _, ok := items.PropertyByColumn("product_id"); ok {
		t.Error("product_id becomes a relationship and must not also be a property")
	}
	if p, _ := items.PropertyByColumn("unit_price"); p.Property != "unitPrice" || p.Type != ir.TypeDecimal {
		t.Errorf("unit_price = %+v", p)
	}
	if len(m.Policies.Allow) != 1 || m.Policies.Allow[0].Type != ir.TypeDecimal {
		t.Errorf("allow = %+v", m.Policies.Allow)
	}
	if !strings.Contains(string(y), "# entity and label named after table") && !strings.Contains(string(y), "OrderItem") {
		t.Errorf("no notes:\n%s", y)
	}
}

func TestFromRelationalNotes(t *testing.T) {
	doc := readMeta(t, "conformance", "cases", "ecommerce", "expected-schema-metadata.json")
	s := &doc.Schemas[0]
	s.Tables = append(s.Tables, schemameta.Table{Name: "audit_log", Columns: s.Tables[0].Columns})
	d, err := FromRelational(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Notes) != 1 || !strings.Contains(d.Notes[0], "audit_log is not mapped") {
		t.Errorf("notes = %v", d.Notes)
	}
	if _, err := d.YAML(); err != nil {
		t.Fatal(err)
	}
}

func TestReverseOfExample(t *testing.T) {
	m, issues := mapping.LoadFile(testutil.RepoPath(t, "examples", "ecommerce", "mapping.yaml"))
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	d, err := Reverse(m)
	if err != nil {
		t.Fatal(err)
	}
	y, err := d.YAML()
	if err != nil {
		t.Fatal(err)
	}
	r, issues := mapping.Decode(y, mapping.FormatYAML)
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	src := readMeta(t, "conformance", "cases", "ecommerce-reverse", "source-metadata.json")
	dst := readMeta(t, "conformance", "cases", "ecommerce-reverse", "destination-metadata.json")
	if _, issues := planning.Build(planning.Input{Mapping: r, Source: src, Destination: neo4j.TypeSupport{}, DestinationMeta: dst,
		SourceTypes: neo4j.TypeSupport{}, DestinationTypes: postgresql.TypeSupport{}}); issues.HasErrors() {
		t.Fatalf("reverse draft does not plan: %v\n%s", issues, y)
	}
	// It matches the hand-written reverse example apart from naming and comments.
	want, _ := mapping.LoadFile(testutil.RepoPath(t, "examples", "ecommerce", "mapping-reverse.yaml"))
	want.Name, want.Description = r.Name, r.Description
	h1, _ := r.Hash()
	h2, _ := want.Hash()
	if h1 != h2 {
		a, _ := json.MarshalIndent(r, "", " ")
		b, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("reverse draft differs from examples/ecommerce/mapping-reverse.yaml:\n%s\n---\n%s", a, b)
	}
	if _, err := Reverse(r); err == nil {
		t.Error("a graph-to-relational mapping cannot be reversed")
	}
}
