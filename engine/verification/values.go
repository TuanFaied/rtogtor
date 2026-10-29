package verification

import (
	"bytes"
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
)

// Value verification: every migrated value is read from both sides and
// compared in canonical form. Values covered by a lossy transformation the
// plan allowed with a changing loss (precision, range, structure) are not
// compared; representation losses (decimal stored as a string) round-trip
// exactly and are compared. Messages name keys and properties, never values.

// maxValueSamples bounds the records named in a mismatch message.
const maxValueSamples = 3

// equalValues compares canonical values.
func equalValues(a, b ir.Value) bool {
	if a.Null || b.Null {
		return a.Null == b.Null
	}
	if a.JSON != nil || b.JSON != nil {
		return bytes.Equal(a.JSON, b.JSON)
	}
	if a.Elements != nil || b.Elements != nil {
		if len(a.Elements) != len(b.Elements) {
			return false
		}
		for i := range a.Elements {
			if !equalValues(a.Elements[i], b.Elements[i]) {
				return false
			}
		}
		return true
	}
	return a.Text == b.Text
}

// compared returns the fields whose values can be compared.
func compared(plan *planning.Plan, entity string, fields []planning.Field) []int {
	skip := map[string]bool{}
	for _, t := range plan.Transformations {
		if t.Entity == entity && t.LossKind != string(graph.LossRepresentation) {
			skip[t.Property] = true
		}
	}
	var out []int
	for i, f := range fields {
		if !skip[f.Property] {
			out = append(out, i)
		}
	}
	return out
}

// mismatches collects one entity's differing records.
type mismatches struct {
	count   int64
	samples []string
}

func (m *mismatches) add(key []ir.Value, props []string) {
	m.count++
	if len(m.samples) < maxValueSamples {
		k := make([]string, len(key))
		for i, v := range key {
			k[i] = v.Text
		}
		m.samples = append(m.samples, fmt.Sprintf("(%s): %s", strings.Join(k, ", "), strings.Join(props, ", ")))
	}
}

func (m *mismatches) record(check *reporting.EntityCheck, issue func(rtgerr.Code, string, string, ...any)) {
	n := m.count
	check.ValueMismatches = &n
	if n > 0 {
		issue(rtgerr.VerifyValueMismatch, check.Entity, "%d records differ, for example key %s", n, strings.Join(m.samples, "; key "))
	}
}

// CompareValues adds value checks for a relational → graph run to v.
func CompareValues(ctx context.Context, plan *planning.Plan, src Source, dst interface {
	NodeValues(ctx context.Context, label, idProperty string, ids []string, props []graph.NodeField) (map[string]graph.NodeValues, error)
}, batchSize int, v *reporting.Verification) error {
	if batchSize <= 0 {
		batchSize = 1000
	}
	issue := issuer(v)
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.Kind != planning.StepNodes {
			continue
		}
		check := entityCheck(v, step.Entity)
		if check == nil {
			continue
		}
		idx := compared(plan, step.Entity, step.Properties)
		fields := make([]graph.NodeField, len(idx))
		cols := make([]relational.Column, len(idx))
		for j, k := range idx {
			f := step.Properties[k]
			fields[j] = graph.NodeField{Property: f.Property, Type: f.Type, Element: f.Element}
			cols[j] = relational.Column{Name: f.Column, Type: f.Type, Element: f.Element, NativeType: f.NativeType}
		}
		var mm mismatches
		req := relational.ReadRequest{Table: relational.Table{Schema: step.Table.Schema, Name: step.Table.Name},
			Key: columnsOf(step.Key), Columns: cols, Limit: batchSize}
		for {
			rows, err := src.ReadBatch(ctx, req)
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(rows))
			idOf := make([]string, len(rows))
			for r, row := range rows {
				comps := make([]ir.KeyComponent, len(step.Key))
				for j, f := range step.Key {
					comps[j] = ir.KeyComponent{Type: f.Type, Value: row.Key[j].Text}
				}
				id, err := ir.Identity(step.Entity, comps...)
				if err != nil {
					return err
				}
				idOf[r] = id
				ids = append(ids, id)
			}
			nodes, err := dst.NodeValues(ctx, step.Labels[0], plan.IdentityProperty, ids, fields)
			if err != nil {
				return err
			}
			for r, row := range rows {
				n, ok := nodes[idOf[r]]
				if !ok || row.Err != nil {
					continue // missing nodes and unreadable rows are reported elsewhere
				}
				if n.Err != nil {
					mm.add(row.Key, []string{"(unreadable: " + n.Err.Error() + ")"})
					continue
				}
				if diff := differing(fields, row.Values, n.Values); len(diff) > 0 {
					mm.add(row.Key, diff)
				}
			}
			if len(rows) < batchSize {
				break
			}
			req.After = rows[len(rows)-1].Key
		}
		mm.record(check, issue)
	}
	return nil
}

