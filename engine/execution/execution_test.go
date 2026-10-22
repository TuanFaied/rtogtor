package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
	"github.com/TuanFaied/rtogtor/engine/adapters/graph/neo4j"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/internal/jsonschemas"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/planning"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/runstate"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// ---- fixtures ---------------------------------------------------------------

const testMapping = `
specVersion: "0.1.0"
name: people
direction: relational-to-graph
source: { kind: postgresql }
destination: { kind: neo4j }
entities:
  - name: Person
    table: people
    labels: [Person]
    key: [id]
    properties:
      - { column: id, property: id, type: int64 }
      - { column: name, property: name, type: string }
relationships:
  - name: FriendOf
    type: FRIEND_OF
    foreignKey: { entity: Person, columns: [friend_id], references: Person, direction: owner-to-referenced }
`

func ptr[T any](v T) *T { return &v }

func testPlan(t *testing.T) (*planning.Plan, []byte) {
	t.Helper()
	m, issues := mapping.Decode([]byte(testMapping), mapping.FormatYAML)
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	col := func(name string, typ ir.LogicalType, native string, nullable bool) schemameta.Column {
		return schemameta.Column{Name: name, Nullable: nullable, TypeInfo: schemameta.TypeInfo{Type: ptr(typ), NativeType: native}}
	}
	doc := &schemameta.Document{
		SpecVersion: spec.Version, Kind: schemameta.KindRelational,
		Database: schemameta.Database{Product: "postgresql"},
		Schemas: []schemameta.Schema{{Name: "public", Tables: []schemameta.Table{{
			Name: "people",
			Columns: []schemameta.Column{
				col("id", ir.TypeInt64, "bigint", false),
				col("name", ir.TypeString, "text", false),
				col("friend_id", ir.TypeInt64, "bigint", true),
			},
			PrimaryKey: &schemameta.KeyConstraint{Name: "people_pkey", Columns: []string{"id"}},
		}}}},
	}
	p, issues := planning.Build(planning.Input{Mapping: m, Source: doc, Destination: neo4j.TypeSupport{}})
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	mj, _ := json.Marshal(m)
	return p, mj
}

// person describes one source row.
type person struct {
	id       int64
	name     string
	friend   int64 // 0 = NULL
	badValue bool  // simulate a conversion failure
}

// fakeSource serves the people table in key order.
type fakeSource struct {
	rows     []person
	nullKeys int64
	mu       sync.Mutex
	reads    int
}

