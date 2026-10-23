// Package execution runs plans: it reads the source in key order and in
// batches, converts rows to identities and properties, writes each batch to
// the destination in one transaction, and checkpoints after every commit.
//
// Guarantees (see docs/recovery.md):
//
//   - Each batch is atomic in the destination.
//   - The checkpoint and the batch's failed records are recorded only after
//     the batch commits, so a crash replays at most the uncommitted batch.
//   - Writes are idempotent (MERGE on rtg_id; INSERT … ON CONFLICT and
//     UPDATE by key for graph → relational), so replaying is safe: a run
//     is at-least-once with effectively-once results.
//   - Memory is bounded: the reader runs at most two batches ahead.
package execution

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/internal/canonjson"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/planning"
	"github.com/TuanFaied/rtogtor/engine/reporting"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/runstate"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// Source is the part of a relational adapter the executor uses.
type Source interface {
	ReadBatch(ctx context.Context, req relational.ReadRequest) ([]relational.Row, error)
	CountNullKeys(ctx context.Context, table relational.Table, key []string) (int64, error)
}

// Destination is the part of a graph adapter the executor uses.
type Destination interface {
	EnsureIdentityConstraint(ctx context.Context, label, property string) error
	EncodeProperties(props []graph.Property) (graph.Encoded, error)
	MergeNodes(ctx context.Context, batch graph.NodeBatch) error
	MergeRelationships(ctx context.Context, batch graph.RelationshipBatch) ([]int, error)
}

