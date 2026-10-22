package rtogtor

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
	"github.com/TuanFaied/rtogtor/engine/adapters/graph/neo4j"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational/postgresql"
	"github.com/TuanFaied/rtogtor/engine/execution"
	"github.com/TuanFaied/rtogtor/engine/internal/canonjson"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/planning"
	"github.com/TuanFaied/rtogtor/engine/reporting"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/runstate"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
	"github.com/TuanFaied/rtogtor/engine/verification"
)

// Types returned by the API.
type (
	Plan           = planning.Plan
	Report         = reporting.Report
	Verification   = reporting.Verification
	RunState       = runstate.State
	Failure        = runstate.Failure
	SchemaMetadata = schemameta.Document
	Progress       = execution.Progress
)

// DefaultStateDir is where run state is kept unless RunOptions.StateDir is
// set.
const DefaultStateDir = runstate.DefaultDir

// Inspect returns the schema metadata of a PostgreSQL database. Empty schemas
// means "public".
func Inspect(ctx context.Context, source Connection, schemas ...string) (*SchemaMetadata, error) {
	src, err := openSource(ctx, source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()
	return src.Inspect(ctx, relational.InspectOptions{Schemas: schemas})
}

// PlanFromMetadata plans a mapping against previously inspected metadata,
// without connecting to any database. Problems are returned as an
// rtgerr.List; warnings are in the plan.
func PlanFromMetadata(m *Mapping, meta *SchemaMetadata) (*Plan, error) {
	p, issues := planning.Build(planning.Input{Mapping: m, Source: meta, Destination: neo4j.TypeSupport{}})
	if issues.HasErrors() {
		return nil, issues
	}
	return p, nil
}

// RunOptions configures Migration.Run.
type RunOptions struct {
	// DryRun reads and converts everything and writes nothing.
	DryRun bool
	// BatchSize is the number of rows per batch (default 1000). It is
	// ignored when resuming.
	BatchSize int
	// ResumeRunID resumes an interrupted run.
	ResumeRunID string
	// StateDir holds run state (default ".rtogtor").
	StateDir string
	// Progress, if set, is called after every committed batch.
	Progress func(Progress)
}

// Migration migrates data from a source to a destination with a mapping.
// Connections are opened on first use and closed by Close.
type Migration struct {
	source, destination Connection
	mapping             *Mapping
	src                 *postgresql.Adapter
	dst                 *neo4j.Adapter
}

// NewMigration creates a migration. The destination may be the zero
// Connection for dry runs.
func NewMigration(source, destination Connection, m *Mapping) *Migration {
	return &Migration{source: source, destination: destination, mapping: m}
}

// Close closes any open connections.
func (m *Migration) Close() error {
	var first error
	if m.src != nil {
		first = m.src.Close()
		m.src = nil
	}
	if m.dst != nil {
		if err := m.dst.Close(); err != nil && first == nil {
			first = err
		}
		m.dst = nil
	}
	return first
}

func openSource(ctx context.Context, c Connection) (*postgresql.Adapter, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.product != ProductPostgreSQL {
		return nil, rtgerr.New(rtgerr.ConnConfig, "the source must be a PostgreSQL connection, got %s", c)
	}
	return postgresql.Open(ctx, c.url)
}

func (m *Migration) sourceAdapter(ctx context.Context) (*postgresql.Adapter, error) {
	if m.src == nil {
		src, err := openSource(ctx, m.source)
		if err != nil {
			return nil, err
		}
		m.src = src
	}
	return m.src, nil
}

func (m *Migration) destinationAdapter(ctx context.Context, database string) (*neo4j.Adapter, error) {
	if m.dst != nil {
		return m.dst, nil
	}
	c := m.destination
	if c.err != nil {
		return nil, c.err
	}
	if c.product != ProductNeo4j {
		return nil, rtgerr.New(rtgerr.ConnConfig, "the destination must be a Neo4j connection, got %s", c)
	}
	if database == "" {
		database = c.database
	}
	dst, err := neo4j.Open(ctx, c.url, database)
	if err != nil {
		return nil, err
	}
	m.dst = dst
	return dst, nil
}

// schemasOf lists the source schemas a mapping reads.
func schemasOf(m *Mapping) []string {
	set := map[string]bool{m.Source.Schema: true}
	for _, e := range m.Entities {
		if e.Schema != "" {
			set[e.Schema] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Plan inspects the source and plans the migration. It never writes.
func (m *Migration) Plan(ctx context.Context) (*Plan, error) {
	p, _, err := m.plan(ctx)
	return p, err
}

func (m *Migration) plan(ctx context.Context) (*Plan, runstate.Endpoint, error) {
	src, err := m.sourceAdapter(ctx)
	if err != nil {
		return nil, runstate.Endpoint{}, err
	}
	doc, err := src.Inspect(ctx, relational.InspectOptions{Schemas: schemasOf(m.mapping)})
	if err != nil {
		return nil, runstate.Endpoint{}, err
	}
	info := runstate.Endpoint{Product: doc.Database.Product, Version: doc.Database.Version, Database: doc.Database.Name}
	p, err := PlanFromMetadata(m.mapping, doc)
	return p, info, err
}

// Run executes the migration (or a dry run) and returns its report. When the
// run fails or is interrupted, both the report and the error are returned;
// the report's RunID can be passed to RunOptions.ResumeRunID.
func (m *Migration) Run(ctx context.Context, opts RunOptions) (*Report, error) {
	store := &runstate.Store{Dir: opts.StateDir}
	if store.Dir == "" {
		store.Dir = DefaultStateDir
	}
	hash, err := m.mapping.Hash()
	if err != nil {
		return nil, rtgerr.New(rtgerr.Internal, "hashing the mapping: %v", err)
	}
	mappingJSON, err := canonjson.Marshal(m.mapping)
	if err != nil {
		return nil, rtgerr.New(rtgerr.Internal, "encoding the mapping: %v", err)
	}
	src, err := m.sourceAdapter(ctx)
	if err != nil {
		return nil, err
	}

	var plan *Plan
	var srcInfo runstate.Endpoint
	if opts.ResumeRunID != "" {
		// A resumed run continues the plan its checkpoints refer to.
		plan, err = loadPlan(store, opts.ResumeRunID)
		if err != nil {
			return nil, err
		}
		if plan.Mapping.Hash != hash {
			return nil, rtgerr.New(rtgerr.RunMappingChanged,
				"run %s was started with a different mapping (hash %s, this mapping %s)", opts.ResumeRunID, plan.Mapping.Hash[:12], hash[:12])
		}
	} else if plan, srcInfo, err = m.plan(ctx); err != nil {
		return nil, err
	}

	var dst execution.Destination = dryRunDestination{}
	dstInfo := runstate.Endpoint{Product: ProductNeo4j, Database: plan.Destination.Database}
	if !m.destination.IsZero() {
		g, err := m.destinationAdapter(ctx, plan.Destination.Database)
		if err != nil {
			return nil, err
		}
		info, err := g.ServerInfo(ctx)
		if err != nil {
			return nil, err
		}
		dstInfo = runstate.Endpoint{Product: info.Product, Version: info.Version, Database: info.Database}
		dst = g
	} else if !opts.DryRun {
		return nil, rtgerr.New(rtgerr.ConnConfig, "a destination connection is required unless DryRun is set")
	}

	st, runErr := execution.Run(ctx, execution.Options{
		Plan: plan, MappingJSON: mappingJSON, Source: src, Destination: dst,
		SourceInfo: srcInfo, DestinationInfo: dstInfo, Store: store,
		BatchSize: opts.BatchSize, DryRun: opts.DryRun, ResumeRunID: opts.ResumeRunID, Progress: opts.Progress,
	})
	if st == nil {
		return nil, runErr
	}
	rep, err := LoadReport(store.Dir, st.RunID)
	if err != nil {
		return nil, err
	}
	return rep, runErr
}

// Verify compares a finished run's source and destination and adds the
// result to the run's report.
func (m *Migration) Verify(ctx context.Context, runID, stateDir string) (*Verification, error) {
	store := &runstate.Store{Dir: stateDir}
	if store.Dir == "" {
		store.Dir = DefaultStateDir
	}
	st, err := store.Load(runID)
	if err != nil {
		return nil, err
	}
	if st.DryRun {
		return nil, rtgerr.New(rtgerr.RunCompleted, "run %s was a dry run; nothing was written, so there is nothing to verify", runID)
	}
	plan, err := loadPlan(store, runID)
	if err != nil {
		return nil, err
	}
	src, err := m.sourceAdapter(ctx)
	if err != nil {
		return nil, err
	}
	dst, err := m.destinationAdapter(ctx, plan.Destination.Database)
	if err != nil {
		return nil, err
	}
	v, err := verification.Verify(ctx, plan, src, dst, st.BatchSize)
	if err != nil {
		return nil, err
	}
	rep, err := LoadReport(store.Dir, runID)
	if err != nil {
		return nil, err
	}
	rep.Verification = v
	b, err := canonjson.Marshal(rep)
	if err != nil {
		return nil, rtgerr.New(rtgerr.Internal, "encoding the report: %v", err)
	}
	return v, store.WriteFile(runID, "report.json", b)
}

func loadPlan(store *runstate.Store, runID string) (*Plan, error) {
	b, err := store.ReadFile(runID, "plan.json")
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, rtgerr.New(rtgerr.RunStateCorrupt, "plan of run %s is unreadable: %v", runID, err)
	}
	return &p, nil
}

// LoadReport reads a run's report.
func LoadReport(stateDir, runID string) (*Report, error) {
	store := &runstate.Store{Dir: stateDir}
	b, err := store.ReadFile(runID, "report.json")
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, rtgerr.New(rtgerr.RunStateCorrupt, "report of run %s is unreadable: %v", runID, err)
	}
	return &r, nil
}

// LoadRunState reads a run's state.
func LoadRunState(stateDir, runID string) (*RunState, error) {
	return (&runstate.Store{Dir: stateDir}).Load(runID)
}

// LoadFailures returns a run's failed records.
func LoadFailures(stateDir, runID string) ([]Failure, error) {
	store := &runstate.Store{Dir: stateDir}
	st, err := store.Load(runID)
	if err != nil {
		return nil, err
	}
	return store.Failures(st)
}

// DecodeMapping parses and validates a mapping document. Problems are
// returned as an rtgerr.List.
func DecodeMapping(data []byte, format mapping.Format) (*Mapping, rtgerr.List) {
	return mapping.Decode(data, format)
}

// dryRunDestination stands in for the destination in dry runs without a
// connection. The executor never writes in a dry run; properties are encoded
// with Neo4j's rules so conversion failures are still found.
type dryRunDestination struct{}

func (dryRunDestination) EnsureIdentityConstraint(context.Context, string, string) error {
	return rtgerr.New(rtgerr.Internal, "dry run attempted a write")
}

func (dryRunDestination) EncodeProperties(props []graph.Property) (graph.Encoded, error) {
	return neo4j.EncodeProperties(props)
}

func (dryRunDestination) MergeNodes(context.Context, graph.NodeBatch) error {
	return rtgerr.New(rtgerr.Internal, "dry run attempted a write")
}

func (dryRunDestination) MergeRelationships(context.Context, graph.RelationshipBatch) ([]int, error) {
	return nil, rtgerr.New(rtgerr.Internal, "dry run attempted a write")
}
