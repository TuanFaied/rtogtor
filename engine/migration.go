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

// Inspect returns the schema metadata of a database: tables of the given
// PostgreSQL schemas (empty means "public"), or the labels and relationship
// types of a Neo4j database.
func Inspect(ctx context.Context, c Connection, schemas ...string) (*SchemaMetadata, error) {
	if c.err != nil {
		return nil, c.err
	}
	switch c.product {
	case ProductPostgreSQL:
		a, err := postgresql.Open(ctx, c.url)
		if err != nil {
			return nil, err
		}
		defer func() { _ = a.Close() }()
		return a.Inspect(ctx, relational.InspectOptions{Schemas: schemas})
	case ProductNeo4j:
		a, err := neo4j.Open(ctx, c.url, c.database)
		if err != nil {
			return nil, err
		}
		defer func() { _ = a.Close() }()
		return a.Inspect(ctx, graph.InspectOptions{})
	}
	return nil, rtgerr.New(rtgerr.ConnConfig, "no connection given")
}

// PlanFromMetadata plans a mapping against previously inspected metadata,
// without connecting to any database. source is the inspected source;
// destination is the inspected relational destination, which graph →
// relational mappings need (its existing tables) and relational → graph
// mappings ignore. Problems are returned as an rtgerr.List; warnings are in
// the plan.
func PlanFromMetadata(m *Mapping, source, destination *SchemaMetadata) (*Plan, error) {
	in := planning.Input{Mapping: m, Source: source, Destination: neo4j.TypeSupport{}}
	if m.Direction == mapping.GraphToRelational {
		if destination == nil {
			return nil, rtgerr.New(rtgerr.ConnConfig, "a graph → relational plan needs the inspected destination database")
		}
		in.DestinationMeta = destination
		in.SourceTypes, in.DestinationTypes = neo4j.TypeSupport{}, postgresql.TypeSupport{}
	}
	p, issues := planning.Build(in)
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
//
// The mapping's direction decides the roles: PostgreSQL → Neo4j for
// relational-to-graph, Neo4j → PostgreSQL for graph-to-relational.
type Migration struct {
	source, destination Connection
	mapping             *Mapping
	pg                  *postgresql.Adapter
	neo                 *neo4j.Adapter
}

// NewMigration creates a migration. For relational → graph mappings the
// destination may be the zero Connection for dry runs; graph → relational
// planning always inspects the destination.
func NewMigration(source, destination Connection, m *Mapping) *Migration {
	return &Migration{source: source, destination: destination, mapping: m}
}

// Close closes any open connections.
func (m *Migration) Close() error {
	var first error
	if m.pg != nil {
		first = m.pg.Close()
		m.pg = nil
	}
	if m.neo != nil {
		if err := m.neo.Close(); err != nil && first == nil {
			first = err
		}
		m.neo = nil
	}
	return first
}

func (m *Migration) postgres(ctx context.Context, role string, c Connection) (*postgresql.Adapter, error) {
	if m.pg != nil {
		return m.pg, nil
	}
	if c.err != nil {
		return nil, c.err
	}
	if c.product != ProductPostgreSQL {
		return nil, rtgerr.New(rtgerr.ConnConfig, "the %s must be a PostgreSQL connection, got %s", role, c)
	}
	a, err := postgresql.Open(ctx, c.url)
	if err != nil {
		return nil, err
	}
	m.pg = a
	return a, nil
}

func (m *Migration) graph(ctx context.Context, role string, c Connection, database string) (*neo4j.Adapter, error) {
	if m.neo != nil {
		return m.neo, nil
	}
	if c.err != nil {
		return nil, c.err
	}
	if c.product != ProductNeo4j {
		return nil, rtgerr.New(rtgerr.ConnConfig, "the %s must be a Neo4j connection, got %s", role, c)
	}
	if database == "" {
		database = c.database
	}
	a, err := neo4j.Open(ctx, c.url, database)
	if err != nil {
		return nil, err
	}
	m.neo = a
	return a, nil
}

// reverseAdapters opens both sides of a graph → relational migration.
func (m *Migration) reverseAdapters(ctx context.Context, sourceDatabase string) (*neo4j.Adapter, *postgresql.Adapter, error) {
	g, err := m.graph(ctx, "source", m.source, sourceDatabase)
	if err != nil {
		return nil, nil, err
	}
	if m.destination.IsZero() {
		return nil, nil, rtgerr.New(rtgerr.ConnConfig, "a graph → relational migration needs a destination connection, even for dry runs: planning inspects its tables")
	}
	pg, err := m.postgres(ctx, "destination", m.destination)
	if err != nil {
		return nil, nil, err
	}
	return g, pg, nil
}

// schemasOf lists the relational schemas a mapping reads or writes.
func schemasOf(m *Mapping) []string {
	side := m.Source
	if m.Direction == mapping.GraphToRelational {
		side = m.Destination
	}
	set := map[string]bool{side.Schema: true}
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

func endpointOf(doc *SchemaMetadata) runstate.Endpoint {
	return runstate.Endpoint{Product: doc.Database.Product, Version: doc.Database.Version, Database: doc.Database.Name}
}

// Plan inspects the databases and plans the migration. It never writes.
func (m *Migration) Plan(ctx context.Context) (*Plan, error) {
	p, _, _, err := m.plan(ctx)
	return p, err
}

func (m *Migration) plan(ctx context.Context) (*Plan, runstate.Endpoint, runstate.Endpoint, error) {
	var none runstate.Endpoint
	if m.mapping.Direction == mapping.GraphToRelational {
		g, pg, err := m.reverseAdapters(ctx, m.mapping.Source.Database)
		if err != nil {
			return nil, none, none, err
		}
		gdoc, err := g.Inspect(ctx, graph.InspectOptions{})
		if err != nil {
			return nil, none, none, err
		}
		ddoc, err := pg.Inspect(ctx, relational.InspectOptions{Schemas: schemasOf(m.mapping)})
		if err != nil {
			return nil, none, none, err
		}
		p, err := PlanFromMetadata(m.mapping, gdoc, ddoc)
		return p, endpointOf(gdoc), endpointOf(ddoc), err
	}
	src, err := m.postgres(ctx, "source", m.source)
	if err != nil {
		return nil, none, none, err
	}
	doc, err := src.Inspect(ctx, relational.InspectOptions{Schemas: schemasOf(m.mapping)})
	if err != nil {
		return nil, none, none, err
	}
	p, err := PlanFromMetadata(m.mapping, doc, nil)
	return p, endpointOf(doc), none, err
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

	var plan *Plan
	var srcInfo, dstInfo runstate.Endpoint
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
	} else if plan, srcInfo, dstInfo, err = m.plan(ctx); err != nil {
		return nil, err
	}

	exec := execution.Options{
		Plan: plan, MappingJSON: mappingJSON, Store: store,
		BatchSize: opts.BatchSize, DryRun: opts.DryRun, ResumeRunID: opts.ResumeRunID, Progress: opts.Progress,
	}
	if plan.Direction == mapping.GraphToRelational {
		g, pg, err := m.reverseAdapters(ctx, plan.Source.Database)
		if err != nil {
			return nil, err
		}
		if srcInfo.Product == "" {
			info, err := g.ServerInfo(ctx)
			if err != nil {
				return nil, err
			}
			srcInfo = runstate.Endpoint{Product: info.Product, Version: info.Version, Database: info.Database}
		}
		exec.GraphSource, exec.RelationalDestination = g, pg
		exec.SourceInfo, exec.DestinationInfo = srcInfo, dstInfo
	} else {
		src, err := m.postgres(ctx, "source", m.source)
		if err != nil {
			return nil, err
		}
		var dst execution.Destination = dryRunDestination{}
		dstInfo = runstate.Endpoint{Product: ProductNeo4j, Database: plan.Destination.Database}
		if !m.destination.IsZero() {
			g, err := m.graph(ctx, "destination", m.destination, plan.Destination.Database)
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
		exec.Source, exec.Destination = src, dst
		exec.SourceInfo, exec.DestinationInfo = srcInfo, dstInfo
	}

	st, runErr := execution.Run(ctx, exec)
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
	var v *Verification
	if plan.Direction == mapping.GraphToRelational {
		g, pg, err := m.reverseAdapters(ctx, plan.Source.Database)
		if err != nil {
			return nil, err
		}
		if v, err = verification.VerifyReverse(ctx, plan, g, pg, st.BatchSize); err != nil {
			return nil, err
		}
	} else {
		src, err := m.postgres(ctx, "source", m.source)
		if err != nil {
			return nil, err
		}
		dst, err := m.graph(ctx, "destination", m.destination, plan.Destination.Database)
		if err != nil {
			return nil, err
		}
		if v, err = verification.Verify(ctx, plan, src, dst, st.BatchSize); err != nil {
			return nil, err
		}
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
