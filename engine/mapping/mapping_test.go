package mapping

import (
	"strings"
	"testing"

	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

const header = `
specVersion: "0.1.0"
name: test
direction: relational-to-graph
source: { kind: postgresql }
destination: { kind: neo4j }
`

const customer = `
  - name: Customer
    table: customers
    labels: [Customer]
    key: [id]
    properties:
      - { column: id, property: id, type: int64 }
      - { column: referred_by, property: referredBy, type: int64 }
`

func codes(list rtgerr.List) []string {
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = string(e.Code) + "@" + e.Path
	}
	return out
}

// errorsOnly drops warnings such as RTG-MAP-010.
func errorsOnly(list rtgerr.List) rtgerr.List {
	var out rtgerr.List
	for _, e := range list {
		if e.Severity() != rtgerr.SeverityWarning {
			out = append(out, e)
		}
	}
	return out
}

func TestEcommerceExampleIsValid(t *testing.T) {
	m, issues := LoadFile(testutil.RepoPath(t, "examples", "ecommerce", "mapping.yaml"))
	if issues.HasErrors() {
		t.Fatalf("unexpected errors: %v", issues)
	}
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %v", codes(issues))
	}
	if m == nil {
		t.Fatal("mapping is nil")
	}
	if got := len(m.Entities); got != 4 {
		t.Errorf("entities = %d, want 4", got)
	}
	item, ok := m.Entity("OrderItem")
	if !ok || strings.Join(item.Key, ",") != "order_id,line_no" {
		t.Errorf("OrderItem composite key not loaded: %+v", item)
	}
	if m.Source.Schema != "public" || m.Identity.Property != "rtg_id" || m.Policies.Loss != LossStrict {
		t.Errorf("defaults not applied: %+v %+v %+v", m.Source, m.Identity, m.Policies)
	}
}

func TestDefaultsApplied(t *testing.T) {
	m, issues := Decode([]byte(header+"entities:"+customer), FormatYAML)
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	if m.Identity.Strategy != StrategyHash || m.Identity.Property != DefaultIdentityProperty {
		t.Errorf("identity defaults: %+v", m.Identity)
	}
}