// CompareValuesReverse adds value checks for a graph → relational run to v.
func CompareValuesReverse(ctx context.Context, plan *planning.Plan, src GraphSource, dst interface {
	RowsByKey(ctx context.Context, table relational.Table, key []relational.Column, columns []relational.Column, keys [][]ir.Value) ([][]ir.Value, error)
}, batchSize int, v *reporting.Verification) error {
	if batchSize <= 0 {
		batchSize = 1000
	}
	issue := issuer(v)
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.Kind != planning.StepRows {
			continue
		}
		check := entityCheck(v, step.Entity)
		if check == nil {
			continue
		}
		idx := compared(plan, step.Entity, step.Properties)
		fields := make([]graph.NodeField, len(idx))
		cols := make([]relational.Column, len(idx))
		for j, k := range idx {
			f := step.Properties[k]
			fields[j] = graph.NodeField{Property: f.Property, Type: f.Type, Element: f.Element}
			cols[j] = relational.Column{Name: f.Column, Type: f.Type, Element: f.Element, NativeType: f.NativeType}
		}
		table := relational.Table{Schema: step.Table.Schema, Name: step.Table.Name}
		keyCols := columnsOf(step.Key)
		var mm mismatches
		var prev string
		first := true
		req := graph.NodeReadRequest{Labels: step.Labels, Key: execution.NodeFields(step.Key), Properties: fields, Limit: batchSize}
		for {
			nodes, err := src.ReadNodes(ctx, req)
			if err != nil {
				return err
			}
			var keep []graph.NodeRecord
			var keys [][]ir.Value
			for _, n := range nodes {
				k := make([]string, len(n.Key))
				for j, x := range n.Key {
					k[j] = x.Text
				}
				joined := strings.Join(k, "\x00")
				// Later duplicates were never written (RTG-EXEC-006).
				if (first || joined != prev) && n.Err == nil {
					keep = append(keep, n)
					keys = append(keys, n.Key)
				}
				prev, first = joined, false
			}
			rows, err := dst.RowsByKey(ctx, table, keyCols, cols, keys)
			if err != nil {
				return err
			}
			for r, n := range keep {
				if rows[r] == nil {
					continue // missing rows are reported elsewhere
				}
				if diff := differing(fields, n.Values, rows[r]); len(diff) > 0 {
					mm.add(n.Key, diff)
				}
			}
			if len(nodes) < batchSize {
				break
			}
			last := nodes[len(nodes)-1]
			req.After = &graph.NodeCursor{Key: last.Key, ElementID: last.ElementID}
		}
		mm.record(check, issue)
	}
	return nil
}

func differing(fields []graph.NodeField, a, b []ir.Value) []string {
	var out []string
	for i, f := range fields {
		if !equalValues(a[i], b[i]) {
			out = append(out, f.Property)
		}
	}
	return out
}

func columnsOf(fields []planning.Field) []relational.Column {
	out := make([]relational.Column, len(fields))
	for i, f := range fields {
		out[i] = relational.Column{Name: f.Column, Type: f.Type, Element: f.Element, NativeType: f.NativeType}
	}
	return out
}

func entityCheck(v *reporting.Verification, entity string) *reporting.EntityCheck {
	for i := range v.Entities {
		if v.Entities[i].Entity == entity {
			return &v.Entities[i]
		}
	}
	return nil
}

func issuer(v *reporting.Verification) func(rtgerr.Code, string, string, ...any) {
	return func(code rtgerr.Code, path, format string, args ...any) {
		v.Issues = append(v.Issues, planning.Diagnostic{Code: string(code), Path: path, Message: fmt.Sprintf(format, args...)})
		if d, ok := rtgerr.Lookup(code); !ok || d.Severity != rtgerr.SeverityWarning {
			v.Status = "failed"
		}
	}
}
