package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

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
	"github.com/TuanFaied/rtogtor/engine/verification"
)

// ExitPartial: the migration finished but some records failed.
const ExitPartial = 3

// connFlags are the connection flags shared by several commands.
type connFlags struct {
	sourceEnv string
	destEnv   string
	stateDir  string
}

func (c *connFlags) register(cmd *cobra.Command, needDest bool) {
	cmd.Flags().StringVar(&c.sourceEnv, "source-env", "", "name of the environment variable holding the PostgreSQL URL")
	_ = cmd.MarkFlagRequired("source-env")
	if needDest {
		cmd.Flags().StringVar(&c.destEnv, "dest-env", "", "name of the environment variable holding the Neo4j URL (neo4j://user:password@host:7687)")
	}
}

func registerStateDir(cmd *cobra.Command, dir *string) {
	cmd.Flags().StringVar(dir, "state-dir", runstate.DefaultDir, "directory holding run state, failed records and reports")
}

// destURLFromEnv reads a Neo4j URL from the named environment variable.
func destURLFromEnv(name string) (string, error) {
	v, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(v) == "" {
		return "", rtgerr.New(rtgerr.ConnConfig, "environment variable %s is not set or is empty", name)
	}
	return v, nil
}

// interruptible returns a context cancelled by Ctrl-C.
func interruptible(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(cmd.Context(), os.Interrupt)
}

// loadMapping loads a mapping and prints its problems.
func loadMapping(cmd *cobra.Command, opts *options, path string) (*mapping.Mapping, error) {
	m, issues := mapping.LoadFile(path)
	if issues.HasErrors() {
		return nil, printIssues(cmd, opts, issues)
	}
	return m, nil
}

