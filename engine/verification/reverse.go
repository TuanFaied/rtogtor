package verification

import (
	"context"
	"fmt"
	"strings"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/execution"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/planning"
	"github.com/TuanFaied/rtogtor/engine/reporting"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/runstate"
)

// GraphSource is the part of a graph adapter graph → relational
// verification uses.
type GraphSource interface {
	ReadNodes(ctx context.Context, req graph.NodeReadRequest) ([]graph.NodeRecord, error)
	CountKeyedNodes(ctx context.Context, labels []string, key []graph.NodeField) (int64, error)
	CountRelated(ctx context.Context, labels []string, key []graph.NodeField, rel graph.RelatedRead) (int64, error)
}

// RelationalDestination is the part of a relational adapter graph →
// relational verification uses.
type RelationalDestination interface {
	CountRows(ctx context.Context, table relational.Table, key []string) (int64, error)
	CountReferences(ctx context.Context, ref relational.Reference) (relational.ReferenceCount, error)
	MissingKeys(ctx context.Context, table relational.Table, key []relational.Column, keys [][]ir.Value) ([][]ir.Value, error)
}

// VerifyReverse checks a graph → relational plan's results: every keyed
// node has a row, row counts match the distinct node keys, and every
// foreign key that a single matching relationship defines is filled.
func VerifyReverse(ctx context.Context, plan *planning.Plan, src GraphSource, dst RelationalDestination, batchSize int) (*reporting.Verification, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	v := &reporting.Verification{
		Status: "passed", CheckedAt: runstate.Now(),
		Entities: []reporting.EntityCheck{}, Relationships: []reporting.RelationshipCheck{},
		Issues: []planning.Diagnostic{},
	}
	issue := func(code rtgerr.Code, path, format string, args ...any) {
		v.Issues = append(v.Issues, planning.Diagnostic{Code: string(code), Path: path, Message: fmt.Sprintf(format, args...)})
		if d, ok := rtgerr.Lookup(code); !ok || d.Severity != rtgerr.SeverityWarning {
			v.Status = "failed"
		}
	}

	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.Kind != planning.StepRows {
			continue
		}
		check, distinct, err := verifyRows(ctx, step, src, dst, batchSize)
		if err != nil {
			return nil, err
		}
		v.Entities = append(v.Entities, *check)
		table := step.Table.Schema + "." + step.Table.Name
		if check.Missing > 0 {
			issue(rtgerr.VerifyMissingEntity, step.Entity, "%d of %d :%s nodes have no row in %s", check.Missing, check.SourceCount, check.Label, table)
		}
		if want := distinct - check.Missing; check.DestinationCount != want {
			issue(rtgerr.VerifyEntityCount, table, "%d rows exist but %d were expected from distinct node keys; extra rows may come from other sources or earlier runs",
				check.DestinationCount, want)
		}
	}

	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.Kind != planning.StepRows && step.Kind != planning.StepForeignKeys {
			continue
		}
		for _, l := range step.ForeignKeys {
			if step.Kind == planning.StepRows && l.Deferred {
				continue
			}
			target, ok := plan.StepByID("rows:" + l.References)
			if !ok {
				return nil, rtgerr.New(rtgerr.Internal, "plan has no rows step for %s", l.References)
			}
			expected, err := src.CountRelated(ctx, step.Labels, execution.NodeFields(step.Key), execution.RelatedRead(l))
			if err != nil {
				return nil, err
			}
			ref := relational.Reference{
				Owner: relational.Table{Schema: step.Table.Schema, Name: step.Table.Name}, Columns: columnNames(l.Columns),
				Target: relational.Table{Schema: target.Table.Schema, Name: target.Table.Name}, TargetColumns: columnNames(target.Key),
			}
			counts, err := dst.CountReferences(ctx, ref)
			if err != nil {
				return nil, err
			}
			from, to := step.Labels[0], l.ReferencedLabels[0]
			if l.Direction == "incoming" {
				from, to = to, from
			}
			rc := reporting.RelationshipCheck{Type: l.RelationshipType, FromLabel: from, ToLabel: to,
				Relationships: []string{l.Relationship}, Expected: expected, Actual: counts.Matched, Dangling: counts.Dangling}
			v.Relationships = append(v.Relationships, rc)
			path := fmt.Sprintf("%s.%s(%s)", ref.Owner.Schema, ref.Owner.Name, strings.Join(ref.Columns, ", "))
			if rc.Actual != rc.Expected {
				issue(rtgerr.VerifyRelationCount, path, "%d rows reference %s.%s, expected %d from single :%s relationships",
					rc.Actual, ref.Target.Schema, ref.Target.Name, rc.Expected, rc.Type)
			}
			if rc.Dangling > 0 {
				issue(rtgerr.VerifyRelationCount, path, "%d rows reference keys that do not exist in %s.%s",
					rc.Dangling, ref.Target.Schema, ref.Target.Name)
			}
		}
	}
	return v, nil
}

// verifyRows streams an entity's node keys and checks each has a row. It
// also returns the number of distinct keys (duplicates are adjacent in key
// order).
func verifyRows(ctx context.Context, step *planning.Step, src GraphSource, dst RelationalDestination, batchSize int) (*reporting.EntityCheck, int64, error) {
	table := relational.Table{Schema: step.Table.Schema, Name: step.Table.Name}
	check := &reporting.EntityCheck{Entity: step.Entity, Label: step.Labels[0], MissingKeys: [][]string{}}
	key := execution.NodeFields(step.Key)
	var err error
	if check.SourceCount, err = src.CountKeyedNodes(ctx, step.Labels, key); err != nil {
		return nil, 0, err
	}
	if check.DestinationCount, err = dst.CountRows(ctx, table, columnNames(step.Key)); err != nil {
		return nil, 0, err
	}

	keyCols := make([]relational.Column, len(step.Key))
	for i, f := range step.Key {
		keyCols[i] = relational.Column{Name: f.Column, Type: f.Type, Element: f.Element, NativeType: f.NativeType}
	}
	var distinct int64
	var prev string
	first := true
	req := graph.NodeReadRequest{Labels: step.Labels, Key: key, Limit: batchSize}
	for {
		batch, err := src.ReadNodes(ctx, req)
		if err != nil {
			return nil, 0, err
		}
		var keys [][]ir.Value
		for _, n := range batch {
			text := make([]string, len(n.Key))
			for i, k := range n.Key {
				text[i] = k.Text
			}
			joined := strings.Join(text, "\x00")
			if first || joined != prev {
				distinct++
				keys = append(keys, n.Key)
			}
			prev, first = joined, false
		}
		missing, err := dst.MissingKeys(ctx, table, keyCols, keys)
		if err != nil {
			return nil, 0, err
		}
		check.Missing += int64(len(missing))
		for _, m := range missing {
			if len(check.MissingKeys) < maxMissingKeys {
				text := make([]string, len(m))
				for i, k := range m {
					text[i] = k.Text
				}
				check.MissingKeys = append(check.MissingKeys, text)
			}
		}
		if len(batch) < batchSize {
			break
		}
		last := batch[len(batch)-1]
		req.After = &graph.NodeCursor{Key: last.Key, ElementID: last.ElementID}
	}
	return check, distinct, nil
}
