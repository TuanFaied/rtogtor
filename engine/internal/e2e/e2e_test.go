//go:build integration

// End-to-end tests: PostgreSQL → Neo4j with real, disposable databases.
//
//	RTOGTOR_TEST_POSTGRES_URL=postgres://…  RTOGTOR_TEST_NEO4J_URL=neo4j://neo4j:…@127.0.0.1:7687 \
//	  go test -tags integration ./internal/e2e/
//
// Each test loads its own PostgreSQL schema and uses labels with a unique
// suffix, and removes both afterwards.
package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph/neo4j"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational/postgresql"
	"github.com/TuanFaied/rtogtor/engine/execution"
	"github.com/TuanFaied/rtogtor/engine/internal/canonjson"
	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/planning"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/runstate"
	"github.com/TuanFaied/rtogtor/engine/verification"
)

func env(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("%s must point to a disposable database for integration tests", name)
	}
	return v
}

func suffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// world is one test's isolated source schema and destination labels.
type world struct {
	t      *testing.T
	ctx    context.Context
	schema string
	sfx    string
	labels []string
	src    *postgresql.Adapter
	dst    *neo4j.Adapter
	drv    driver.Driver
}

func newWorld(t *testing.T, ddl string) *world {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)
	w := &world{t: t, ctx: ctx, sfx: suffix()}
	w.schema = "rtg_e2e_" + w.sfx

	pgURL := env(t, "RTOGTOR_TEST_POSTGRES_URL")
	conn, err := pgx.Connect(ctx, pgURL)
	if err != nil {
		t.Fatal("cannot connect to the PostgreSQL test database")
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+w.schema+" CASCADE")
		_ = conn.Close(context.Background())
	})
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+w.schema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.PgConn().Exec(ctx, "SET search_path TO "+w.schema+";\n"+ddl).ReadAll(); err != nil {
		t.Fatalf("loading fixture: %v", err)
	}

	if w.src, err = postgresql.Open(ctx, pgURL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.src.Close() })

	neoURL := env(t, "RTOGTOR_TEST_NEO4J_URL")
	if w.dst, err = neo4j.Open(ctx, neoURL, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.dst.Close() })
	w.drv = rawDriver(t, neoURL)
	t.Cleanup(w.cleanupGraph)
	return w
}

func rawDriver(t *testing.T, raw string) driver.Driver {
	t.Helper()
	u := strings.SplitN(strings.TrimPrefix(raw, "neo4j://"), "@", 2)
	user, pw, _ := strings.Cut(u[0], ":")
	d, err := driver.NewDriver("neo4j://"+u[1], driver.BasicAuth(user, pw, ""))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	return d
}

func (w *world) cypher(q string, params map[string]any) []*driver.Record {
	w.t.Helper()
	res, err := driver.ExecuteQuery(w.ctx, w.drv, q, params, driver.EagerResultTransformer)
	if err != nil {
		w.t.Fatalf("cypher %q: %v", q, err)
	}
	return res.Records
}

// cleanupGraph deletes the test's nodes and constraints. CALL … IN
// TRANSACTIONS needs an auto-commit transaction, so it runs through
// session.Run rather than a managed transaction. Failures fail the test:
// silently leaving data behind would hide problems.
func (w *world) cleanupGraph() {
	ctx := context.Background()
	s := w.drv.NewSession(ctx, driver.SessionConfig{})
	defer func() { _ = s.Close(ctx) }()
	for _, l := range w.labels {
		for _, q := range []string{
			"MATCH (n:`" + l + "`) CALL (n) { DETACH DELETE n } IN TRANSACTIONS OF 5000 ROWS",
			"DROP CONSTRAINT `rtg_" + l + "_rtg_id` IF EXISTS",
		} {
			res, err := s.Run(ctx, q, nil)
			if err == nil {
				_, err = res.Consume(ctx)
			}
			if err != nil {
				w.t.Errorf("cleanup %q: %v", q, err)
			}
		}
	}
}

// load reads a mapping, points it at the test schema and suffixes labels.
func (w *world) load(path string) *mapping.Mapping {
	w.t.Helper()
	m, issues := mapping.LoadFile(path)
	if issues.HasErrors() {
		w.t.Fatal(issues)
	}
	return w.adapt(m)
}

func (w *world) adapt(m *mapping.Mapping) *mapping.Mapping {
	m.Source.Schema = w.schema
	for i := range m.Entities {
		for j, l := range m.Entities[i].Labels {
			m.Entities[i].Labels[j] = l + "_" + w.sfx
			w.labels = append(w.labels, m.Entities[i].Labels[j])
		}
	}
	return m
}