func (s *fakeSource) ReadBatch(_ context.Context, req relational.ReadRequest) ([]relational.Row, error) {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	var after int64 = -1 << 62
	if req.After != nil {
		after, _ = strconv.ParseInt(req.After[0].Text, 10, 64)
	}
	sorted := append([]person{}, s.rows...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].id < sorted[j].id })
	var out []relational.Row
	for _, p := range sorted {
		if p.id <= after {
			continue
		}
		if len(out) == req.Limit {
			break
		}
		row := relational.Row{Key: []ir.Value{ir.TextValue(strconv.FormatInt(p.id, 10))}}
		if p.badValue {
			row.Err = rtgerr.New(rtgerr.ExecConversion, "column name: invalid UTF-8")
		} else {
			for _, c := range req.Columns {
				switch c.Name {
				case "id":
					row.Values = append(row.Values, ir.TextValue(strconv.FormatInt(p.id, 10)))
				case "name":
					row.Values = append(row.Values, ir.TextValue(p.name))
				case "friend_id":
					if p.friend == 0 {
						row.Values = append(row.Values, ir.NullValue())
					} else {
						row.Values = append(row.Values, ir.TextValue(strconv.FormatInt(p.friend, 10)))
					}
				}
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *fakeSource) CountNullKeys(context.Context, relational.Table, []string) (int64, error) {
	return s.nullKeys, nil
}

// fakeGraph is an in-memory graph with transactional batches.
type fakeGraph struct {
	mu          sync.Mutex
	constraints map[string]bool
	nodes       map[string]map[string]graph.Encoded // label -> id -> props
	rels        map[string]int                      // "type|from|to" -> count
	transient   int                                 // fail this many writes with a retryable error
	writes      int
}

func newFakeGraph() *fakeGraph {
	return &fakeGraph{constraints: map[string]bool{}, nodes: map[string]map[string]graph.Encoded{}, rels: map[string]int{}}
}

func (g *fakeGraph) EnsureIdentityConstraint(_ context.Context, label, prop string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.constraints[label+"."+prop] = true
	return nil
}

func (g *fakeGraph) EncodeProperties(props []graph.Property) (graph.Encoded, error) {
	out := graph.Encoded{}
	for _, p := range props {
		out[p.Name] = p.Value.Text
	}
	return out, nil
}

func (g *fakeGraph) transientFailure() error {
	if g.transient > 0 {
		g.transient--
		return rtgerr.New(rtgerr.ExecBatchFailed, "simulated transient failure")
	}
	return nil
}

func (g *fakeGraph) MergeNodes(_ context.Context, b graph.NodeBatch) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.transientFailure(); err != nil {
		return err
	}
	g.writes++
	label := b.Labels[0]
	if g.nodes[label] == nil {
		g.nodes[label] = map[string]graph.Encoded{}
	}
	for _, r := range b.Rows {
		g.nodes[label][r.ID] = r.Props // MERGE: same id, same node
	}
	return nil
}

func (g *fakeGraph) MergeRelationships(_ context.Context, b graph.RelationshipBatch) ([]int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.transientFailure(); err != nil {
		return nil, err
	}
	g.writes++
	var missing []int
	for i, r := range b.Rows {
		_, fromOK := g.nodes[b.FromLabel][r.From]
		_, toOK := g.nodes[b.ToLabel][r.To]
		if !fromOK || !toOK {
			missing = append(missing, i)
			continue
		}
		g.rels[b.Type+"|"+r.From+"|"+r.To] = 1 // MERGE
	}
	return missing, nil
}

func (g *fakeGraph) snapshot() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var parts []string
	for label, ns := range g.nodes {
		for id, p := range ns {
			parts = append(parts, fmt.Sprintf("N %s %s %v", label, id[:8], p))
		}
	}
	for k := range g.rels {
		parts = append(parts, "R "+k)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func people(n int) []person {
	out := make([]person, n)
	for i := range out {
		id := int64(i + 1)
		out[i] = person{id: id, name: fmt.Sprintf("p%d", id)}
		if id > 1 {
			out[i].friend = id - 1
		}
	}
	return out
}

func options(t *testing.T, src *fakeSource, g *fakeGraph, store *runstate.Store) Options {
	p, mj := testPlan(t)
	return Options{
		Plan: p, MappingJSON: mj, Source: src, Destination: g, Store: store, BatchSize: 3,
		SourceInfo: runstate.Endpoint{Product: "postgresql"}, DestinationInfo: runstate.Endpoint{Product: "neo4j"},
		Retry: RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
	}
}

func validate(t *testing.T, store *runstate.Store, runID string) {
	t.Helper()
	for file, schema := range map[string]string{"state.json": spec.RunStateSchemaFile, "report.json": spec.ReportSchemaFile, "plan.json": spec.PlanSchemaFile} {
		b, err := store.ReadFile(runID, file)
		if err != nil {
			t.Fatal(err)
		}
		v, err := jsonschemas.ValidateJSON(schema, b)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range v {
			t.Errorf("%s violates %s at %s: %s", file, schema, x.Path, x.Message)
		}
	}
}

// ---- tests ------------------------------------------------------------------

func TestFullRun(t *testing.T) {
	src, g, store := &fakeSource{rows: people(10)}, newFakeGraph(), &runstate.Store{Dir: t.TempDir()}
	st, err := Run(context.Background(), options(t, src, g, store))
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != runstate.StatusSucceeded || st.Attempts != 1 {
		t.Errorf("status %s, attempts %d", st.Status, st.Attempts)
	}
	if !g.constraints["Person.rtg_id"] {
		t.Error("identity constraint not created")
	}
	if len(g.nodes["Person"]) != 10 || len(g.rels) != 9 {
		t.Errorf("got %d nodes and %d relationships, want 10 and 9", len(g.nodes["Person"]), len(g.rels))
	}
	nodes := st.Stream("nodes:Person")
	rels := st.Stream("relationships:FriendOf")
	if nodes.Read != 10 || nodes.Written != 10 || nodes.Batches != 4 || !nodes.Done {
		t.Errorf("node stream: %+v", nodes)
	}
	if rels.Read != 10 || rels.Written != 9 || rels.Skipped != 1 {
		t.Errorf("relationship stream (person 1 has no friend): %+v", rels)
	}
	validate(t, store, st.RunID)
}

func TestRerunIsIdempotent(t *testing.T) {
	src, g := &fakeSource{rows: people(7)}, newFakeGraph()
	store := &runstate.Store{Dir: t.TempDir()}
	if _, err := Run(context.Background(), options(t, src, g, store)); err != nil {
		t.Fatal(err)
	}
	first := g.snapshot()
	if _, err := Run(context.Background(), options(t, src, g, store)); err != nil {
		t.Fatal(err)
	}
	if g.snapshot() != first {
		t.Error("a second run changed the destination")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	src, g, store := &fakeSource{rows: people(5)}, newFakeGraph(), &runstate.Store{Dir: t.TempDir()}
	opts := options(t, src, g, store)
	opts.DryRun = true
	st, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if g.writes != 0 || len(g.constraints) != 0 || g.snapshot() != "" {
		t.Errorf("dry run touched the destination: writes=%d constraints=%v", g.writes, g.constraints)
	}
	if !st.DryRun || st.Stream("nodes:Person").Written != 5 {
		t.Errorf("dry run should count would-be writes: %+v", st.Stream("nodes:Person"))
	}
	validate(t, store, st.RunID)
}

var errCrash = rtgerr.New(rtgerr.ExecInterrupted, "simulated crash")

func TestResumeAfterCrash(t *testing.T) {
	for _, point := range []FaultPoint{BeforeWrite, BeforeCheckpoint, AfterCheckpoint} {
		for _, step := range []string{"nodes:Person", "relationships:FriendOf"} {
			t.Run(string(point)+"/"+step, func(t *testing.T) {
				// Reference result without faults.
				ref := newFakeGraph()
				if _, err := Run(context.Background(), options(t, &fakeSource{rows: people(10)}, ref, &runstate.Store{Dir: t.TempDir()})); err != nil {
					t.Fatal(err)
				}

				src, g, store := &fakeSource{rows: people(10)}, newFakeGraph(), &runstate.Store{Dir: t.TempDir()}
				opts := options(t, src, g, store)
				opts.Fault = func(s string, batch int64, p FaultPoint) error {
					if s == step && batch == 1 && p == point {
						return errCrash
					}
					return nil
				}
				st, err := Run(context.Background(), opts)
				if !rtgerr.IsCode(err, rtgerr.ExecInterrupted) || st.Status != runstate.StatusInterrupted {
					t.Fatalf("got %v, status %s", err, st.Status)
				}

				opts.Fault = nil
				opts.ResumeRunID = st.RunID
				st, err = Run(context.Background(), opts)
				if err != nil {
					t.Fatal(err)
				}
				if st.Status != runstate.StatusSucceeded || st.Attempts != 2 {
					t.Errorf("status %s, attempts %d", st.Status, st.Attempts)
				}
				if g.snapshot() != ref.snapshot() {
					t.Errorf("resumed result differs from an uninterrupted run:\n%s\nwant\n%s", g.snapshot(), ref.snapshot())
				}
				// Counts must not double-count the replayed batch.
				if s := st.Stream("nodes:Person"); s.Read != 10 || s.Written != 10 {
					t.Errorf("node counts after resume: %+v", s)
				}
				if s := st.Stream("relationships:FriendOf"); s.Read != 10 || s.Written != 9 {
					t.Errorf("relationship counts after resume: %+v", s)
				}
				validate(t, store, st.RunID)
			})
		}
	}
}

func TestResumeRefusals(t *testing.T) {
	src, g, store := &fakeSource{rows: people(4)}, newFakeGraph(), &runstate.Store{Dir: t.TempDir()}
	opts := options(t, src, g, store)
	st, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.ResumeRunID = st.RunID
	if _, err := Run(context.Background(), opts); !rtgerr.IsCode(err, rtgerr.RunCompleted) {
		t.Errorf("resuming a finished run: got %v", err)
	}

	opts.ResumeRunID = ""
	opts.Fault = func(string, int64, FaultPoint) error { return errCrash }
	st, _ = Run(context.Background(), opts)
	opts.Fault = nil
	opts.ResumeRunID = st.RunID
	changed := *opts.Plan
	changed.Mapping.Hash = strings.Repeat("0", 64)
	opts.Plan = &changed
	if _, err := Run(context.Background(), opts); !rtgerr.IsCode(err, rtgerr.RunMappingChanged) {
		t.Errorf("resuming with a different mapping: got %v", err)
	}

	opts.ResumeRunID = "01234567-89ab-7def-8123-456789abcdef"
	if _, err := Run(context.Background(), opts); !rtgerr.IsCode(err, rtgerr.RunNotFound) {
		t.Errorf("unknown run: got %v", err)
	}
	opts.ResumeRunID = "../../etc"
	if _, err := Run(context.Background(), opts); !rtgerr.IsCode(err, rtgerr.RunNotFound) {
		t.Errorf("path traversal run ID: got %v", err)
	}
}

func TestFailedRecordsAreRecordedOnce(t *testing.T) {
	rows := people(8)
	rows[4].badValue = true // person 5 cannot be converted
	src, g, store := &fakeSource{rows: rows, nullKeys: 2}, newFakeGraph(), &runstate.Store{Dir: t.TempDir()}
	opts := options(t, src, g, store)
	// Batch 1 holds ids 4-6. Crash after it is written and its failed record
	// (person 5) is appended, but before the checkpoint. The batch is then
	// uncommitted from the run's point of view and is replayed on resume,
	// appending person 5's failure a second time.
	opts.Fault = func(s string, batch int64, p FaultPoint) error {
		if s == "nodes:Person" && batch == 1 && p == BeforeCheckpoint {
			return errCrash
		}
		return nil
	}
	st, _ := Run(context.Background(), opts)
	if pending, _ := store.Failures(st); len(pending) != 1 || pending[0].Code != "RTG-ID-001" {
		t.Errorf("failures of an uncommitted batch must not be listed yet: %+v", pending)
	}
	opts.Fault = nil
	opts.ResumeRunID = st.RunID
	st, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != runstate.StatusSucceededWithFailures {
		t.Errorf("status %s", st.Status)
	}
	failures, err := store.Failures(st)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range failures {
		key := ""
		if len(f.Key) > 0 {
			key = f.Key[0].Value
		}
		got = append(got, f.StepID+" "+f.Code+" "+key)
	}
	// Person 5: conversion failure on the node step and the relationship
	// step; person 6's FRIEND_OF points at missing person 5; one aggregate
	// entry for the 2 NULL keys.
	want := []string{
		"nodes:Person RTG-ID-001 ",
		"nodes:Person RTG-EXEC-001 5",
		"relationships:FriendOf RTG-EXEC-001 5",
		"relationships:FriendOf RTG-EXEC-002 6",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("failures:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if s := st.Stream("nodes:Person"); s.Failed != 1 || s.NullKeys != 2 || s.Written != 7 {
		t.Errorf("node stream: %+v", s)
	}
	validate(t, store, st.RunID)
}

func TestTransientErrorsAreRetried(t *testing.T) {
	src, g, store := &fakeSource{rows: people(3)}, newFakeGraph(), &runstate.Store{Dir: t.TempDir()}
	g.transient = 2
	if st, err := Run(context.Background(), options(t, src, g, store)); err != nil || st.Status != runstate.StatusSucceeded {
		t.Fatalf("retries should absorb 2 transient failures: %v", err)
	}

	g2 := newFakeGraph()
	g2.transient = 100
	st, err := Run(context.Background(), options(t, &fakeSource{rows: people(3)}, g2, store))
	if !rtgerr.IsCode(err, rtgerr.ExecBatchFailed) || st.Status != runstate.StatusInterrupted {
		t.Fatalf("persistent transient failure: %v, status %s", err, st.Status)
	}
	if st.LastError == nil || st.LastError.Code != "RTG-EXEC-003" {
		t.Errorf("last error: %+v", st.LastError)
	}
}

func TestCancellationInterruptsCleanly(t *testing.T) {
	src, g, store := &fakeSource{rows: people(20)}, newFakeGraph(), &runstate.Store{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	opts := options(t, src, g, store)
	opts.Fault = func(s string, batch int64, p FaultPoint) error {
		if batch == 2 && p == AfterCheckpoint {
			cancel()
		}
		return nil
	}
	st, err := Run(ctx, opts)
	if !errors.Is(err, context.Canceled) || st.Status != runstate.StatusInterrupted {
		t.Fatalf("got %v, status %s", err, st.Status)
	}
	opts.Fault = nil
	opts.ResumeRunID = st.RunID
	if _, err = Run(context.Background(), opts); err != nil || len(g.nodes["Person"]) != 20 {
		t.Fatalf("resume after cancel: %v, %d nodes", err, len(g.nodes["Person"]))
	}
}
