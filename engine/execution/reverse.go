package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/planning"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/runstate"
)

// Graph → relational execution (ADR-0006). The same guarantees hold: each
// batch is one destination statement, failures and the checkpoint are
// recorded after it commits, and replays are idempotent (INSERT … ON
// CONFLICT, UPDATE by key).

// GraphSource is the part of a graph adapter the executor reads from.
type GraphSource interface {
	ReadNodes(ctx context.Context, req graph.NodeReadRequest) ([]graph.NodeRecord, error)
	CountNodesWithoutKey(ctx context.Context, labels []string, key []graph.NodeField) (int64, error)
}

// RelationalDestination is the part of a relational adapter the executor
// writes to.
type RelationalDestination interface {
	ExecDDL(ctx context.Context, sql string) error
	UpsertRows(ctx context.Context, batch relational.UpsertBatch) error
	UpdateRows(ctx context.Context, batch relational.UpdateBatch) error
}

func (r *runner) runReverse(ctx context.Context) error {
	if r.opts.GraphSource == nil || r.opts.RelationalDestination == nil {
		return rtgerr.New(rtgerr.Internal, "a graph → relational run needs a graph source and a relational destination")
	}
	if !r.st.DryRun {
		for _, s := range r.opts.Plan.Steps {
			if s.Kind != planning.StepTable {
				continue
			}
			if err := r.retry(ctx, s.ID, 0, func() error { return r.opts.RelationalDestination.ExecDDL(ctx, s.SQL) }); err != nil {
				return err
			}
		}
	}
	for i := range r.opts.Plan.Steps {
		step := &r.opts.Plan.Steps[i]
		if !step.Kind.Loads() {
			continue
		}
		stream := r.st.Stream(step.ID)
		if stream == nil {
			return rtgerr.New(rtgerr.RunStateCorrupt, "run state has no stream for step %s", step.ID)
		}
		if stream.Done {
			continue
		}
		if err := r.runNodeStep(ctx, step, stream); err != nil {
			return err
		}
	}
	return nil
}

// NodeFields converts plan fields to graph read fields.
func NodeFields(fields []planning.Field) []graph.NodeField {
	out := make([]graph.NodeField, len(fields))
	for i, f := range fields {
		out[i] = graph.NodeField{Property: f.Property, Type: f.Type, Element: f.Element}
	}
	return out
}

// RelatedRead converts a foreign-key load to a graph related-node read.
func RelatedRead(l planning.ForeignKeyLoad) graph.RelatedRead {
	return graph.RelatedRead{Type: l.RelationshipType, Outgoing: l.Direction == "outgoing",
		Labels: l.ReferencedLabels, Key: NodeFields(l.ReferencedKey)}
}

type nodeResult struct {
	recs []graph.NodeRecord
	err  error
}