// RetryPolicy controls retries of retryable errors (error codes marked
// retryable in specs/error-codes.json). Each retry waits a random time
// between half and all of the current backoff, which starts at
// InitialBackoff and doubles up to MaxBackoff.
type RetryPolicy struct {
	// MaxAttempts per operation, including the first; 1 disables retries.
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// State converts the policy for run state and reports.
func (p RetryPolicy) State() *runstate.RetryPolicy {
	return &runstate.RetryPolicy{MaxAttempts: p.MaxAttempts,
		InitialBackoffMs: p.InitialBackoff.Milliseconds(), MaxBackoffMs: p.MaxBackoff.Milliseconds()}
}

// RetryFromState converts a stored policy.
func RetryFromState(p *runstate.RetryPolicy) RetryPolicy {
	return RetryPolicy{MaxAttempts: p.MaxAttempts,
		InitialBackoff: time.Duration(p.InitialBackoffMs) * time.Millisecond, MaxBackoff: time.Duration(p.MaxBackoffMs) * time.Millisecond}
}

// Validate checks the policy's limits (run-state.schema.json).
func (p RetryPolicy) Validate() error {
	const limit = time.Hour
	if p.MaxAttempts < 1 || p.MaxAttempts > 100 || p.InitialBackoff < 0 || p.InitialBackoff > limit ||
		p.MaxBackoff < 0 || p.MaxBackoff > limit || p.MaxBackoff < p.InitialBackoff {
		return rtgerr.New(rtgerr.CLIUsage, "invalid retry policy: attempts must be 1–100 and backoffs 0–1h with the maximum at least the initial backoff")
	}
	return nil
}

// DefaultRetry is used when Options.Retry is zero.
var DefaultRetry = RetryPolicy{MaxAttempts: 3, InitialBackoff: 200 * time.Millisecond, MaxBackoff: 5 * time.Second}

// DefaultBatchSize is used when Options.BatchSize is zero.
const DefaultBatchSize = 1000

// FaultPoint names where a fault injector is consulted.
type FaultPoint string

// Fault points.
const (
	// BeforeWrite: after a batch is read and converted, before it is written.
	BeforeWrite FaultPoint = "before-write"
	// BeforeCheckpoint: after a batch is committed and its failed records
	// are recorded, but before the checkpoint is saved.
	BeforeCheckpoint FaultPoint = "before-checkpoint"
	// AfterCheckpoint: after a batch is committed and checkpointed.
	AfterCheckpoint FaultPoint = "after-checkpoint"
	// Write: inside the retried destination write, before every attempt.
	// A retryable error simulates a transient destination failure. Batch
	// is 0 for schema statements (constraints, tables).
	Write FaultPoint = "write"
)

// FaultInjector lets tests stop a run at a precise point. Returning an error
// stops the run as if the process had failed there.
type FaultInjector func(stepID string, batch int64, point FaultPoint) error

// Progress reports one committed batch.
type Progress struct {
	StepID  string
	Batch   int64
	Read    int64
	Written int64
	Failed  int64
	Done    bool
}

// Options configures a run.
type Options struct {
	Plan *planning.Plan
	// MappingJSON is the mapping's canonical JSON, stored with a new run.
	MappingJSON []byte
	Source      Source
	Destination Destination
	// GraphSource and RelationalDestination replace Source and Destination
	// for graph → relational plans.
	GraphSource           GraphSource
	RelationalDestination RelationalDestination
	SourceInfo            runstate.Endpoint
	DestinationInfo       runstate.Endpoint
	Store                 *runstate.Store
	BatchSize             int
	// DryRun reads and converts everything but writes nothing to the
	// destination (not even constraints).
	DryRun bool
	// ResumeRunID resumes an existing run instead of starting a new one.
	ResumeRunID string
	// Retry is the retry policy. Zero keeps a resumed run's policy, or
	// uses DefaultRetry.
	Retry    RetryPolicy
	Progress func(Progress)
	Fault    FaultInjector
}

// Run executes the plan and returns the final run state. The state, the
// failed records and the report are persisted in the store even when Run
// returns an error, so the run can be inspected and resumed.
func Run(ctx context.Context, opts Options) (*runstate.State, error) {
	if opts.Retry.MaxAttempts > 0 {
		if err := opts.Retry.Validate(); err != nil {
			return nil, err
		}
	}
	st, err := start(opts)
	if err != nil {
		return nil, err
	}
	switch {
	case opts.Retry.MaxAttempts > 0:
	case st.Retry != nil:
		opts.Retry = RetryFromState(st.Retry)
	default:
		opts.Retry = DefaultRetry
	}
	st.Retry = opts.Retry.State()
	r := &runner{opts: opts, st: st}
	runErr := r.run(ctx)
	if err := r.finish(runErr); err != nil && runErr == nil {
		runErr = err
	}
	return st, runErr
}

func start(opts Options) (*runstate.State, error) {
	if opts.ResumeRunID == "" {
		batch := opts.BatchSize
		if batch <= 0 {
			batch = DefaultBatchSize
		}
		now := runstate.Now()
		st := &runstate.State{
			SpecVersion: spec.Version, RunID: runstate.NewRunID(),
			Mapping: runstate.MappingRef{Name: opts.Plan.Mapping.Name, Hash: opts.Plan.Mapping.Hash},
			DryRun:  opts.DryRun, Status: runstate.StatusRunning, BatchSize: batch,
			Source: opts.SourceInfo, Destination: opts.DestinationInfo,
			Streams: []runstate.Stream{}, Attempts: 1, StartedAt: now,
		}
		for _, s := range opts.Plan.Steps {
			if s.Kind.Loads() {
				st.Streams = append(st.Streams, runstate.Stream{StepID: s.ID, Kind: string(s.Kind)})
			}
		}
		planJSON, err := canonjson.Marshal(opts.Plan)
		if err != nil {
			return nil, rtgerr.New(rtgerr.Internal, "encoding the plan: %v", err)
		}
		if err := opts.Store.Create(st, opts.MappingJSON, planJSON); err != nil {
			return nil, err
		}
		return st, nil
	}

	st, err := opts.Store.Load(opts.ResumeRunID)
	if err != nil {
		return nil, err
	}
	if st.Status == runstate.StatusSucceeded || st.Status == runstate.StatusSucceededWithFailures {
		return nil, rtgerr.New(rtgerr.RunCompleted, "run %s already finished with status %s", st.RunID, st.Status)
	}
	if st.Mapping.Hash != opts.Plan.Mapping.Hash {
		return nil, rtgerr.New(rtgerr.RunMappingChanged, "run %s was started with mapping %s, not %s",
			st.RunID, st.Mapping.Hash[:12], opts.Plan.Mapping.Hash[:12])
	}
	st.Attempts++
	st.Status = runstate.StatusRunning
	st.LastError = nil
	if opts.DestinationInfo.Version != "" {
		st.Destination = opts.DestinationInfo
	}
	if opts.SourceInfo.Version != "" {
		st.Source = opts.SourceInfo
	}
	if err := opts.Store.Save(st); err != nil {
		return nil, err
	}
	return st, nil
}

type runner struct {
	opts Options
	st   *runstate.State
}

func (r *runner) run(ctx context.Context) error {
	if r.opts.Plan.Direction == mapping.GraphToRelational {
		return r.runReverse(ctx)
	}
	if !r.st.DryRun {
		for _, s := range r.opts.Plan.Steps {
			if s.Kind != planning.StepConstraint {
				continue
			}
			if err := r.retry(ctx, s.ID, 0, func() error {
				return r.opts.Destination.EnsureIdentityConstraint(ctx, s.Label, s.Property)
			}); err != nil {
				return err
			}
		}
	}
	for i := range r.opts.Plan.Steps {
		step := &r.opts.Plan.Steps[i]
		if step.Kind == planning.StepConstraint {
			continue
		}
		stream := r.st.Stream(step.ID)
		if stream == nil {
			return rtgerr.New(rtgerr.RunStateCorrupt, "run state has no stream for step %s", step.ID)
		}
		if stream.Done {
			continue
		}
		if err := r.runStep(ctx, step, stream); err != nil {
			return err
		}
	}
	return nil
}

// finish records the outcome, the report and the final state.
func (r *runner) finish(runErr error) error {
	st := r.st
	if runErr == nil {
		st.Status = runstate.StatusSucceeded
		for _, s := range st.Streams {
			if s.Failed > 0 || s.NullKeys > 0 {
				st.Status = runstate.StatusSucceededWithFailures
			}
		}
		st.CompletedAt = runstate.Now()
	} else {
		var e *rtgerr.Error
		if !errors.As(runErr, &e) {
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				e = rtgerr.New(rtgerr.ExecInterrupted, "the run was cancelled; resume it with --resume %s", st.RunID)
			} else {
				e = rtgerr.New(rtgerr.Internal, "%v", runErr)
			}
		}
		st.LastError = &runstate.ErrorInfo{Code: string(e.Code), Message: e.Message}
		st.Status = runstate.StatusFailed
		if e.Retryable() || e.Code == rtgerr.ExecInterrupted {
			st.Status = runstate.StatusInterrupted
		}
	}
	if err := r.opts.Store.Save(st); err != nil {
		return err
	}
	failures, err := r.opts.Store.Failures(st)
	if err != nil {
		return err
	}
	rep := reporting.Build(st, r.opts.Plan, failures)
	b, err := canonjson.Marshal(rep)
	if err != nil {
		return rtgerr.New(rtgerr.Internal, "encoding the report: %v", err)
	}
	return r.opts.Store.WriteFile(st.RunID, "report.json", b)
}

