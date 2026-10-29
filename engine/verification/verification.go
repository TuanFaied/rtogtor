// Package verification compares a migration's source and destination.
//
// For every entity it streams the source keys in batches, derives each
// row's identity and asks the destination which identities are missing, so
// the check is exact and memory stays bounded. Relationship counts are
// compared per (type, start label, end label) against the source references
// that point to existing rows.
package verification

import (
	"context"
	"fmt"

	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/planning"
	"github.com/TuanFaied/rtogtor/engine/reporting"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/runstate"
)

// Source is the part of a relational adapter verification uses.
type Source interface {
	ReadBatch(ctx context.Context, req relational.ReadRequest) ([]relational.Row, error)
	CountRows(ctx context.Context, table relational.Table, key []string) (int64, error)
	CountReferences(ctx context.Context, ref relational.Reference) (relational.ReferenceCount, error)
}

// Destination is the part of a graph adapter verification uses.
type Destination interface {
	CountNodes(ctx context.Context, label string) (int64, error)
	CountRelationships(ctx context.Context, relType, fromLabel, toLabel string) (int64, error)
	MissingIDs(ctx context.Context, label, property string, ids []string) ([]string, error)
}

// maxMissingKeys bounds the sample of missing keys in a report.
const maxMissingKeys = 10

// Verify checks a plan's results. batchSize bounds memory.
func Verify(ctx context.Context, plan *planning.Plan, src Source, dst Destination, batchSize int) (*reporting.Verification, error) {
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

	// Several entities may share a primary label; node counts are then only
	// comparable for the label as a whole.
	labelRows := map[string]int64{}
	labelOrder := []string{}

	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.Kind != planning.StepNodes {
			continue
		}
		check, err := verifyEntity(ctx, plan, step, src, dst, batchSize)
		if err != nil {
			return nil, err
		}
		v.Entities = append(v.Entities, *check)
		if check.Missing > 0 {
			issue(rtgerr.VerifyMissingEntity, step.Entity, "%d of %d rows have no :%s node", check.Missing, check.SourceCount, check.Label)
		}
		if _, seen := labelRows[check.Label]; !seen {
			labelOrder = append(labelOrder, check.Label)
		}
		labelRows[check.Label] += check.SourceCount - check.Missing
	}
	for _, label := range labelOrder {
		var nodes int64
		for _, e := range v.Entities {
			if e.Label == label {
				nodes = e.DestinationCount
			}
		}
		if nodes != labelRows[label] {
			issue(rtgerr.VerifyEntityCount, ":"+label, "%d :%s nodes exist but %d were expected from the source; extra nodes may come from other sources or earlier runs",
				nodes, label, labelRows[label])
		}
	}

	// Relationships, grouped by (type, from, to).
	type groupKey struct{ typ, from, to string }
	groups := map[groupKey]*reporting.RelationshipCheck{}
	var order []groupKey
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.Kind != planning.StepRelationships {
			continue
		}
		target, ok := plan.StepByID("nodes:" + step.ForeignKey.References)
		if !ok {
			return nil, rtgerr.New(rtgerr.Internal, "plan has no node step for %s", step.ForeignKey.References)
		}
		ref := relational.Reference{
			Owner:   relational.Table{Schema: step.Table.Schema, Name: step.Table.Name},
			Columns: columnNames(step.ForeignKey.Columns),
			Target:  relational.Table{Schema: target.Table.Schema, Name: target.Table.Name},
		}
		ref.TargetColumns = columnNames(target.Key)
		counts, err := src.CountReferences(ctx, ref)
		if err != nil {
			return nil, err
		}
		k := groupKey{step.RelationshipType, step.From.Label, step.To.Label}
		g := groups[k]
		if g == nil {
			g = &reporting.RelationshipCheck{Type: k.typ, FromLabel: k.from, ToLabel: k.to, Relationships: []string{}}
			groups[k] = g
			order = append(order, k)
		}
		g.Relationships = append(g.Relationships, step.Relationship)
		g.Expected += counts.Matched
		g.Dangling += counts.Dangling
		if counts.Dangling > 0 {
			issue(rtgerr.VerifyDanglingSource, step.Relationship, "%d rows of %s.%s reference keys that do not exist in %s.%s",
				counts.Dangling, ref.Owner.Schema, ref.Owner.Name, ref.Target.Schema, ref.Target.Name)
		}
	}
	for _, k := range order {
		g := groups[k]
		actual, err := dst.CountRelationships(ctx, g.Type, g.FromLabel, g.ToLabel)
		if err != nil {
			return nil, err
		}
		g.Actual = actual
		if g.Actual != g.Expected {
			issue(rtgerr.VerifyRelationCount, fmt.Sprintf("(:%s)-[:%s]->(:%s)", g.FromLabel, g.Type, g.ToLabel),
				"found %d relationships, expected %d", g.Actual, g.Expected)
		}
		v.Relationships = append(v.Relationships, *g)
	}
	return v, nil
}

func columnNames(fields []planning.Field) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Column
	}
	return out
}

func verifyEntity(ctx context.Context, plan *planning.Plan, step *planning.Step, src Source, dst Destination, batchSize int) (*reporting.EntityCheck, error) {
	table := relational.Table{Schema: step.Table.Schema, Name: step.Table.Name}
	check := &reporting.EntityCheck{Entity: step.Entity, Label: step.Labels[0], MissingKeys: [][]string{}}

	rows, err := src.CountRows(ctx, table, columnNames(step.Key))
	if err != nil {
		return nil, err
	}
	check.SourceCount = rows
	if check.DestinationCount, err = dst.CountNodes(ctx, check.Label); err != nil {
		return nil, err
	}

	keyCols := make([]relational.Column, len(step.Key))
	for i, f := range step.Key {
		keyCols[i] = relational.Column{Name: f.Column, Type: f.Type, Element: f.Element, NativeType: f.NativeType}
	}
	req := relational.ReadRequest{Table: table, Key: keyCols, Limit: batchSize}
	for {
		batch, err := src.ReadBatch(ctx, req)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		ids := make([]string, 0, len(batch))
		keyOf := make(map[string][]string, len(batch))
		for _, row := range batch {
			comps := make([]ir.KeyComponent, len(step.Key))
			text := make([]string, len(step.Key))
			for i, f := range step.Key {
				comps[i] = ir.KeyComponent{Type: f.Type, Value: row.Key[i].Text}
				text[i] = row.Key[i].Text
			}
			id, err := ir.Identity(step.Entity, comps...)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
			keyOf[id] = text
		}
		missing, err := dst.MissingIDs(ctx, check.Label, plan.IdentityProperty, ids)
		if err != nil {
			return nil, err
		}
		check.Missing += int64(len(missing))
		for _, id := range missing {
			if len(check.MissingKeys) < maxMissingKeys {
				check.MissingKeys = append(check.MissingKeys, keyOf[id])
			}
		}
		if len(batch) < batchSize {
			break
		}
		req.After = batch[len(batch)-1].Key
	}
	return check, nil
}