func (r *runner) runNodeStep(ctx context.Context, step *planning.Step, stream *runstate.Stream) error {
	key := NodeFields(step.Key)
	if stream.Batches == 0 && stream.Cursor == nil {
		if step.Kind == planning.StepRows {
			// Nodes without a usable key cannot be read in key order or
			// written: count them once and record one aggregate failure.
			n, err := r.opts.GraphSource.CountNodesWithoutKey(ctx, step.Labels, key)
			if err != nil {
				return err
			}
			stream.NullKeys = n
			if n > 0 {
				props := make([]string, len(step.Key))
				for i, f := range step.Key {
					props[i] = fmt.Sprintf("%s (%s)", f.Property, f.Type)
				}
				if err := r.opts.Store.AppendFailures(r.st.RunID, []runstate.Failure{{
					StepID: step.ID, Batch: runstate.AggregateBatch, Key: []runstate.KeyValue{},
					Code: string(rtgerr.IDNullComponent),
					Message: fmt.Sprintf("%d :%s nodes lack key properties %s, or hold them with another type, and were not loaded",
						n, strings.Join(step.Labels, ":"), strings.Join(props, ", ")),
					At: runstate.Now(),
				}}); err != nil {
					return err
				}
			}
		}
		if err := r.opts.Store.Save(r.st); err != nil {
			return err
		}
	}

	req := graph.NodeReadRequest{Labels: step.Labels, Key: key, Limit: r.st.BatchSize}
	if step.Kind == planning.StepRows {
		req.Properties = NodeFields(step.Properties)
	}
	for _, l := range step.ForeignKeys {
		req.Related = append(req.Related, RelatedRead(l))
	}
	// prev is the key of the last node read; equal keys are adjacent.
	var prev []string
	if c := stream.Cursor; c != nil {
		if len(c) != len(step.Key)+1 {
			return rtgerr.New(rtgerr.RunStateCorrupt, "step %s: cursor has %d values, expected %d", step.ID, len(c), len(step.Key)+1)
		}
		req.After = &graph.NodeCursor{ElementID: c[len(c)-1]}
		for _, v := range c[:len(c)-1] {
			req.After.Key = append(req.After.Key, ir.TextValue(v))
		}
		prev = c[:len(c)-1]
	}

	readCtx, cancel := context.WithCancel(ctx)
	batches := make(chan nodeResult, 2) // bounded: the reader runs ≤ 2 batches ahead
	defer func() {
		cancel()
		for range batches {
		}
	}()
	go func() {
		defer close(batches)
		next := req
		for {
			recs, err := r.opts.GraphSource.ReadNodes(readCtx, next)
			select {
			case batches <- nodeResult{recs: recs, err: err}:
			case <-readCtx.Done():
				return
			}
			if err != nil || len(recs) < next.Limit {
				return
			}
			last := recs[len(recs)-1]
			next.After = &graph.NodeCursor{Key: last.Key, ElementID: last.ElementID}
		}
	}()

	for b := range batches {
		if b.err != nil {
			return b.err
		}
		if err := r.processNodeBatch(ctx, step, stream, b.recs, &prev); err != nil {
			return err
		}
		if len(b.recs) < req.Limit {
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

func texts(vals []ir.Value) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = v.Text
	}
	return out
}

func sameKey(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *runner) processNodeBatch(ctx context.Context, step *planning.Step, stream *runstate.Stream, recs []graph.NodeRecord, prev *[]string) error {
	batchNo := stream.Batches
	var failures []runstate.Failure
	fail := func(rec graph.NodeRecord, err error) {
		code, msg := codeOf(err)
		failures = append(failures, runstate.Failure{
			StepID: step.ID, Batch: batchNo, Key: keyValues(step.Key, rec.Key),
			Code: string(code), Message: msg + " (element ID " + rec.ElementID + ")", At: runstate.Now(),
		})
	}

	// Duplicate keys: the first node in key order wins; later ones are
	// failed records in the rows step and skipped in update steps, where
	// the rows step already reported them.
	var unique []graph.NodeRecord
	var skipped int64
	seen := map[string]bool{}
	for _, rec := range recs {
		k := texts(rec.Key)
		joined := strings.Join(k, "\x00")
		if sameKey(k, *prev) || seen[joined] {
			if step.Kind == planning.StepRows {
				fail(rec, rtgerr.New(rtgerr.ExecDuplicateKey, "another :%s node with key (%s) was already written",
					strings.Join(step.Labels, ":"), strings.Join(k, ", ")))
			} else {
				skipped++
			}
		} else {
			unique = append(unique, rec)
		}
		*prev = k
		seen[joined] = true
	}

	var written int64
	var err error
	switch step.Kind {
	case planning.StepRows:
		written, err = r.rows(ctx, step, batchNo, unique, fail)
	case planning.StepForeignKeys:
		written, err = r.foreignKeys(ctx, step, batchNo, unique, fail)
	}
	if err != nil {
		return err
	}

	if err := r.opts.Store.AppendFailures(r.st.RunID, failures); err != nil {
		return err
	}
	if err := r.fault(step.ID, batchNo, BeforeCheckpoint); err != nil {
		return err
	}
	stream.Read += int64(len(recs))
	stream.Written += written
	stream.Failed += int64(len(failures))
	stream.Skipped += skipped
	stream.Batches++
	if len(recs) > 0 {
		last := recs[len(recs)-1]
		stream.Cursor = append(texts(last.Key), last.ElementID)
	}
	if err := r.opts.Store.Save(r.st); err != nil {
		return err
	}
	r.progress(step.ID, stream)
	return r.fault(step.ID, batchNo, AfterCheckpoint)
}

// fractionalDigits counts the digits after the decimal point of a canonical
// decimal or temporal text.
func fractionalDigits(text string) int {
	i := strings.IndexByte(text, '.')
	if i < 0 {
		return 0
	}
	n := 0
	for _, c := range text[i+1:] {
		if c < '0' || c > '9' {
			break
		}
		n++
	}
	return n
}

// precisionLoss reports whether the destination column would round v.
func precisionLoss(f planning.Field, v ir.Value) bool {
	if v.Null {
		return false
	}
	switch {
	case f.Type == ir.TypeDecimal && f.Scale != nil:
		return fractionalDigits(v.Text) > *f.Scale
	case (f.Type == ir.TypeTimestamp || f.Type == ir.TypeTimestampTZ) && f.FractionalDigits != nil:
		return fractionalDigits(v.Text) > *f.FractionalDigits
	}
	return false
}

// resolveForeignKey returns the referenced key of a node's single matching
// related node, nil when it has none, or an error.
func resolveForeignKey(l planning.ForeignKeyLoad, related []graph.RelatedNode) ([]ir.Value, error) {
	switch {
	case len(related) == 0:
		return nil, nil
	case len(related) > 1:
		return nil, rtgerr.New(rtgerr.ExecMultipleRelated, "the node has more than one :%s relationship for foreign key %s, which holds one reference",
			l.RelationshipType, l.Relationship)
	case !related[0].Matches:
		key := make([]string, len(l.ReferencedKey))
		for i, f := range l.ReferencedKey {
			key[i] = f.Property
		}
		return nil, rtgerr.New(rtgerr.ExecMissingEndpoint, "the node at the other end of its :%s relationship is not a :%s node with key properties (%s)",
			l.RelationshipType, strings.Join(l.ReferencedLabels, ":"), strings.Join(key, ", "))
	}
	return related[0].Key, nil
}

func (r *runner) rows(ctx context.Context, step *planning.Step, batchNo int64, recs []graph.NodeRecord, fail func(graph.NodeRecord, error)) (int64, error) {
	allowed := map[string]bool{}
	for _, t := range r.opts.Plan.Transformations {
		if t.Entity == step.Entity && t.LossKind == string(graph.LossPrecision) {
			allowed[t.Property] = true
		}
	}
	// Columns: mapped properties, then foreign-key columns that are not
	// also properties.
	fields := append([]planning.Field{}, step.Properties...)
	index := map[string]int{}
	for i, f := range fields {
		index[f.Column] = i
	}
	nProps := len(fields)
	for _, l := range step.ForeignKeys {
		for _, c := range l.Columns {
			if _, ok := index[c.Column]; !ok {
				index[c.Column] = len(fields)
				fields = append(fields, c)
			}
		}
	}

	var out [][]ir.Value
	var sources []graph.NodeRecord
next:
	for _, rec := range recs {
		if rec.Err != nil {
			fail(rec, rec.Err)
			continue
		}
		vals := make([]ir.Value, len(fields))
		copy(vals, rec.Values)
		for i := nProps; i < len(vals); i++ {
			vals[i] = ir.NullValue()
		}
		for i, f := range step.Properties {
			if !allowed[f.Property] && precisionLoss(f, vals[i]) {
				fail(rec, rtgerr.New(rtgerr.ExecPrecisionLoss, "property %s: %s has more fractional digits than column %s (%s) keeps",
					f.Property, vals[i].Text, f.Column, f.NativeType))
				continue next
			}
		}
		for li, l := range step.ForeignKeys {
			ref, err := resolveForeignKey(l, rec.Related[li])
			if err != nil {
				fail(rec, err)
				continue next
			}
			if ref == nil {
				continue // no relationship: the columns stay as mapped (or NULL)
			}
			for j, c := range l.Columns {
				i := index[c.Column]
				if i < nProps && !vals[i].Null && vals[i].Text != ref[j].Text {
					fail(rec, rtgerr.New(rtgerr.ExecMissingEndpoint, "column %s: property %s is %s, but the :%s relationship leads to a node with key %s",
						c.Column, step.Properties[i].Property, vals[i].Text, l.RelationshipType, ref[j].Text))
					continue next
				}
				vals[i] = ref[j]
			}
		}
		out = append(out, vals)
		sources = append(sources, rec)
	}
	if err := r.fault(step.ID, batchNo, BeforeWrite); err != nil {
		return 0, err
	}
	if r.st.DryRun || len(out) == 0 {
		return int64(len(out)), nil
	}

	cols := columnsOf(fields)
	batch := relational.UpsertBatch{
		Table: relational.Table{Schema: step.Table.Schema, Name: step.Table.Name},
		Key:   names(step.Key), Columns: cols, Rows: out,
	}
	return r.writeRows(ctx, step, batchNo, len(out), func(rows [][]ir.Value) error {
		b := batch
		b.Rows = rows
		return r.opts.RelationalDestination.UpsertRows(ctx, b)
	}, out, func(i int, err error) { fail(sources[i], err) })
}

func (r *runner) foreignKeys(ctx context.Context, step *planning.Step, batchNo int64, recs []graph.NodeRecord, fail func(graph.NodeRecord, error)) (int64, error) {
	l := step.ForeignKeys[0]
	var out [][]ir.Value
	var sources []graph.NodeRecord
	for _, rec := range recs {
		ref, err := resolveForeignKey(l, rec.Related[0])
		if err != nil {
			fail(rec, err)
			continue
		}
		vals := append([]ir.Value{}, rec.Key...)
		for j := range l.Columns {
			if ref == nil {
				vals = append(vals, ir.NullValue())
			} else {
				vals = append(vals, ref[j])
			}
		}
		out = append(out, vals)
		sources = append(sources, rec)
	}
	if err := r.fault(step.ID, batchNo, BeforeWrite); err != nil {
		return 0, err
	}
	if r.st.DryRun || len(out) == 0 {
		return int64(len(out)), nil
	}
	batch := relational.UpdateBatch{
		Table: relational.Table{Schema: step.Table.Schema, Name: step.Table.Name},
		Key:   columnsOf(step.Key), Columns: columnsOf(l.Columns),
	}
	return r.writeRows(ctx, step, batchNo, len(out), func(rows [][]ir.Value) error {
		b := batch
		b.Rows = rows
		return r.opts.RelationalDestination.UpdateRows(ctx, b)
	}, out, func(i int, err error) { fail(sources[i], err) })
}

// writeRows writes a batch; if the database rejects it for a non-retryable
// reason, it writes the rows one at a time to isolate the rejected ones.
func (r *runner) writeRows(ctx context.Context, step *planning.Step, batchNo int64, n int, write func([][]ir.Value) error, rows [][]ir.Value, fail func(int, error)) (int64, error) {
	err := r.retry(ctx, step.ID, batchNo, func() error { return write(rows) })
	if err == nil {
		return int64(n), nil
	}
	if retryableOrCancelled(err) {
		return 0, err
	}
	var e *rtgerr.Error
	if !errors.As(err, &e) || (e.Code != rtgerr.PGRowRejected && e.Code != rtgerr.ExecConversion) {
		return 0, err // not caused by the data: stop the run
	}
	var written int64
	for i := range rows {
		if err := r.retry(ctx, step.ID, batchNo, func() error { return write(rows[i : i+1]) }); err != nil {
			if retryableOrCancelled(err) {
				return 0, err
			}
			fail(i, err)
			continue
		}
		written++
	}
	return written, nil
}