// retry runs a destination write, retrying retryable errors with
// exponential backoff and jitter. The Write fault point is consulted before
// every attempt.
func (r *runner) retry(ctx context.Context, stepID string, batch int64, fn func() error) error {
	p := r.opts.Retry
	backoff := p.InitialBackoff
	for attempt := 1; ; attempt++ {
		err := r.fault(stepID, batch, Write)
		if err == nil {
			err = fn()
		}
		if err == nil {
			return nil
		}
		var e *rtgerr.Error
		if !errors.As(err, &e) || !e.Retryable() || attempt >= p.MaxAttempts {
			return err
		}
		r.st.Retries++
		sleep := backoff/2 + time.Duration(rand.Int64N(int64(backoff/2)+1)) //nolint:gosec // jitter, not security
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
		backoff = min(backoff*2, p.MaxBackoff)
	}
}

func (r *runner) fault(step string, batch int64, point FaultPoint) error {
	if r.opts.Fault == nil {
		return nil
	}
	return r.opts.Fault(step, batch, point)
}

// readResult is one batch from the reader.
type readResult struct {
	rows []relational.Row
	err  error
}

func columnsOf(fields []planning.Field) []relational.Column {
	out := make([]relational.Column, len(fields))
	for i, f := range fields {
		out[i] = relational.Column{Name: f.Column, Type: f.Type, Element: f.Element, NativeType: f.NativeType}
	}
	return out
}