func TestInvalidMappings(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want []string // code@path, errors only
	}{
		{
			name: "unsupported spec version",
			doc:  strings.Replace(header, `"0.1.0"`, `"0.2.0"`, 1) + "entities:" + customer,
			want: []string{"RTG-SPEC-001@/specVersion"},
		},
		{
			name: "missing labels",
			doc: header + `
entities:
  - name: Customer
    table: customers
    key: [id]
    properties: [{ column: id, property: id, type: int64 }]
`,
			want: []string{"RTG-MAP-001@/entities/0"},
		},
		{
			name: "unknown logical type",
			doc: header + `
entities:
  - name: Customer
    table: customers
    labels: [Customer]
    key: [id]
    properties: [{ column: id, property: id, type: integer }]
`,
			want: []string{"RTG-MAP-001@/entities/0/properties/0/type"},
		},
		{
			name: "graph endpoint as source for relational-to-graph",
			doc: strings.Replace(header, "source: { kind: postgresql }", "source: { kind: neo4j }", 1) +
				"entities:" + customer,
			want: []string{"RTG-MAP-001@/source/kind"},
		},
		{
			name: "duplicate entity",
			doc:  header + "entities:" + customer + customer,
			want: []string{"RTG-MAP-003@/entities/1/name"},
		},
		{
			name: "key column not mapped",
			doc: header + `
entities:
  - name: OrderItem
    table: order_items
    labels: [OrderItem]
    key: [order_id, line_no]
    properties: [{ column: order_id, property: orderId, type: int64 }]
`,
			want: []string{"RTG-MAP-005@/entities/0/key/1"},
		},
		{
			name: "float key",
			doc: header + `
entities:
  - name: Reading
    table: readings
    labels: [Reading]
    key: [value]
    properties: [{ column: value, property: value, type: float64 }]
`,
			want: []string{"RTG-MAP-006@/entities/0/key/0"},
		},
		{
			name: "duplicate property and column",
			doc: header + `
entities:
  - name: Customer
    table: customers
    labels: [Customer]
    key: [id]
    properties:
      - { column: id, property: id, type: int64 }
      - { column: email, property: id, type: string }
      - { column: email, property: email, type: string }
`,
			want: []string{
				"RTG-MAP-008@/entities/0/properties/1/property",
				"RTG-MAP-009@/entities/0/properties/2/column",
			},
		},
		{
			name: "reserved identity property",
			doc: header + `
entities:
  - name: Customer
    table: customers
    labels: [Customer]
    key: [id]
    properties:
      - { column: id, property: id, type: int64 }
      - { column: legacy_id, property: rtg_id, type: string }
`,
			want: []string{"RTG-MAP-012@/entities/0/properties/1/property"},
		},
		{
			name: "unknown referenced entity",
			doc: header + "entities:" + customer + `
relationships:
  - name: Placed
    type: PLACED
    foreignKey: { entity: Order, columns: [customer_id], references: Customer, direction: referenced-to-owner }
`,
			want: []string{"RTG-MAP-004@/relationships/0/foreignKey/entity"},
		},
		{
			name: "foreign key arity",
			doc: header + "entities:" + customer + `
relationships:
  - name: ReferredBy
    type: REFERRED_BY
    foreignKey: { entity: Customer, columns: [referred_by, extra], references: Customer, direction: owner-to-referenced }
`,
			want: []string{"RTG-MAP-007@/relationships/0/foreignKey/columns"},
		},
		{
			name: "duplicate relationship",
			doc: header + "entities:" + customer + `
relationships:
  - name: ReferredBy
    type: REFERRED_BY
    foreignKey: { entity: Customer, columns: [referred_by], references: Customer, direction: owner-to-referenced }
  - name: ReferredBy
    type: REFERRED
    foreignKey: { entity: Customer, columns: [referred_by], references: Customer, direction: owner-to-referenced }
`,
			want: []string{"RTG-MAP-011@/relationships/1/name"},
		},
		{
			name: "relationship with both foreignKey and junction",
			doc: header + "entities:" + customer + `
relationships:
  - name: Both
    type: BOTH
    foreignKey: { entity: Customer, columns: [referred_by], references: Customer, direction: owner-to-referenced }
    junction:
      table: links
      from: { entity: Customer, columns: [a] }
      to: { entity: Customer, columns: [b] }
`,
			want: []string{"RTG-MAP-001@/relationships/0"},
		},
		{
			name: "junction arity",
			doc: header + "entities:" + customer + `
relationships:
  - name: Friends
    type: FRIEND_OF
    junction:
      table: friendships
      from: { entity: Customer, columns: [a, b] }
      to: { entity: Customer, columns: [c] }
`,
			want: []string{"RTG-MAP-007@/relationships/0/junction/from/columns"},
		},
		{
			name: "array without element type",
			doc: header + `
entities:
  - name: Customer
    table: customers
    labels: [Customer]
    key: [id]
    properties:
      - { column: id, property: id, type: int64 }
      - { column: tags, property: tags, type: array }
`,
			want: []string{"RTG-MAP-001@/entities/0/properties/1"},
		},
		{
			name: "YAML duplicate key",
			doc:  header + "name: again\nentities:" + customer,
			want: []string{"RTG-MAP-002@"},
		},
		{
			name: "multiple YAML documents",
			doc:  header + "entities:" + customer + "---\n" + header,
			want: []string{"RTG-MAP-002@"},
		},
		{
			name: "unquoted spec version is a number",
			doc:  strings.Replace(header, `"0.1.0"`, `0.1`, 1) + "entities:" + customer,
			want: []string{"RTG-MAP-001@/specVersion"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, issues := Decode([]byte(tc.doc), FormatYAML)
			if m != nil {
				t.Fatalf("expected the mapping to be rejected")
			}
			got := codes(errorsOnly(issues))
			if !containsAll(got, tc.want) {
				t.Errorf("issues = %v, want to include %v", got, tc.want)
			}
		})
	}
}