func (w *world) plan(m *mapping.Mapping) *planning.Plan {
	w.t.Helper()
	doc, err := w.src.Inspect(w.ctx, relational.InspectOptions{Schemas: []string{w.schema}})
	if err != nil {
		w.t.Fatal(err)
	}
	p, issues := planning.Build(planning.Input{Mapping: m, Source: doc, Destination: neo4j.TypeSupport{}})
	if issues.HasErrors() {
		w.t.Fatal(issues)
	}
	return p
}

func (w *world) options(m *mapping.Mapping, p *planning.Plan, store *runstate.Store, batch int) execution.Options {
	mj, _ := canonjson.Marshal(m)
	return execution.Options{
		Plan: p, MappingJSON: mj, Source: w.src, Destination: w.dst, Store: store, BatchSize: batch,
		SourceInfo: runstate.Endpoint{Product: "postgresql"}, DestinationInfo: runstate.Endpoint{Product: "neo4j"},
	}
}

// counts returns node and relationship counts for the test's labels.
func (w *world) counts() string {
	var parts []string
	for _, l := range w.labels {
		n := w.cypher("MATCH (n:`"+l+"`) RETURN count(n) AS n", nil)[0].Values[0]
		r := w.cypher("MATCH (:`"+l+"`)-[r]->() RETURN count(r) AS n", nil)[0].Values[0]
		parts = append(parts, fmt.Sprintf("%s=%v/%v", strings.TrimSuffix(l, "_"+w.sfx), n, r))
	}
	return strings.Join(parts, " ")
}

