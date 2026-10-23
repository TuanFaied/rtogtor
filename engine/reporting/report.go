// Package reporting builds migration reports (specs/report.schema.json) and
// renders them as text.
package reporting

import (
	"fmt"
	"sort"
	"strings"

	"github.com/TuanFaied/rtogtor/engine/planning"
	"github.com/TuanFaied/rtogtor/engine/runstate"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// Report summarizes a run and, once verified, its verification.
type Report struct {
	SpecVersion     string                    `json:"specVersion"`
	RunID           string                    `json:"runId"`
	Mapping         runstate.MappingRef       `json:"mapping"`
	DryRun          bool                      `json:"dryRun"`
	Status          runstate.Status           `json:"status"`
	StartedAt       string                    `json:"startedAt"`
	CompletedAt     string                    `json:"completedAt,omitempty"`
	Attempts        int                       `json:"attempts"`
	BatchSize       int                       `json:"batchSize"`
	Source          runstate.Endpoint         `json:"source"`
	Destination     runstate.Endpoint         `json:"destination"`
	Steps           []StepReport              `json:"steps"`
	Totals          Totals                    `json:"totals"`
	Transformations []planning.Transformation `json:"transformations"`
	Failures        FailureSummary            `json:"failures"`
	Warnings        []planning.Diagnostic     `json:"warnings"`
	LastError       *runstate.ErrorInfo       `json:"lastError,omitempty"`
	Verification    *Verification             `json:"verification,omitempty"`
}

// StepReport holds the counts of one load step.
type StepReport struct {
	StepID   string `json:"stepId"`
	Kind     string `json:"kind"`
	Done     bool   `json:"done"`
	Read     int64  `json:"read"`
	Written  int64  `json:"written"`
	Failed   int64  `json:"failed"`
	Skipped  int64  `json:"skipped"`
	NullKeys int64  `json:"nullKeys"`
}

// Totals sums the step counts.
type Totals struct {
	Read    int64 `json:"read"`
	Written int64 `json:"written"`
	Failed  int64 `json:"failed"`
	Skipped int64 `json:"skipped"`
}

// FailureSummary counts failed records by error code.
type FailureSummary struct {
	Total  int64       `json:"total"`
	ByCode []CodeCount `json:"byCode"`
}

// CodeCount is a count for one error code.
type CodeCount struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

// Verification is the result of comparing source and destination.
type Verification struct {
	Status        string                `json:"status"` // passed | failed
	CheckedAt     string                `json:"checkedAt"`
	Entities      []EntityCheck         `json:"entities"`
	Relationships []RelationshipCheck   `json:"relationships"`
	Issues        []planning.Diagnostic `json:"issues"`
}

// EntityCheck compares one entity's rows and nodes.
type EntityCheck struct {
	Entity string `json:"entity"`
	Label  string `json:"label"`
	// SourceCount counts source records (rows or nodes) with a non-null key.
	SourceCount int64 `json:"sourceCount"`
	// DestinationCount counts destination records (nodes or rows).
	DestinationCount int64 `json:"destinationCount"`
	// Missing counts source rows whose identity has no node.
	Missing int64 `json:"missing"`
	// MissingKeys lists up to 10 missing rows' keys (canonical text).
	MissingKeys [][]string `json:"missingKeys"`
}

// RelationshipCheck compares the relationships of one type between two
// labels. Several mapped relationships may share a check.
type RelationshipCheck struct {
	Type          string   `json:"type"`
	FromLabel     string   `json:"fromLabel"`
	ToLabel       string   `json:"toLabel"`
	Relationships []string `json:"relationships"`
	Expected      int64    `json:"expected"`
	Actual        int64    `json:"actual"`
	Dangling      int64    `json:"dangling"`
}

// Build assembles a report from a run's state, plan and failures.
func Build(st *runstate.State, plan *planning.Plan, failures []runstate.Failure) *Report {
	r := &Report{
		SpecVersion: spec.Version, RunID: st.RunID, Mapping: st.Mapping, DryRun: st.DryRun,
		Status: st.Status, StartedAt: st.StartedAt, CompletedAt: st.CompletedAt,
		Attempts: st.Attempts, BatchSize: st.BatchSize, Source: st.Source, Destination: st.Destination,
		Steps: []StepReport{}, Transformations: plan.Transformations, Warnings: plan.Warnings,
		Failures: FailureSummary{ByCode: []CodeCount{}}, LastError: st.LastError,
	}
	for _, s := range st.Streams {
		r.Steps = append(r.Steps, StepReport{
			StepID: s.StepID, Kind: s.Kind, Done: s.Done,
			Read: s.Read, Written: s.Written, Failed: s.Failed, Skipped: s.Skipped, NullKeys: s.NullKeys,
		})
		r.Totals.Read += s.Read
		r.Totals.Written += s.Written
		r.Totals.Failed += s.Failed
		r.Totals.Skipped += s.Skipped
	}
	byCode := map[string]int64{}
	for _, f := range failures {
		if f.Batch == runstate.AggregateBatch {
			continue // aggregate entries stand for many rows; NullKeys counts them below
		}
		byCode[f.Code]++
	}
	for _, s := range st.Streams {
		if s.NullKeys > 0 {
			byCode["RTG-ID-001"] += s.NullKeys
		}
	}
	for code, n := range byCode {
		r.Failures.ByCode = append(r.Failures.ByCode, CodeCount{Code: code, Count: n})
		r.Failures.Total += n
	}
	sort.Slice(r.Failures.ByCode, func(i, j int) bool { return r.Failures.ByCode[i].Code < r.Failures.ByCode[j].Code })
	return r
}

// Text renders the report for humans.
func (r *Report) Text() string {
	var sb strings.Builder
	mode := ""
	if r.DryRun {
		mode = " (dry run: nothing was written)"
	}
	fmt.Fprintf(&sb, "Run %s: %s%s\n", r.RunID, r.Status, mode)
	fmt.Fprintf(&sb, "Mapping %q, %s %s -> %s %s, batch size %d, attempts %d\n",
		r.Mapping.Name, r.Source.Product, r.Source.Version, r.Destination.Product, r.Destination.Version, r.BatchSize, r.Attempts)
	fmt.Fprintf(&sb, "\n%-42s %10s %10s %8s %8s\n", "step", "read", "written", "failed", "skipped")
	for _, s := range r.Steps {
		done := ""
		if !s.Done {
			done = " (incomplete)"
		}
		fmt.Fprintf(&sb, "%-42s %10d %10d %8d %8d%s\n", s.StepID, s.Read, s.Written, s.Failed+s.NullKeys, s.Skipped, done)
	}
	fmt.Fprintf(&sb, "%-42s %10d %10d %8d %8d\n", "total", r.Totals.Read, r.Totals.Written, r.Failures.Total, r.Totals.Skipped)
	if r.Failures.Total > 0 {
		sb.WriteString("\nFailed records by code (see `rtogtor failures --run-id " + r.RunID + "`):\n")
		for _, c := range r.Failures.ByCode {
			fmt.Fprintf(&sb, "  %s: %d\n", c.Code, c.Count)
		}
	}
	if len(r.Transformations) > 0 {
		sb.WriteString("\nLossy transformations applied:\n")
		for _, t := range r.Transformations {
			owner := t.Entity
			if t.Relationship != "" {
				owner = t.Relationship
			}
			fmt.Fprintf(&sb, "  %s.%s: %s stored as %s (%s)\n", owner, t.Property, t.Type, t.StoredAs, t.LossKind)
		}
	}
	if r.LastError != nil {
		fmt.Fprintf(&sb, "\nLast error %s: %s\n", r.LastError.Code, r.LastError.Message)
	}
	if r.Verification != nil {
		sb.WriteString("\n")
		sb.WriteString(r.VerificationText())
	}
	return sb.String()
}

// VerificationText renders the verification section.
func (r *Report) VerificationText() string {
	var sb strings.Builder
	if v := r.Verification; v != nil {
		fmt.Fprintf(&sb, "Verification %s at %s\n", v.Status, v.CheckedAt)
		for _, e := range v.Entities {
			fmt.Fprintf(&sb, "  %-20s source %-8d destination %-8d missing %d\n", e.Entity, e.SourceCount, e.DestinationCount, e.Missing)
		}
		for _, rc := range v.Relationships {
			fmt.Fprintf(&sb, "  (:%s)-[:%s]->(:%s) expected %d, found %d, dangling in source %d\n",
				rc.FromLabel, rc.Type, rc.ToLabel, rc.Expected, rc.Actual, rc.Dangling)
		}
		for _, i := range v.Issues {
			fmt.Fprintf(&sb, "  %s at %s: %s\n", i.Code, i.Path, i.Message)
		}
	}
	return sb.String()
}
