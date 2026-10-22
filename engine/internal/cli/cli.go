// Package cli implements the rtogtor command-line interface.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	rtogtor "github.com/TuanFaied/rtogtor/engine"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// Exit codes.
const (
	ExitOK      = 0 // success
	ExitFailure = 1 // the command ran and reported a failure (e.g. invalid mapping)
	ExitUsage   = 2 // invalid command-line usage
)

// exitError ends a command with a specific exit code after its output has
// already been written.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

type options struct {
	format string
}

// Run executes the CLI with args (without the program name) and returns the
// process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	opts := &options{}
	root := newRoot(opts)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	// Anything else comes from flag parsing or argument validation.
	fmt.Fprintf(stderr, "error %s: %v\nRun 'rtogtor --help' for usage.\n", rtgerr.CLIUsage, err)
	return ExitUsage
}

func newRoot(opts *options) *cobra.Command {
	root := &cobra.Command{
		Use:   "rtogtor",
		Short: "Bidirectional relational ↔ graph migration engine",
		Long: `RtoGtoR moves data between relational and graph databases using explicit,
validated mappings.

Status: pre-alpha. Available commands are listed below; migration commands
arrive in later milestones (see docs/roadmap.md).`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if opts.format != "text" && opts.format != "json" {
				return fmt.Errorf(`invalid --format %q: use "text" or "json"`, opts.format)
			}
			return nil
		},
	}
	root.PersistentFlags().StringVar(&opts.format, "format", "text", `output format: "text" or "json"`)
	root.AddCommand(newVersionCmd(opts), newValidateCmd(opts), newInspectCmd(opts),
		newPlanCmd(opts), newMigrateCmd(opts), newVerifyCmd(opts), newReportCmd(opts), newFailuresCmd(opts), newServeCmd(opts))
	return root
}

// reportError prints an engine error and ends the command with ExitFailure.
func reportError(cmd *cobra.Command, opts *options, err error) error {
	var e *rtgerr.Error
	if !errors.As(err, &e) {
		switch {
		case errors.Is(err, context.Canceled):
			e = rtgerr.New(rtgerr.Internal, "cancelled")
		case errors.Is(err, context.DeadlineExceeded):
			e = rtgerr.New(rtgerr.ConnFailed, "timed out; raise --timeout or check that the server is reachable")
		default:
			e = rtgerr.New(rtgerr.Internal, "%v", err)
		}
	}
	d := diagnostic{Code: e.Code, Severity: e.Severity(), Retryable: e.Retryable(), Path: e.Path, Message: e.Message}
	if opts.format == "json" {
		_ = writeJSON(cmd.OutOrStdout(), struct {
			Error diagnostic `json:"error"`
		}{d})
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "error %s: %s\n", d.Code, d.Message)
	}
	return &exitError{code: ExitFailure}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ---- version ---------------------------------------------------------------

type versionInfo struct {
	EngineVersion     string   `json:"engineVersion"`
	SpecVersion       string   `json:"specVersion"`
	IdentityAlgorithm string   `json:"identityAlgorithm"`
	ProtocolVersions  []string `json:"protocolVersions"`
	Commit            string   `json:"commit,omitempty"`
	Modified          bool     `json:"modified,omitempty"`
	GoVersion         string   `json:"goVersion"`
}

func currentVersion() versionInfo {
	v := versionInfo{
		EngineVersion:     rtogtor.EngineVersion,
		SpecVersion:       rtogtor.SpecVersion,
		IdentityAlgorithm: rtogtor.IdentityAlgorithm,
		ProtocolVersions:  []string{spec.ProtocolVersion},
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		v.GoVersion = info.GoVersion
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				v.Commit = s.Value
			case "vcs.modified":
				v.Modified = s.Value == "true"
			}
		}
	}
	return v
}

func newVersionCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print engine, specification and protocol versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			v := currentVersion()
			out := cmd.OutOrStdout()
			if opts.format == "json" {
				return writeJSON(out, v)
			}
			fmt.Fprintf(out, "rtogtor engine     %s\n", v.EngineVersion)
			fmt.Fprintf(out, "specification      %s\n", v.SpecVersion)
			fmt.Fprintf(out, "identity algorithm %s\n", v.IdentityAlgorithm)
			fmt.Fprintf(out, "protocol           %s\n", strings.Join(v.ProtocolVersions, ", "))
			if v.Commit != "" {
				suffix := ""
				if v.Modified {
					suffix = " (modified)"
				}
				fmt.Fprintf(out, "commit             %s%s\n", v.Commit, suffix)
			}
			fmt.Fprintf(out, "go                 %s\n", v.GoVersion)
			return nil
		},
	}
}

// ---- validate --------------------------------------------------------------

type diagnostic struct {
	Code      rtgerr.Code     `json:"code"`
	Severity  rtgerr.Severity `json:"severity"`
	Retryable bool            `json:"retryable"`
	Path      string          `json:"path"`
	Message   string          `json:"message"`
}

type validateResult struct {
	Valid       bool         `json:"valid"`
	Mapping     string       `json:"mapping,omitempty"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

func newValidateCmd(opts *options) *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "validate --mapping FILE",
		Short: "Validate a mapping file (JSON or YAML) without connecting to any database",
		Long: `Validate a mapping file against the specification: syntax, schema and
semantic rules (unique names, key coverage, foreign-key arity, identity types).

Exit status: 0 if the mapping is valid (warnings allowed), 1 if it is
invalid, 2 for usage errors.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, issues := mapping.LoadFile(path)
			res := validateResult{Valid: !issues.HasErrors(), Diagnostics: []diagnostic{}}
			if m != nil {
				res.Mapping = m.Name
			}
			for _, e := range issues {
				res.Diagnostics = append(res.Diagnostics, diagnostic{
					Code: e.Code, Severity: e.Severity(), Retryable: e.Retryable(),
					Path: e.Path, Message: e.Message,
				})
			}

			out := cmd.OutOrStdout()
			if opts.format == "json" {
				if err := writeJSON(out, res); err != nil {
					return err
				}
			} else {
				printDiagnostics(out, res)
			}
			if !res.Valid {
				return &exitError{code: ExitFailure}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "mapping", "", "mapping file (.json, .yaml or .yml)")
	_ = cmd.MarkFlagRequired("mapping")
	return cmd
}

func printDiagnostics(out io.Writer, res validateResult) {
	var errs, warns int
	for _, d := range res.Diagnostics {
		loc := ""
		if d.Path != "" {
			loc = " at " + d.Path
		}
		fmt.Fprintf(out, "%-7s %s%s: %s\n", d.Severity, d.Code, loc, d.Message)
		switch d.Severity {
		case rtgerr.SeverityWarning:
			warns++
		case rtgerr.SeverityError, rtgerr.SeverityFatal:
			errs++
		}
	}
	if res.Valid {
		fmt.Fprintf(out, "OK: mapping %q is valid (%s)\n", res.Mapping, plural(warns, "warning"))
	} else {
		fmt.Fprintf(out, "FAILED: mapping is invalid (%s, %s)\n", plural(errs, "error"), plural(warns, "warning"))
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