func ecommerce(t *testing.T) string {
	b, err := os.ReadFile(testutil.RepoPath(t, "examples", "ecommerce", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Outgoing relationships: Customer PLACED 2 + REFERRED_BY 2, Product
// VARIANT_OF 2, Order CONTAINS 3, OrderItem OF_PRODUCT 3.
const wantEcommerce = "Customer=3/4 Product=4/2 Order=2/3 OrderItem=3/3"

func TestEcommerceEndToEnd(t *testing.T) {
	w := newWorld(t, ecommerce(t))
	m := w.load(testutil.RepoPath(t, "examples", "ecommerce", "mapping.yaml"))
	p := w.plan(m)
	store := &runstate.Store{Dir: t.TempDir()}

	// Dry run: nothing changes in the destination.
	dry := w.options(m, p, store, 2)
	dry.DryRun = true
	st, err := execution.Run(w.ctx, dry)
	if err != nil || st.Status != runstate.StatusSucceeded {
		t.Fatalf("dry run: %v %s", err, st.Status)
	}
	if got := w.counts(); got != "Customer=0/0 Product=0/0 Order=0/0 OrderItem=0/0" {
		t.Fatalf("dry run changed the destination: %s", got)
	}
	if n := w.cypher("SHOW CONSTRAINTS YIELD name WHERE name STARTS WITH 'rtg_' AND name ENDS WITH $s RETURN count(*) AS n",
		map[string]any{"s": w.sfx + "_rtg_id"})[0].Values[0]; n != int64(0) {
		t.Errorf("dry run created %v constraints", n)
	}

	// Real run, small batches to exercise keyset paging.
	st, err = execution.Run(w.ctx, w.options(m, p, store, 2))
	if err != nil || st.Status != runstate.StatusSucceeded {
		t.Fatalf("migrate: %v %s", err, st.Status)
	}
	if got := w.counts(); got != wantEcommerce {
		t.Fatalf("graph: %s, want %s", got, wantEcommerce)
	}

	// Composite key, self-references and values.
	rec := w.cypher(fmt.Sprintf(
		"MATCH (c:Customer_%[1]s {id: 3})-[:REFERRED_BY]->(r:Customer_%[1]s)-[:REFERRED_BY]->(root:Customer_%[1]s) "+
			"MATCH (i:OrderItem_%[1]s {orderId: 1001, lineNo: 2})-[:OF_PRODUCT]->(p:Product_%[1]s) "+
			"RETURN r.fullName, root.fullName, p.sku, i.unitPrice, toString(c.createdAt)", w.sfx), nil)
	if len(rec) != 1 {
		t.Fatalf("expected one match, got %d", len(rec))
	}
	parts := make([]string, len(rec[0].Values))
	for i, v := range rec[0].Values {
		parts[i] = fmt.Sprint(v)
	}
	got := strings.Join(parts, " | ")
	if got != "Alan Turing | Ada Lovelace | MUG | 9 | 2026-03-20T08:15:00Z[UTC]" {
		t.Errorf("values: %s", got)
	}

	// Verification passes.
	v, err := verification.Verify(w.ctx, p, w.src, w.dst, 2)
	if err != nil || v.Status != "passed" {
		t.Fatalf("verify: %v %+v", err, v)
	}

	// Re-running changes nothing (idempotent).
	if _, err := execution.Run(w.ctx, w.options(m, p, store, 3)); err != nil {
		t.Fatal(err)
	}
	if got := w.counts(); got != wantEcommerce {
		t.Errorf("second run changed the graph: %s", got)
	}

	// Verification detects a missing node and its relationships.
	w.cypher("MATCH (o:Order_"+w.sfx+" {id: 1002}) DETACH DELETE o", nil)
	v, err = verification.Verify(w.ctx, p, w.src, w.dst, 2)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, i := range v.Issues {
		codes = append(codes, i.Code)
	}
	if v.Status != "failed" || !strings.Contains(strings.Join(codes, ","), "RTG-VERIFY-001") || !strings.Contains(strings.Join(codes, ","), "RTG-VERIFY-003") {
		t.Errorf("verification after deleting an order: %s %v", v.Status, codes)
	}
	for _, e := range v.Entities {
		if e.Entity == "Order" && (e.Missing != 1 || len(e.MissingKeys) != 1 || e.MissingKeys[0][0] != "1002") {
			t.Errorf("missing order not identified: %+v", e)
		}
	}
}

func TestResumeAfterInjectedFailure(t *testing.T) {
	w := newWorld(t, ecommerce(t))
	m := w.load(testutil.RepoPath(t, "examples", "ecommerce", "mapping.yaml"))
	p := w.plan(m)
	store := &runstate.Store{Dir: t.TempDir()}

	opts := w.options(m, p, store, 1)
	opts.Fault = func(step string, batch int64, point execution.FaultPoint) error {
		if step == "relationships:OrderContainsItem" && batch == 1 && point == execution.BeforeCheckpoint {
			return rtgerr.New(rtgerr.ExecInterrupted, "injected failure after commit, before checkpoint")
		}
		return nil
	}
	st, err := execution.Run(w.ctx, opts)
	if !rtgerr.IsCode(err, rtgerr.ExecInterrupted) || st.Status != runstate.StatusInterrupted {
		t.Fatalf("got %v, status %s", err, st.Status)
	}

	opts.Fault = nil
	opts.ResumeRunID = st.RunID
	st, err = execution.Run(w.ctx, opts)
	if err != nil || st.Status != runstate.StatusSucceeded || st.Attempts != 2 {
		t.Fatalf("resume: %v, status %s, attempts %d", err, st.Status, st.Attempts)
	}
	if got := w.counts(); got != wantEcommerce {
		t.Errorf("graph after resume: %s, want %s (no duplicates)", got, wantEcommerce)
	}
	if v, err := verification.Verify(w.ctx, p, w.src, w.dst, 1); err != nil || v.Status != "passed" {
		t.Errorf("verify after resume: %v %+v", err, v)
	}
}

func TestTypeFidelity(t *testing.T) {
	w := newWorld(t, `
		CREATE TABLE things (
			id     bigint PRIMARY KEY,
			flag   boolean,
			small  smallint,
			amount numeric(30,10),
			ratio  double precision,
			code   char(4),
			body   text,
			ref    uuid,
			born   date,
			local  timestamp(6),
			at     timestamptz,
			blob   bytea,
			doc    jsonb,
			tags   text[],
			nums   integer[]
		);
		INSERT INTO things VALUES
		  (1, true, -32768, 12345678901234567890.0123456789, 'NaN', 'ab', 'Grüße ☕',
		   '6F9619FF-8B86-D011-B42D-00C04FC964FF', '0001-01-01', '2026-10-21 14:30:00.123456',
		   '2026-10-21 14:30:00.5+02', '\xdeadbeef', '{"b": [1, 2.5], "a": null}', '{x,"y z"}', '{1,2}'),
		  (2, NULL, NULL, NULL, '-Infinity', NULL, '', NULL, NULL, NULL, NULL, '\x', '[]', '{}', '{3,NULL}'),
		  (3, false, 7, 0.10, 1e300, 'abcd', 'x', NULL, '9999-12-31', '1999-01-01 00:00:00', '2026-01-01 00:00:00Z', NULL,
		   '{"n": 12345678901234567890}', NULL, NULL);
	`)
	m := w.adapt(mustDecode(t, `
specVersion: "0.1.0"
name: things
direction: relational-to-graph
source: { kind: postgresql }
destination: { kind: neo4j }
policies:
  loss: flexible
entities:
  - name: Thing
    table: things
    labels: [Thing]
    key: [id]
    properties:
      - { column: id, property: id, type: int64 }
      - { column: flag, property: flag, type: bool }
      - { column: small, property: small, type: int64 }
      - { column: amount, property: amount, type: decimal }
      - { column: ratio, property: ratio, type: float64 }
      - { column: code, property: code, type: string }
      - { column: body, property: body, type: string }
      - { column: ref, property: ref, type: uuid }
      - { column: born, property: born, type: date }
      - { column: local, property: local, type: timestamp }
      - { column: at, property: at, type: timestamptz }
      - { column: blob, property: blob, type: bytes }
      - { column: doc, property: doc, type: json }
      - { column: tags, property: tags, type: array, element: string }
      - { column: nums, property: nums, type: array, element: int64 }
`))
	p := w.plan(m)
	store := &runstate.Store{Dir: t.TempDir()}
	st, err := execution.Run(w.ctx, w.options(m, p, store, 10))
	if err != nil {
		t.Fatal(err)
	}
	// Row 2 has an array with NULL (Neo4j lists cannot hold null); row 3 has a
	// JSON number a double cannot represent exactly. Both must fail loudly.
	failures, _ := store.Failures(st)
	var got []string
	for _, f := range failures {
		got = append(got, f.Key[0].Value+" "+f.Code)
	}
	if strings.Join(got, ",") != "2 RTG-EXEC-001,3 RTG-EXEC-001" || st.Status != runstate.StatusSucceededWithFailures {
		t.Errorf("failures: %v, status %s", got, st.Status)
	}

	rec := w.cypher("MATCH (t:Thing_"+w.sfx+" {id: 1}) RETURN t.flag, t.small, t.amount, t.ratio, t.code, t.body, t.ref, "+
		"toString(t.born), toString(t.local), toString(t.at), t.blob, t.doc, t.tags, t.nums", nil)
	if len(rec) != 1 {
		t.Fatalf("row 1 missing")
	}
	vals := rec[0].Values
	checks := []struct {
		name string
		got  any
		want string
	}{
		{"flag", vals[0], "true"},
		{"small", vals[1], "-32768"},
		{"amount (exact, 30 digits)", vals[2], "12345678901234567890.0123456789"},
		{"ratio NaN", vals[3], "NaN"},
		{"code keeps char(4) padding", vals[4], "ab  "},
		{"body", vals[5], "Grüße ☕"},
		{"ref lowercased", vals[6], "6f9619ff-8b86-d011-b42d-00c04fc964ff"},
		{"born year 1", vals[7], "0001-01-01"},
		{"local microseconds", vals[8], "2026-10-21T14:30:00.123456"},
		{"at in UTC", vals[9], "2026-10-21T12:30:00.5Z[UTC]"},
		{"blob", vals[10], "[222 173 190 239]"},
		{"doc canonical", vals[11], `{"a":null,"b":[1,2.5]}`},
		{"tags", vals[12], "[x y z]"},
		{"nums", vals[13], "[1 2]"},
	}
	for _, c := range checks {
		if s := fmt.Sprint(c.got); s != c.want {
			t.Errorf("%s: got %q, want %q", c.name, s, c.want)
		}
	}
}

func mustDecode(t *testing.T, doc string) *mapping.Mapping {
	t.Helper()
	m, issues := mapping.Decode([]byte(doc), mapping.FormatYAML)
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	return m
}

// TestBoundedMemory migrates a table far larger than the memory ceiling.
// Loading it whole would need ~300 MiB; the heap must stay well below.
func TestBoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	const rows = 150_000
	w := newWorld(t, fmt.Sprintf(`
		CREATE TABLE big (id bigint PRIMARY KEY, payload text NOT NULL);
		INSERT INTO big SELECT g, repeat(md5(g::text), 64) FROM generate_series(1, %d) AS g;
	`, rows))
	m := w.adapt(mustDecode(t, `
specVersion: "0.1.0"
name: big
direction: relational-to-graph
source: { kind: postgresql }
destination: { kind: neo4j }
entities:
  - name: Big
    table: big
    labels: [Big]
    key: [id]
    properties:
      - { column: id, property: id, type: int64 }
      - { column: payload, property: payload, type: string }
`))
	p := w.plan(m)
	store := &runstate.Store{Dir: t.TempDir()}

	runtime.GC()
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peak.Load() {
					peak.Store(ms.HeapInuse)
				}
			}
		}
	}()
	st, err := execution.Run(w.ctx, w.options(m, p, store, 1000))
	close(stop)
	wg.Wait()
	if err != nil || st.Status != runstate.StatusSucceeded {
		t.Fatalf("%v %s", err, st.Status)
	}
	if n := w.cypher("MATCH (n:Big_"+w.sfx+") RETURN count(n) AS n", nil)[0].Values[0]; n != int64(rows) {
		t.Fatalf("got %v nodes", n)
	}
	const ceiling = 96 << 20
	t.Logf("peak heap in use: %d MiB for %d rows of ~2 KiB", peak.Load()>>20, rows)
	if peak.Load() > ceiling {
		t.Errorf("peak heap %d MiB exceeds the %d MiB ceiling", peak.Load()>>20, ceiling>>20)
	}
	b, _ := json.Marshal(st.Stream("nodes:Big"))
	t.Logf("stream: %s", b)
}