func names(fields []planning.Field) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Column
	}
	return out
}

func (r *runner) runStep(ctx context.Context, step *planning.Step, stream *runstate.Stream) error {
	table := relational.Table{Schema: step.Table.Schema, Name: step.Table.Name}

	// Rows with a NULL key cannot be read in key order or identified. Count
	// them once, before the first batch, and record one aggregate failure.
	if stream.Batches == 0 && stream.Cursor == nil {
		n, err := r.opts.Source.CountNullKeys(ctx, table, names(step.Key))
		if err != nil {
			return err
		}
		stream.NullKeys = n
		if n > 0 && step.Kind == planning.StepNodes {
			if err := r.opts.Store.AppendFailures(r.st.RunID, []runstate.Failure{{
				StepID: step.ID, Batch: runstate.AggregateBatch, Key: []runstate.KeyValue{},
				Code:    string(rtgerr.IDNullComponent),
				Message: fmt.Sprintf("%d rows of %s.%s have NULL in key columns (%s) and were not loaded", n, table.Schema, table.Name, strings.Join(names(step.Key), ", ")),
				At:      runstate.Now(),
			}}); err != nil {
				return err
			}
		}
		if step.Kind == planning.StepRelationships {
			stream.NullKeys = 0 // already reported by the entity's node step
		}
		if err := r.opts.Store.Save(r.st); err != nil {
			return err
		}
	}

	req := relational.ReadRequest{Table: table, Key: columnsOf(step.Key), Limit: r.st.BatchSize}
	switch step.Kind {
	case planning.StepNodes:
		req.Columns = columnsOf(step.Properties)
	case planning.StepRelationships:
		req.Columns = append(columnsOf(step.ForeignKey.Columns), columnsOf(step.Properties)...)
	}
	if stream.Cursor != nil {
		req.After = make([]ir.Value, len(stream.Cursor))
		for i, c := range stream.Cursor {
			req.After[i] = ir.TextValue(c)
		}
	}

	readCtx, cancel := context.WithCancel(ctx)
	batches := make(chan readResult, 2) // bounded: the reader runs ≤ 2 batches ahead
	// Wait for the reader to exit before returning: the source connection
	// must be idle before the next step uses it.
	defer func() {
		cancel()
		for range batches {
		}
	}()
	go func() {
		defer close(batches)
		next := req
		for {
			rows, err := r.opts.Source.ReadBatch(readCtx, next)
			select {
			case batches <- readResult{rows: rows, err: err}:
			case <-readCtx.Done():
				return
			}
			if err != nil || len(rows) < next.Limit {
				return
			}
			next.After = rows[len(rows)-1].Key
		}
	}()

	for b := range batches {
		if b.err != nil {
			return b.err
		}
		if err := r.processBatch(ctx, step, stream, b.rows); err != nil {
			return err
		}
		if len(b.rows) < req.Limit {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stream.Done = true
	if err := r.opts.Store.Save(r.st); err != nil {
		return err
	}
	r.progress(step.ID, stream)
	return nil
}

func (r *runner) progress(stepID string, s *runstate.Stream) {
	if r.opts.Progress != nil {
		r.opts.Progress(Progress{StepID: stepID, Batch: s.Batches, Read: s.Read, Written: s.Written, Failed: s.Failed, Done: s.Done})
	}
}

func keyValues(fields []planning.Field, vals []ir.Value) []runstate.KeyValue {
	out := make([]runstate.KeyValue, len(fields))
	for i, f := range fields {
		out[i] = runstate.KeyValue{Column: f.Column, Type: string(f.Type), Value: vals[i].Text}
	}
	return out
}

func identity(entity string, fields []planning.Field, vals []ir.Value) (string, error) {
	comps := make([]ir.KeyComponent, len(fields))
	for i, f := range fields {
		comps[i] = ir.KeyComponent{Type: f.Type, Value: vals[i].Text, Null: vals[i].Null}
	}
	return ir.Identity(entity, comps...)
}

func properties(fields []planning.Field, vals []ir.Value) []graph.Property {
	out := make([]graph.Property, len(fields))
	for i, f := range fields {
		out[i] = graph.Property{Name: f.Property, Type: f.Type, Element: f.Element, Value: vals[i]}
	}
	return out
}

func codeOf(err error) (rtgerr.Code, string) {
	var e *rtgerr.Error
	if errors.As(err, &e) {
		return e.Code, e.Message
	}
	return rtgerr.ExecConversion, err.Error()
}

func (r *runner) processBatch(ctx context.Context, step *planning.Step, stream *runstate.Stream, rows []relational.Row) error {
	batchNo := stream.Batches
	var failures []runstate.Failure
	fail := func(row relational.Row, err error) {
		code, msg := codeOf(err)
		failures = append(failures, runstate.Failure{
			StepID: step.ID, Batch: batchNo, Key: keyValues(step.Key, row.Key),
			Code: string(code), Message: msg, At: runstate.Now(),
		})
	}

	var written, skipped int64
	var err error
	switch step.Kind {
	case planning.StepNodes:
		written, err = r.nodes(ctx, step, batchNo, rows, fail)
	case planning.StepRelationships:
		written, skipped, err = r.relationships(ctx, step, batchNo, rows, fail)
	}
	if err != nil {
		return err
	}

	// Commit point: the destination transaction succeeded. Record the
	// batch's failures, then the checkpoint.
	if err := r.opts.Store.AppendFailures(r.st.RunID, failures); err != nil {
		return err
	}
	if err := r.fault(step.ID, batchNo, BeforeCheckpoint); err != nil {
		return err
	}
	stream.Read += int64(len(rows))
	stream.Written += written
	stream.Failed += int64(len(failures))
	stream.Skipped += skipped
	stream.Batches++
	if len(rows) > 0 {
		last := rows[len(rows)-1].Key
		stream.Cursor = make([]string, len(last))
		for i, v := range last {
			stream.Cursor[i] = v.Text
		}
	}
	if err := r.opts.Store.Save(r.st); err != nil {
		return err
	}
	r.progress(step.ID, stream)
	return r.fault(step.ID, batchNo, AfterCheckpoint)
}

func (r *runner) nodes(ctx context.Context, step *planning.Step, batchNo int64, rows []relational.Row, fail func(relational.Row, error)) (int64, error) {
	var out []graph.NodeRow
	var sources []relational.Row
	for _, row := range rows {
		if row.Err != nil {
			fail(row, row.Err)
			continue
		}
		id, err := identity(step.Entity, step.Key, row.Key)
		if err != nil {
			fail(row, err)
			continue
		}
		enc, err := r.opts.Destination.EncodeProperties(properties(step.Properties, row.Values))
		if err != nil {
			fail(row, err)
			continue
		}
		out = append(out, graph.NodeRow{ID: id, Props: enc})
		sources = append(sources, row)
	}
	if err := r.fault(step.ID, batchNo, BeforeWrite); err != nil {
		return 0, err
	}
	if r.st.DryRun || len(out) == 0 {
		return int64(len(out)), nil
	}

	batch := graph.NodeBatch{Labels: step.Labels, IDProperty: r.opts.Plan.IdentityProperty, Rows: out}
	err := r.retry(ctx, step.ID, batchNo, func() error { return r.opts.Destination.MergeNodes(ctx, batch) })
	if err == nil {
		return int64(len(out)), nil
	}
	if retryableOrCancelled(err) {
		return 0, err
	}
	// A non-retryable batch error: isolate the offending rows by writing
	// one at a time.
	var written int64
	for i, n := range out {
		single := graph.NodeBatch{Labels: step.Labels, IDProperty: batch.IDProperty, Rows: []graph.NodeRow{n}}
		if err := r.retry(ctx, step.ID, batchNo, func() error { return r.opts.Destination.MergeNodes(ctx, single) }); err != nil {
			if retryableOrCancelled(err) {
				return 0, err
			}
			fail(sources[i], err)
			continue
		}
		written++
	}
	return written, nil
}

func retryableOrCancelled(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var e *rtgerr.Error
	return errors.As(err, &e) && (e.Retryable() || e.Code == rtgerr.ExecInterrupted)
}

func (r *runner) relationships(ctx context.Context, step *planning.Step, batchNo int64, rows []relational.Row, fail func(relational.Row, error)) (int64, int64, error) {
	nFK := len(step.ForeignKey.Columns)
	ownerIsFrom := step.ForeignKey.Direction == "owner-to-referenced"
	var out []graph.RelationshipRow
	var sources []relational.Row
	var skipped int64
	for _, row := range rows {
		if row.Err != nil {
			fail(row, row.Err)
			continue
		}
		fk := row.Values[:nFK]
		anyNull := false
		for _, v := range fk {
			if v.Null {
				anyNull = true
			}
		}
		if anyNull {
			skipped++ // a NULL foreign key means "no relationship"
			continue
		}
		owner, err := identity(step.Entity, step.Key, row.Key)
		if err != nil {
			fail(row, err)
			continue
		}
		target, err := identity(step.ForeignKey.References, step.ForeignKey.Columns, fk)
		if err != nil {
			fail(row, err)
			continue
		}
		enc, err := r.opts.Destination.EncodeProperties(properties(step.Properties, row.Values[nFK:]))
		if err != nil {
			fail(row, err)
			continue
		}
		rel := graph.RelationshipRow{From: owner, To: target, Props: enc}
		if !ownerIsFrom {
			rel.From, rel.To = target, owner
		}
		out = append(out, rel)
		sources = append(sources, row)
	}
	if err := r.fault(step.ID, batchNo, BeforeWrite); err != nil {
		return 0, 0, err
	}
	if r.st.DryRun || len(out) == 0 {
		return int64(len(out)), skipped, nil
	}

	batch := graph.RelationshipBatch{
		Type: step.RelationshipType, FromLabel: step.From.Label, ToLabel: step.To.Label,
		IDProperty: r.opts.Plan.IdentityProperty, Rows: out,
	}
	var missing []int
	err := r.retry(ctx, step.ID, batchNo, func() error {
		var err error
		missing, err = r.opts.Destination.MergeRelationships(ctx, batch)
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	for _, i := range missing {
		fail(sources[i], rtgerr.New(rtgerr.ExecMissingEndpoint,
			"(:%s {%s: %s})-[:%s]->(:%s {%s: %s}): an end node does not exist",
			step.From.Label, batch.IDProperty, out[i].From, step.RelationshipType, step.To.Label, batch.IDProperty, out[i].To))
	}
	return int64(len(out) - len(missing)), skipped, nil
}
