//go:build integration

package e2e

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
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

// Graph → relational (ADR-0006): the e-commerce graph written by the
// forward mapping is migrated back to tables and compared with the original.

var ecommerceTables = []string{"customers", "products", "orders", "order_items"}

// backSchema creates an empty destination schema, optionally with the
// example's DDL (without data), and returns its name and a connection.
func (w *world) backSchema(withDDL bool) (string, *pgx.Conn) {
	w.t.Helper()
	name := w.schema + "_back"
	conn, err := pgx.Connect(w.ctx, env(w.t, "RTOGTOR_TEST_POSTGRES_URL"))
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE")
		_ = conn.Close(context.Background())
	})
	if _, err := conn.Exec(w.ctx, "SET TimeZone = 'UTC'; CREATE SCHEMA "+name); err != nil {
		w.t.Fatal(err)
	}
	if withDDL {
		ddl, _, _ := strings.Cut(ecommerce(w.t), "-- Sample data")
		if _, err := conn.PgConn().Exec(w.ctx, "SET search_path TO "+name+";\n"+ddl+"\nSET search_path TO public").ReadAll(); err != nil {
			w.t.Fatal(err)
		}
	}
	return name, conn
}

// forward loads the example graph.
func (w *world) forward() {
	w.t.Helper()
	m := w.load(testutil.RepoPath(w.t, "examples", "ecommerce", "mapping.yaml"))
	st, err := execution.Run(w.ctx, w.options(m, w.plan(m), &runstate.Store{Dir: w.t.TempDir()}, 2))
	if err != nil || st.Status != runstate.StatusSucceeded {
		w.t.Fatalf("forward run: %v, status %s", err, st.Status)
	}
}

// reverseMapping loads the reverse example, with suffixed labels, writing to
// schema.
func (w *world) reverseMapping(schema string) *mapping.Mapping {
	w.t.Helper()
	m, issues := mapping.LoadFile(testutil.RepoPath(w.t, "examples", "ecommerce", "mapping-reverse.yaml"))
	if issues.HasErrors() {
		w.t.Fatal(issues)
	}
	for i := range m.Entities {
		for j, l := range m.Entities[i].Labels {
			m.Entities[i].Labels[j] = l + "_" + w.sfx
		}
	}
	m.Destination.Schema = schema
	return m
}

func (w *world) reversePlan(m *mapping.Mapping) *planning.Plan {
	w.t.Helper()
	p, issues := w.tryReversePlan(m)
	if issues.HasErrors() {
		w.t.Fatal(issues)
	}
	return p
}

func (w *world) tryReversePlan(m *mapping.Mapping) (*planning.Plan, rtgerr.List) {
	w.t.Helper()
	gdoc, err := w.dst.Inspect(w.ctx, graph.InspectOptions{})
	if err != nil {
		w.t.Fatal(err)
	}
	ddoc, err := w.src.Inspect(w.ctx, relational.InspectOptions{Schemas: []string{m.Destination.Schema}})
	if err != nil {
		w.t.Fatal(err)
	}
	return planning.Build(planning.Input{Mapping: m, Source: gdoc, Destination: neo4j.TypeSupport{}, DestinationMeta: ddoc,
		SourceTypes: neo4j.TypeSupport{}, DestinationTypes: postgresql.TypeSupport{}})
}

func (w *world) reverseOptions(m *mapping.Mapping, p *planning.Plan, store *runstate.Store, batch int) execution.Options {
	mj, _ := canonjson.Marshal(m)
	return execution.Options{
		Plan: p, MappingJSON: mj, GraphSource: w.dst, RelationalDestination: w.src, Store: store, BatchSize: batch,
		SourceInfo: runstate.Endpoint{Product: "neo4j"}, DestinationInfo: runstate.Endpoint{Product: "postgresql"},
	}
}

// diff counts rows of a table that are in one schema but not the other.
// Rows compare as jsonb objects: column order and numeric scale (19.90 vs
// 19.9) do not matter, values do.
func diff(t *testing.T, conn *pgx.Conn, a, b, table string) int64 {
	t.Helper()
	q := "SELECT (SELECT count(*) FROM (SELECT to_jsonb(x) FROM " + a + "." + table + " x EXCEPT SELECT to_jsonb(y) FROM " + b + "." + table + " y) d1)" +
		" + (SELECT count(*) FROM (SELECT to_jsonb(y) FROM " + b + "." + table + " y EXCEPT SELECT to_jsonb(x) FROM " + a + "." + table + " x) d2)"
	var n int64
	if err := conn.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("comparing %s: %v", table, err)
	}
	return n
}

func (w *world) sameTables(conn *pgx.Conn, back string) {
	w.t.Helper()
	for _, table := range ecommerceTables {
		if n := diff(w.t, conn, w.schema, back, table); n != 0 {
			w.t.Errorf("%s: %d rows differ between source and round trip", table, n)
		}
	}
}

func failureCodes(t *testing.T, store *runstate.Store, st *runstate.State) map[string]int {
	t.Helper()
	fs, err := store.Failures(st)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, f := range fs {
		out[f.StepID+" "+f.Code]++
	}
	return out
}