// printIssues prints errors and warnings and returns an exit error when
// there are errors.
func printIssues(cmd *cobra.Command, opts *options, issues rtgerr.List) error {
	res := validateResult{Valid: !issues.HasErrors(), Diagnostics: []diagnostic{}}
	for _, e := range issues {
		res.Diagnostics = append(res.Diagnostics, diagnostic{Code: e.Code, Severity: e.Severity(), Retryable: e.Retryable(), Path: e.Path, Message: e.Message})
	}
	if opts.format == "json" {
		_ = writeJSON(cmd.OutOrStdout(), res)
	} else {
		for _, d := range res.Diagnostics {
			loc := ""
			if d.Path != "" {
				loc = " at " + d.Path
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%-7s %s%s: %s\n", d.Severity, d.Code, loc, d.Message)
		}
	}
	if issues.HasErrors() {
		return &exitError{code: ExitFailure}
	}
	return nil
}

// schemasOf lists the source schemas a mapping reads.
func schemasOf(m *mapping.Mapping) []string {
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

// buildPlan inspects the source and plans the mapping. It returns the
// source endpoint information too.
func buildPlan(ctx context.Context, cmd *cobra.Command, opts *options, m *mapping.Mapping, src *postgresql.Adapter) (*planning.Plan, runstate.Endpoint, error) {
	doc, err := src.Inspect(ctx, relational.InspectOptions{Schemas: schemasOf(m)})
	if err != nil {
		return nil, runstate.Endpoint{}, reportError(cmd, opts, err)
	}
	info := runstate.Endpoint{Product: doc.Database.Product, Version: doc.Database.Version, Database: doc.Database.Name}
	p, issues := planning.Build(planning.Input{Mapping: m, Source: doc, Destination: neo4j.TypeSupport{}})
	if issues.HasErrors() {
		return nil, info, printIssues(cmd, opts, issues)
	}
	if opts.format == "text" {
		_ = printIssues(cmd, opts, issues) // warnings only
	}
	return p, info, nil
}

// ---- plan ------------------------------------------------------------------

func newPlanCmd(opts *options) *cobra.Command {
	var (
		mappingPath string
		conn        connFlags
		timeout     time.Duration
	)
	cmd := &cobra.Command{
		Use:   "plan --mapping FILE --source-env VAR",
		Short: "Show what a migration would do, without writing anything",
		Long: `Inspect the source, validate the mapping against it, and print the
execution plan: steps in order, lossy transformations and what allowed them,
and warnings. --format json prints the plan as RFC 8785 canonical JSON
(specs/plan.schema.json); the same inputs always give identical bytes.

Planning reads the source catalog only and never connects to the destination.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := loadMapping(cmd, opts, mappingPath)
			if err != nil {
				return err
			}
			srcURL, err := urlFromEnv(conn.sourceEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			ctx, stop := interruptible(cmd)
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			src, err := postgresql.Open(ctx, srcURL)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			defer func() { _ = src.Close() }()

			p, _, err := buildPlan(ctx, cmd, opts, m, src)
			if err != nil {
				return err
			}
			if opts.format == "json" {
				b, err := p.Canonical()
				if err != nil {
					return reportError(cmd, opts, err)
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", b)
				return err
			}
			_, err = io.WriteString(cmd.OutOrStdout(), p.Text())
			return err
		},
	}
	cmd.Flags().StringVar(&mappingPath, "mapping", "", "mapping file (.json, .yaml or .yml)")
	_ = cmd.MarkFlagRequired("mapping")
	conn.register(cmd, false)
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "time limit for connecting and inspecting")
	return cmd
}

// ---- migrate -----------------------------------------------------------------

func newMigrateCmd(opts *options) *cobra.Command {
	var (
		mappingPath string
		conn        connFlags
		batchSize   int
		dryRun      bool
		resume      string
	)
	cmd := &cobra.Command{
		Use:   "migrate --mapping FILE --source-env VAR --dest-env VAR",
		Short: "Run a migration (or a dry run), resumably",
		Long: `Migrate data as planned. Each batch is written in one destination
transaction and checkpointed after it commits; writes are idempotent, so an
interrupted run can be resumed with --resume RUN_ID without duplicates.

--dry-run reads and converts every row and reports what would be written,
but writes nothing to the destination (not even constraints). A dry run does
not need --dest-env.

Exit status: 0 succeeded, 3 finished with failed records (see
'rtogtor failures'), 1 failed or interrupted (resumable), 2 usage error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := loadMapping(cmd, opts, mappingPath)
			if err != nil {
				return err
			}
			if conn.destEnv == "" && !dryRun {
				return fmt.Errorf("--dest-env is required unless --dry-run is set")
			}
			store := &runstate.Store{Dir: conn.stateDir}
			srcURL, err := urlFromEnv(conn.sourceEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			ctx, stop := interruptible(cmd)
			defer stop()

			src, err := postgresql.Open(ctx, srcURL)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			defer func() { _ = src.Close() }()

			hash, err := m.Hash()
			if err != nil {
				return reportError(cmd, opts, err)
			}
			mappingJSON, err := canonjson.Marshal(m)
			if err != nil {
				return reportError(cmd, opts, err)
			}

			var plan *planning.Plan
			var srcInfo runstate.Endpoint
			if resume != "" {
				// A resumed run continues the plan its checkpoints refer to.
				b, err := store.ReadFile(resume, "plan.json")
				if err != nil {
					return reportError(cmd, opts, err)
				}
				plan = &planning.Plan{}
				if err := json.Unmarshal(b, plan); err != nil {
					return reportError(cmd, opts, rtgerr.New(rtgerr.RunStateCorrupt, "plan of run %s is unreadable: %v", resume, err))
				}
				if plan.Mapping.Hash != hash {
					return reportError(cmd, opts, rtgerr.New(rtgerr.RunMappingChanged,
						"run %s was started with a different mapping (hash %s, this file %s); resume with the original mapping", resume, plan.Mapping.Hash[:12], hash[:12]))
				}
			} else {
				plan, srcInfo, err = buildPlan(ctx, cmd, opts, m, src)
				if err != nil {
					return err
				}
			}

			var dst execution.Destination = noDestination{}
			var dstInfo = runstate.Endpoint{Product: neo4j.Product, Database: plan.Destination.Database}
			if conn.destEnv != "" {
				dstURL, err := destURLFromEnv(conn.destEnv)
				if err != nil {
					return reportError(cmd, opts, err)
				}
				g, err := neo4j.Open(ctx, dstURL, plan.Destination.Database)
				if err != nil {
					return reportError(cmd, opts, err)
				}
				defer func() { _ = g.Close() }()
				info, err := g.ServerInfo(ctx)
				if err != nil {
					return reportError(cmd, opts, err)
				}
				dstInfo = runstate.Endpoint{Product: info.Product, Version: info.Version, Database: info.Database}
				dst = g
			}

			progress := func(p execution.Progress) {}
			if opts.format == "text" {
				progress = func(p execution.Progress) {
					state := "batch"
					if p.Done {
						state = "done "
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "%s %-40s read %-9d written %-9d failed %d\n", state, p.StepID, p.Read, p.Written, p.Failed)
				}
			}
			st, runErr := execution.Run(ctx, execution.Options{
				Plan: plan, MappingJSON: mappingJSON, Source: src, Destination: dst,
				SourceInfo: srcInfo, DestinationInfo: dstInfo, Store: store,
				BatchSize: batchSize, DryRun: dryRun, ResumeRunID: resume, Progress: progress,
			})
			if st == nil {
				return reportError(cmd, opts, runErr)
			}
			if err := printReport(cmd, opts, store, st.RunID); err != nil {
				return err
			}
			if runErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "\nResume with: rtogtor migrate --mapping %s --resume %s ...\n", mappingPath, st.RunID)
				return reportError(cmd, opts, runErr)
			}
			if st.Status == runstate.StatusSucceededWithFailures {
				return &exitError{code: ExitPartial}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&mappingPath, "mapping", "", "mapping file (.json, .yaml or .yml)")
	_ = cmd.MarkFlagRequired("mapping")
	conn.register(cmd, true)
	registerStateDir(cmd, &conn.stateDir)
	cmd.Flags().IntVar(&batchSize, "batch-size", execution.DefaultBatchSize, "rows per batch (ignored when resuming: the run's batch size is kept)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "read and convert everything, write nothing")
	cmd.Flags().StringVar(&resume, "resume", "", "resume the run with this ID")
	return cmd
}

// noDestination is used by dry runs without a destination connection. The
// executor never writes in a dry run; encoding uses Neo4j's rules so
// conversion failures are still found.
type noDestination struct{}

func (noDestination) EnsureIdentityConstraint(context.Context, string, string) error {
	return rtgerr.New(rtgerr.Internal, "dry run attempted a write")
}

func (noDestination) EncodeProperties(props []graph.Property) (graph.Encoded, error) {
	return neo4j.EncodeProperties(props)
}

func (noDestination) MergeNodes(context.Context, graph.NodeBatch) error {
	return rtgerr.New(rtgerr.Internal, "dry run attempted a write")
}

func (noDestination) MergeRelationships(context.Context, graph.RelationshipBatch) ([]int, error) {
	return nil, rtgerr.New(rtgerr.Internal, "dry run attempted a write")
}

func printReport(cmd *cobra.Command, opts *options, store *runstate.Store, runID string) error {
	b, err := store.ReadFile(runID, "report.json")
	if err != nil {
		return reportError(cmd, opts, err)
	}
	if opts.format == "json" {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n", b)
		return err
	}
	var rep reporting.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return reportError(cmd, opts, rtgerr.New(rtgerr.RunStateCorrupt, "report of run %s is unreadable: %v", runID, err))
	}
	_, err = io.WriteString(cmd.OutOrStdout(), rep.Text())
	return err
}

// ---- verify ----------------------------------------------------------------

func newVerifyCmd(opts *options) *cobra.Command {
	var (
		runID string
		conn  connFlags
	)
	cmd := &cobra.Command{
		Use:   "verify --run-id RUN_ID --source-env VAR --dest-env VAR",
		Short: "Compare a run's source and destination",
		Long: `Verify a run: for every entity, check that each source row's identity has a
node; compare node counts per label and relationship counts per type with the
source, and report dangling source references. The result is added to the
run's report.

Exit status: 0 passed, 1 discrepancies found or verification failed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store := &runstate.Store{Dir: conn.stateDir}
			st, err := store.Load(runID)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			if st.DryRun {
				return fmt.Errorf("run %s was a dry run; nothing was written, so there is nothing to verify", runID)
			}
			b, err := store.ReadFile(runID, "plan.json")
			if err != nil {
				return reportError(cmd, opts, err)
			}
			var plan planning.Plan
			if err := json.Unmarshal(b, &plan); err != nil {
				return reportError(cmd, opts, rtgerr.New(rtgerr.RunStateCorrupt, "plan of run %s is unreadable: %v", runID, err))
			}
			if conn.destEnv == "" {
				return fmt.Errorf("--dest-env is required")
			}
			srcURL, err := urlFromEnv(conn.sourceEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			dstURL, err := destURLFromEnv(conn.destEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			ctx, stop := interruptible(cmd)
			defer stop()
			src, err := postgresql.Open(ctx, srcURL)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			defer func() { _ = src.Close() }()
			dst, err := neo4j.Open(ctx, dstURL, plan.Destination.Database)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			defer func() { _ = dst.Close() }()

			v, err := verification.Verify(ctx, &plan, src, dst, st.BatchSize)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			rb, err := store.ReadFile(runID, "report.json")
			if err != nil {
				return reportError(cmd, opts, err)
			}
			var rep reporting.Report
			if err := json.Unmarshal(rb, &rep); err != nil {
				return reportError(cmd, opts, rtgerr.New(rtgerr.RunStateCorrupt, "report of run %s is unreadable: %v", runID, err))
			}
			rep.Verification = v
			out, err := canonjson.Marshal(&rep)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			if err := store.WriteFile(runID, "report.json", out); err != nil {
				return reportError(cmd, opts, err)
			}
			if opts.format == "json" {
				if err := writeJSON(cmd.OutOrStdout(), v); err != nil {
					return err
				}
			} else {
				_, _ = io.WriteString(cmd.OutOrStdout(), (&reporting.Report{RunID: runID, Verification: v}).VerificationText())
			}
			if v.Status != "passed" {
				return &exitError{code: ExitFailure}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "run to verify")
	_ = cmd.MarkFlagRequired("run-id")
	conn.register(cmd, true)
	registerStateDir(cmd, &conn.stateDir)
	return cmd
}

// ---- report and failures -----------------------------------------------------

func newReportCmd(opts *options) *cobra.Command {
	var runID, stateDir string
	cmd := &cobra.Command{
		Use:   "report --run-id RUN_ID",
		Short: "Print a run's report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return printReport(cmd, opts, &runstate.Store{Dir: stateDir}, runID)
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "run to report")
	_ = cmd.MarkFlagRequired("run-id")
	registerStateDir(cmd, &stateDir)
	return cmd
}

func newFailuresCmd(opts *options) *cobra.Command {
	var runID, stateDir string
	cmd := &cobra.Command{
		Use:   "failures --run-id RUN_ID",
		Short: "List a run's failed records",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store := &runstate.Store{Dir: stateDir}
			st, err := store.Load(runID)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			failures, err := store.Failures(st)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			if opts.format == "json" {
				return writeJSON(cmd.OutOrStdout(), failures)
			}
			out := cmd.OutOrStdout()
			if len(failures) == 0 {
				fmt.Fprintln(out, "No failed records.")
				return nil
			}
			for _, f := range failures {
				key := make([]string, len(f.Key))
				for i, k := range f.Key {
					key[i] = k.Column + "=" + k.Value
				}
				where := strings.Join(key, ", ")
				if where == "" {
					where = "(aggregate)"
				}
				fmt.Fprintf(out, "%s  %-34s %s  %s\n", f.Code, f.StepID, where, f.Message)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "run whose failed records to list")
	_ = cmd.MarkFlagRequired("run-id")
	registerStateDir(cmd, &stateDir)
	return cmd
}
