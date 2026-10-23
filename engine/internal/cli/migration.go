package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	rtogtor "github.com/TuanFaied/rtogtor/engine"
	"github.com/TuanFaied/rtogtor/engine/internal/canonjson"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/reporting"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/runstate"
)

// ExitPartial: the migration finished but some records failed.
const ExitPartial = 3

func registerStateDir(cmd *cobra.Command, dir *string) {
	cmd.Flags().StringVar(dir, "state-dir", runstate.DefaultDir, "directory holding run state, failed records and reports")
}

// interruptible returns a context cancelled by Ctrl-C.
func interruptible(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(cmd.Context(), os.Interrupt)
}

// connFromEnv returns a connection from the URL in an environment variable.
// The URL scheme selects the product: postgres:// or postgresql:// for
// PostgreSQL; neo4j://, neo4j+s://, bolt:// or bolt+s:// for Neo4j. The
// variable is validated before any connection attempt.
func connFromEnv(name string) (rtogtor.Connection, error) {
	v, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(v) == "" {
		return rtogtor.Connection{}, rtgerr.New(rtgerr.ConnConfig, "environment variable %s is not set or is empty", name)
	}
	switch {
	case strings.HasPrefix(v, "postgres://"), strings.HasPrefix(v, "postgresql://"):
		return rtogtor.PostgreSQLFromEnv(name), nil
	case strings.HasPrefix(v, "neo4j://"), strings.HasPrefix(v, "neo4j+s://"), strings.HasPrefix(v, "neo4j+ssc://"),
		strings.HasPrefix(v, "bolt://"), strings.HasPrefix(v, "bolt+s://"), strings.HasPrefix(v, "bolt+ssc://"):
		return rtogtor.Neo4jFromEnv(name), nil
	}
	return rtogtor.Connection{}, rtgerr.New(rtgerr.ConnConfig,
		"environment variable %s must hold a postgres:// or postgresql:// URL, or a neo4j:// or bolt:// URL", name)
}

// optionalConn is connFromEnv for an optional flag.
func optionalConn(name string) (rtogtor.Connection, error) {
	if name == "" {
		return rtogtor.Connection{}, nil
	}
	return connFromEnv(name)
}

const (
	sourceEnvHelp = "name of the environment variable holding the source URL (PostgreSQL, or Neo4j for graph-to-relational mappings)"
	destEnvHelp   = "name of the environment variable holding the destination URL (Neo4j, or PostgreSQL for graph-to-relational mappings)"
)

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

// fail reports err, printing every problem when it is a list.
func fail(cmd *cobra.Command, opts *options, err error) error {
	var list rtgerr.List
	if errors.As(err, &list) {
		return printIssues(cmd, opts, list)
	}
	return reportError(cmd, opts, err)
}

// ---- plan ------------------------------------------------------------------