func TestRoundTripIntoExistingTables(t *testing.T) {
	w := newWorld(t, ecommerce(t))
	w.forward()
	back, conn := w.backSchema(true)
	m := w.reverseMapping(back)
	p := w.reversePlan(m)
	for _, s := range p.Steps {
		if s.Kind == planning.StepTable {
			t.Errorf("existing tables must not be created: %s", s.ID)
		}
	}
	store := &runstate.Store{Dir: t.TempDir()}
	st, err := execution.Run(w.ctx, w.reverseOptions(m, p, store, 2))
	if err != nil || st.Status != runstate.StatusSucceeded {
		t.Fatalf("reverse: %v %s %v", err, st.Status, failureCodes(t, store, st))
	}
	w.sameTables(conn, back)

	v, err := verification.VerifyReverse(w.ctx, p, w.dst, w.src, 2)
	if err != nil || v.Status != "passed" {
		t.Fatalf("verify: %v %+v", err, v)
	}

	// Idempotent: a second run changes nothing.
	if st, err := execution.Run(w.ctx, w.reverseOptions(m, p, store, 3)); err != nil || st.Status != runstate.StatusSucceeded {
		t.Fatalf("second run: %v", err)
	}
	w.sameTables(conn, back)

	// Verification notices a missing row.
	if _, err := conn.Exec(w.ctx, "DELETE FROM "+back+".order_items WHERE order_id = 1002"); err != nil {
		t.Fatal(err)
	}
	v, err = verification.VerifyReverse(w.ctx, p, w.dst, w.src, 2)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "failed" || len(v.Issues) == 0 || v.Issues[0].Code != string(rtgerr.VerifyMissingEntity) {
		t.Errorf("verification after deleting a row: %+v", v)
	}
}

func TestRoundTripCreatingTables(t *testing.T) {
	w := newWorld(t, ecommerce(t))
	w.forward()
	back, conn := w.backSchema(false)
	m := w.reverseMapping(back)
	p := w.reversePlan(m)
	store := &runstate.Store{Dir: t.TempDir()}

	// Dry run: no table is created.
	dry := w.reverseOptions(m, p, store, 2)
	dry.DryRun = true
	if st, err := execution.Run(w.ctx, dry); err != nil || st.Status != runstate.StatusSucceeded {
		t.Fatalf("dry run: %v", err)
	}
	var tables int
	if err := conn.QueryRow(w.ctx, "SELECT count(*) FROM pg_tables WHERE schemaname = $1", back).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("dry run created %d tables (%v)", tables, err)
	}

	st, err := execution.Run(w.ctx, w.reverseOptions(m, p, store, 2))
	if err != nil || st.Status != runstate.StatusSucceeded {
		t.Fatalf("reverse: %v %s %v", err, st.Status, failureCodes(t, store, st))
	}
	w.sameTables(conn, back)
	if v, err := verification.VerifyReverse(w.ctx, p, w.dst, w.src, 2); err != nil || v.Status != "passed" {
		t.Fatalf("verify: %v %+v", err, v)
	}
}

func TestReverseResumeAfterInjectedFailure(t *testing.T) {
	w := newWorld(t, ecommerce(t))
	w.forward()
	back, conn := w.backSchema(false)
	m := w.reverseMapping(back)
	p := w.reversePlan(m)
	store := &runstate.Store{Dir: t.TempDir()}

	opts := w.reverseOptions(m, p, store, 1)
	opts.Fault = func(step string, batch int64, point execution.FaultPoint) error {
		if (step == "rows:Product" || step == "foreign-keys:CustomerReferredBy") && batch == 1 && point == execution.BeforeCheckpoint {
			return rtgerr.New(rtgerr.ExecInterrupted, "injected failure after commit, before checkpoint")
		}
		return nil
	}
	st, err := execution.Run(w.ctx, opts)
	if !rtgerr.IsCode(err, rtgerr.ExecInterrupted) || st.Status != runstate.StatusInterrupted {
		t.Fatalf("got %v, status %s", err, st.Status)
	}
	opts.ResumeRunID = st.RunID
	st, err = execution.Run(w.ctx, opts) // fails again, in the deferred step
	if !rtgerr.IsCode(err, rtgerr.ExecInterrupted) || st.Attempts != 2 {
		t.Fatalf("second attempt: %v, attempts %d", err, st.Attempts)
	}
	opts.Fault = nil
	st, err = execution.Run(w.ctx, opts)
	if err != nil || st.Status != runstate.StatusSucceeded || st.Attempts != 3 {
		t.Fatalf("resume: %v, status %s, attempts %d %v", err, st.Status, st.Attempts, failureCodes(t, store, st))
	}
	w.sameTables(conn, back)
}