func containsAll(got, want []string) bool {
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func TestJSONDuplicateKeysRejected(t *testing.T) {
	doc := `{"specVersion":"0.1.0","name":"a","name":"b"}`
	_, issues := Decode([]byte(doc), FormatJSON)
	if len(issues) != 1 || issues[0].Code != rtgerr.MapParse || !strings.Contains(issues[0].Message, `duplicate key "name"`) {
		t.Errorf("got %v", issues)
	}
}

func TestJSONTrailingDataRejected(t *testing.T) {
	_, issues := Decode([]byte(`{} {}`), FormatJSON)
	if len(issues) != 1 || issues[0].Code != rtgerr.MapParse {
		t.Errorf("got %v", issues)
	}
}

func TestJSONAndYAMLAgree(t *testing.T) {
	yamlDoc := header + "entities:" + customer
	jsonDoc := `{
	  "specVersion": "0.1.0", "name": "test", "direction": "relational-to-graph",
	  "source": {"kind": "postgresql"}, "destination": {"kind": "neo4j"},
	  "entities": [{
	    "name": "Customer", "table": "customers", "labels": ["Customer"], "key": ["id"],
	    "properties": [
	      {"column": "id", "property": "id", "type": "int64"},
	      {"column": "referred_by", "property": "referredBy", "type": "int64"}
	    ]
	  }]
	}`
	a, ia := Decode([]byte(yamlDoc), FormatYAML)
	b, ib := Decode([]byte(jsonDoc), FormatJSON)
	if ia.HasErrors() || ib.HasErrors() {
		t.Fatal(ia, ib)
	}
	if a.Entities[0].Properties[1] != b.Entities[0].Properties[1] || a.Source != b.Source {
		t.Errorf("YAML and JSON decode differently: %+v vs %+v", a, b)
	}
}

func TestFormatFromPath(t *testing.T) {
	for path, want := range map[string]Format{"m.json": FormatJSON, "m.YAML": FormatYAML, "dir/m.yml": FormatYAML} {
		if got, ok := FormatFromPath(path); !ok || got != want {
			t.Errorf("FormatFromPath(%q) = %q, %v", path, got, ok)
		}
	}
	if _, ok := FormatFromPath("mapping.txt"); ok {
		t.Error("unexpected format for .txt")
	}
	if _, issues := LoadFile("mapping.txt"); len(issues) != 1 || issues[0].Code != rtgerr.MapParse {
		t.Errorf("got %v", issues)
	}
}

func TestHashIsFormatIndependent(t *testing.T) {
	yamlDoc := header + "entities:" + customer
	jsonDoc := `{"specVersion":"0.1.0","name":"test","direction":"relational-to-graph",
	  "source":{"kind":"postgresql","schema":"public"},"destination":{"kind":"neo4j"},
	  "identity":{"strategy":"hash","property":"rtg_id"},
	  "entities":[{"name":"Customer","table":"customers","labels":["Customer"],"key":["id"],
	    "properties":[{"column":"id","property":"id","type":"int64"},
	                  {"column":"referred_by","property":"referredBy","type":"int64"}]}]}`
	a, ia := Decode([]byte(yamlDoc), FormatYAML)
	b, ib := Decode([]byte(jsonDoc), FormatJSON)
	if ia.HasErrors() || ib.HasErrors() {
		t.Fatal(ia, ib)
	}
	ha, _ := a.Hash()
	hb, _ := b.Hash()
	if ha != hb || len(ha) != 64 {
		t.Errorf("hashes differ: %s vs %s", ha, hb)
	}
	b.Entities[0].Labels = []string{"Client"}
	if hc, _ := b.Hash(); hc == ha {
		t.Error("changing the mapping must change the hash")
	}
}

func TestGraphToRelationalStillWarns(t *testing.T) {
	doc := `
specVersion: "0.1.0"
name: reverse
direction: graph-to-relational
source: { kind: neo4j }
destination: { kind: postgresql }
entities:` + customer
	_, issues := Decode([]byte(doc), FormatYAML)
	if issues.HasErrors() || len(issues) != 1 || issues[0].Code != rtgerr.MapDirectionPending {
		t.Errorf("got %v", codes(issues))
	}
}