func newPlanCmd(opts *options) *cobra.Command {
	var (
		mappingPath, sourceEnv, destEnv string
		timeout                         time.Duration
	)
	cmd := &cobra.Command{
		Use:   "plan --mapping FILE --source-env VAR [--dest-env VAR]",
		Short: "Show what a migration would do, without writing anything",
		Long: `Inspect the source, validate the mapping against it, and print the
execution plan: steps in order, lossy transformations and what allowed them,
and warnings. --format json prints the plan as RFC 8785 canonical JSON
(specs/plan.schema.json); the same inputs always give identical bytes.

Planning only reads catalogs and never writes. Relational-to-graph planning
reads the source only. Graph-to-relational planning also inspects the
destination (--dest-env), to check the tables that already exist.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := loadMapping(cmd, opts, mappingPath)
			if err != nil {
				return err
			}
			src, err := connFromEnv(sourceEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			var dst rtogtor.Connection
			if m.Direction == mapping.GraphToRelational {
				if destEnv == "" {
					return fmt.Errorf("--dest-env is required for graph-to-relational mappings: planning inspects the destination's tables")
				}
				if dst, err = connFromEnv(destEnv); err != nil {
					return reportError(cmd, opts, err)
				}
			}
			ctx, stop := interruptible(cmd)
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			mig := rtogtor.NewMigration(src, dst, m)
			defer func() { _ = mig.Close() }()
			p, err := mig.Plan(ctx)
			if err != nil {
				return fail(cmd, opts, err)
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
	cmd.Flags().StringVar(&sourceEnv, "source-env", "", sourceEnvHelp)
	_ = cmd.MarkFlagRequired("source-env")
	cmd.Flags().StringVar(&destEnv, "dest-env", "", destEnvHelp+"; needed for graph-to-relational mappings")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "time limit for connecting and inspecting")
	return cmd
}

// ---- migrate ---------------------------------------------------------------

func newMigrateCmd(opts *options) *cobra.Command {
	var (
		mappingPath, sourceEnv, destEnv, stateDir, resume string
		batchSize                                         int
		dryRun                                            bool
	)
	cmd := &cobra.Command{
		Use:   "migrate --mapping FILE --source-env VAR --dest-env VAR",
		Short: "Run a migration (or a dry run), resumably",
		Long: `Migrate data as planned. Each batch is written in one destination
transaction and checkpointed after it commits; writes are idempotent, so an
interrupted run can be resumed with --resume RUN_ID without duplicates.

--dry-run reads and converts every record and reports what would be
written, but writes nothing to the destination (not even constraints or
tables). A relational-to-graph dry run does not need --dest-env; a
graph-to-relational one does, because planning inspects the destination.

The direction comes from the mapping: PostgreSQL → Neo4j for
relational-to-graph, Neo4j → PostgreSQL for graph-to-relational. A Neo4j URL
may carry credentials: neo4j://user:password@host:7687.

Exit status: 0 succeeded, 3 finished with failed records (see
'rtogtor failures'), 1 failed or interrupted (resumable), 2 usage error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := loadMapping(cmd, opts, mappingPath)
			if err != nil {
				return err
			}
			if destEnv == "" && (!dryRun || m.Direction == mapping.GraphToRelational) {
				return fmt.Errorf("--dest-env is required unless --dry-run is set for a relational-to-graph mapping")
			}
			src, err := connFromEnv(sourceEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			dst, err := optionalConn(destEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			ctx, stop := interruptible(cmd)
			defer stop()

			var progress func(rtogtor.Progress)
			if opts.format == "text" {
				progress = func(p rtogtor.Progress) {
					state := "batch"
					if p.Done {
						state = "done "
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "%s %-40s read %-9d written %-9d failed %d\n", state, p.StepID, p.Read, p.Written, p.Failed)
				}
			}
			mig := rtogtor.NewMigration(src, dst, m)
			defer func() { _ = mig.Close() }()
			rep, runErr := mig.Run(ctx, rtogtor.RunOptions{
				DryRun: dryRun, BatchSize: batchSize, ResumeRunID: resume, StateDir: stateDir, Progress: progress,
			})
			if rep == nil {
				return fail(cmd, opts, runErr)
			}
			if err := printReport(cmd, opts, rep); err != nil {
				return err
			}
			if runErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "\nResume with: rtogtor migrate --mapping %s --resume %s ...\n", mappingPath, rep.RunID)
				return reportError(cmd, opts, runErr)
			}
			if rep.Status == runstate.StatusSucceededWithFailures {
				return &exitError{code: ExitPartial}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&mappingPath, "mapping", "", "mapping file (.json, .yaml or .yml)")
	_ = cmd.MarkFlagRequired("mapping")
	cmd.Flags().StringVar(&sourceEnv, "source-env", "", sourceEnvHelp)
	_ = cmd.MarkFlagRequired("source-env")
	cmd.Flags().StringVar(&destEnv, "dest-env", "", destEnvHelp)
	registerStateDir(cmd, &stateDir)
	cmd.Flags().IntVar(&batchSize, "batch-size", 1000, "records per batch (ignored when resuming: the run's batch size is kept)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "read and convert everything, write nothing")
	cmd.Flags().StringVar(&resume, "resume", "", "resume the run with this ID")
	return cmd
}

func printReport(cmd *cobra.Command, opts *options, rep *reporting.Report) error {
	if opts.format == "json" {
		b, err := canonjson.Marshal(rep)
		if err != nil {
			return reportError(cmd, opts, err)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", b)
		return err
	}
	_, err := io.WriteString(cmd.OutOrStdout(), rep.Text())
	return err
}

// ---- verify ----------------------------------------------------------------

func newVerifyCmd(opts *options) *cobra.Command {
	var runID, sourceEnv, destEnv, stateDir string
	cmd := &cobra.Command{
		Use:   "verify --run-id RUN_ID --source-env VAR --dest-env VAR",
		Short: "Compare a run's source and destination",
		Long: `Verify a run. Relational to graph: for every entity, check that each source
row's identity has a node; compare node counts per label and relationship
counts per type with the source, and report dangling source references.
Graph to relational: check that each keyed node has a row, compare row counts
with the distinct node keys, and compare filled foreign keys with the
relationships that define them. The result is added to the run's report.

Exit status: 0 passed, 1 discrepancies found or verification failed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := connFromEnv(sourceEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			dst, err := connFromEnv(destEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			ctx, stop := interruptible(cmd)
			defer stop()
			mig := rtogtor.NewMigration(src, dst, nil)
			defer func() { _ = mig.Close() }()
			v, err := mig.Verify(ctx, runID, stateDir)
			if err != nil {
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
	cmd.Flags().StringVar(&sourceEnv, "source-env", "", sourceEnvHelp)
	_ = cmd.MarkFlagRequired("source-env")
	cmd.Flags().StringVar(&destEnv, "dest-env", "", destEnvHelp)
	_ = cmd.MarkFlagRequired("dest-env")
	registerStateDir(cmd, &stateDir)
	return cmd
}

// ---- report and failures ---------------------------------------------------

func newReportCmd(opts *options) *cobra.Command {
	var runID, stateDir string
	cmd := &cobra.Command{
		Use:   "report --run-id RUN_ID",
		Short: "Print a run's report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, err := rtogtor.LoadReport(stateDir, runID)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			return printReport(cmd, opts, rep)
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
			failures, err := rtogtor.LoadFailures(stateDir, runID)
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