// TestReverseGraphShapes covers graph data that does not fit a table: every
// case is a counted failed record, never a silent change.
func TestReverseGraphShapes(t *testing.T) {
	w := newWorld(t, "")
	w.labels = []string{"Customer_" + w.sfx, "Product_" + w.sfx, "Order_" + w.sfx, "OrderItem_" + w.sfx}
	c, o, pr := "Customer_"+w.sfx, "Order_"+w.sfx, "Product_"+w.sfx
	w.cypher(`
CREATE (a:`+c+` {id: 1, email: 'a@example.com', fullName: 'A', createdAt: datetime('2026-01-01T00:00:00Z')})
CREATE (:`+c+` {id: 1, email: 'dup@example.com', fullName: 'Duplicate', createdAt: datetime('2026-01-01T00:00:00Z')})
CREATE (b:`+c+` {id: 2, email: 'b@example.com', fullName: 'B', createdAt: datetime('2026-01-01T00:00:00.123456789Z')})
CREATE (d:`+c+` {id: 3, email: 'd@example.com', fullName: 'D', createdAt: datetime('2026-01-01T00:00:00+02:00')})
CREATE (:`+c+` {email: 'nokey@example.com', fullName: 'No key'})
CREATE (:`+c+` {id: 'four', email: 'str@example.com', fullName: 'String key'})
CREATE (d)-[:REFERRED_BY]->(a), (d)-[:REFERRED_BY]->(b)
CREATE (p:`+pr+` {id: 10, sku: 'TEE', name: 'T-shirt', price: '19.9'})
CREATE (o1:`+o+` {id: 1001, status: 'shipped', placedAt: datetime('2026-04-01T10:00:00Z')})
CREATE (o2:`+o+` {id: 1002, status: 'pending', placedAt: datetime('2026-04-02T10:00:00Z')})
CREATE (o3:`+o+` {id: 1003, status: 'pending', placedAt: datetime('2026-04-03T10:00:00Z')})
CREATE (a)-[:PLACED]->(o1), (p)-[:PLACED]->(o2)`, nil)

	back, conn := w.backSchema(false)
	m := w.reverseMapping(back)
	p := w.reversePlan(m)
	store := &runstate.Store{Dir: t.TempDir()}
	st, err := execution.Run(w.ctx, w.reverseOptions(m, p, store, 2))
	if err != nil || st.Status != runstate.StatusSucceededWithFailures {
		t.Fatalf("run: %v %s", err, st.Status)
	}
	got := failureCodes(t, store, st)
	want := map[string]int{
		"rows:Customer RTG-ID-001":                     1, // one aggregate record for two nodes
		"rows:Customer RTG-EXEC-006":                   1,
		"rows:Customer RTG-EXEC-007":                   1,
		"rows:Order RTG-EXEC-002":                      1,
		"foreign-keys:CustomerReferredBy RTG-EXEC-005": 1,
	}
	keys := func(m map[string]int) string {
		var out []string
		for k, v := range m {
			out = append(out, k+"="+string(rune('0'+v)))
		}
		sort.Strings(out)
		return strings.Join(out, " ")
	}
	if keys(got) != keys(want) {
		t.Errorf("failures:\n got %s\nwant %s", keys(got), keys(want))
	}
	if s := st.Stream("rows:Customer"); s.NullKeys != 2 || s.Written != 2 || s.Read != 4 {
		t.Errorf("rows:Customer stream = %+v", s)
	}

	var rows []string
	r, err := conn.Query(w.ctx, "SELECT id || ':' || full_name || ':' || created_at::text || ':' || coalesce(referred_by::text, '-') FROM "+back+".customers ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var s string
		_ = r.Scan(&s)
		rows = append(rows, s)
	}
	r.Close()
	// The first node with key 1 wins; id 3's offset is normalized to UTC;
	// its two REFERRED_BY relationships leave referred_by NULL.
	if len(rows) != 2 || !strings.HasPrefix(rows[0], "1:A:") || !strings.HasPrefix(rows[1], "3:D:2025-12-31 22:00:00") || !strings.HasSuffix(rows[1], ":-") {
		t.Errorf("customers = %v", rows)
	}
	var orders string
	if err := conn.QueryRow(w.ctx, "SELECT string_agg(id || ':' || coalesce(customer_id::text, '-'), ' ' ORDER BY id) FROM "+back+".orders").Scan(&orders); err != nil {
		t.Fatal(err)
	}
	if orders != "1001:1 1003:-" {
		t.Errorf("orders = %s (1002 points at a product and must fail; 1003 has no customer)", orders)
	}

	// The flexible policy writes the nanosecond timestamp, rounded.
	m.Policies.Loss = mapping.LossFlexible
	p = w.reversePlan(m)
	if _, err := execution.Run(w.ctx, w.reverseOptions(m, p, &runstate.Store{Dir: t.TempDir()}, 2)); err != nil {
		t.Fatal(err)
	}
	var ts string
	if err := conn.QueryRow(w.ctx, "SELECT created_at::text FROM "+back+".customers WHERE id = 2").Scan(&ts); err != nil {
		t.Fatalf("customer 2 not written under the flexible policy: %v", err)
	}
	if ts != "2026-01-01 00:00:00.123457+00" {
		t.Errorf("rounded timestamp = %s", ts)
	}
}
