package planning

import (
	"fmt"
	"sort"
	"strings"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
)

// Graph → relational planning (ADR-0006).

func isTemporal(t ir.LogicalType) bool {
	return t == ir.TypeTimestamp || t == ir.TypeTimestampTZ
}

func intPtr(n int) *int { return &n }

func sqlIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func (b *builder) labelMeta(name string) (*schemameta.Label, bool) {
	for i := range b.in.Source.Labels {
		if b.in.Source.Labels[i].Name == name {
			return &b.in.Source.Labels[i], true
		}
	}
	return nil, false
}

func (b *builder) relTypeKnown(name string) bool {
	for _, r := range b.in.Source.RelationshipTypes {
		if r.Name == name {
			return true
		}
	}
	return false
}

func (b *builder) destTable(schema, table string) (*schemameta.Table, bool) {
	if b.in.DestinationMeta == nil {
		return nil, false
	}
	for i := range b.in.DestinationMeta.Schemas {
		s := &b.in.DestinationMeta.Schemas[i]
		if s.Name != schema {
			continue
		}
		for j := range s.Tables {
			if s.Tables[j].Name == table {
				return &s.Tables[j], true
			}
		}
	}
	return nil, false
}

func (b *builder) destSchemaOf(e *mapping.Entity) string {
	if e.Schema != "" {
		return e.Schema
	}
	return b.m.Destination.Schema
}

// revEntity is one entity's resolved reverse plan.
type revEntity struct {
	e      *mapping.Entity
	index  int
	schema string
	table  *schemameta.Table // nil when the plan creates it
	key    []Field
	props  []Field
	ok     bool
}

// checkGraphProperty compares a mapped property with the inspected graph.
func (b *builder) checkGraphProperty(e *mapping.Entity, prop mapping.Property, path string, isKey bool) bool {
	label, ok := b.labelMeta(e.Labels[0])
	if !ok {
		return true // reported once per entity
	}
	var observed []string
	for _, gp := range label.Properties {
		if gp.Name == prop.Property {
			for _, t := range gp.Types {
				observed = append(observed, t.NativeType)
			}
		}
	}
	if len(observed) == 0 {
		b.add(rtgerr.PlanSourceShape, path, "no :%s node in the inspected graph has property %s; it will be NULL", e.Labels[0], prop.Property)
		return true
	}
	accepted := b.in.SourceTypes.ReadableFrom(prop.Type, prop.Element)
	if isKey {
		k, _ := b.in.SourceTypes.KeyStoredAs(prop.Type)
		accepted = []string{k}
	}
	var good, bad []string
	for _, o := range observed {
		if contains(accepted, o) {
			good = append(good, o)
		} else {
			bad = append(bad, o)
		}
	}
	switch {
	case len(good) == 0:
		b.add(rtgerr.PlanTypeMismatch, path, ":%s.%s holds %s values, but the mapping declares %s (readable from %s)",
			e.Labels[0], prop.Property, strings.Join(observed, ", "), typeName(prop.Type, prop.Element), strings.Join(accepted, ", "))
		return false
	case len(bad) > 0:
		b.add(rtgerr.PlanSourceShape, path, ":%s.%s also holds %s values; those records will fail",
			e.Labels[0], prop.Property, strings.Join(bad, ", "))
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// destField resolves a destination column, or derives it when the table is
// created by the plan.
func (b *builder) destField(t *schemameta.Table, schema, table, column, path string, typ, elem ir.LogicalType, notNull bool) (Field, bool) {
	if t == nil {
		dt := b.in.DestinationTypes
		f := Field{Column: column, Type: typ, Element: elem, NativeType: dt.ColumnType(typ, elem), Nullable: !notNull}
		if isTemporal(typ) {
			f.FractionalDigits = intPtr(dt.DefaultFractionalDigits())
		}
		return f, true
	}
	c, ok := columnMeta(t, column)
	if !ok {
		b.add(rtgerr.PlanColumnNotFound, path, "column %q does not exist in destination table %s.%s", column, schema, table)
		return Field{}, false
	}
	if c.TypeInfo.Type == nil {
		b.add(rtgerr.PlanTypeMismatch, path, "destination column %s.%s.%s has type %s, which is not supported", schema, table, column, c.TypeInfo.NativeType)
		return Field{}, false
	}
	var actualElem ir.LogicalType
	if c.TypeInfo.Element != nil && c.TypeInfo.Element.Type != nil {
		actualElem = *c.TypeInfo.Element.Type
	}
	if *c.TypeInfo.Type != typ || actualElem != elem {
		b.add(rtgerr.PlanTypeMismatch, path, "destination column %s.%s.%s is %s (%s), but the mapping declares %s",
			schema, table, column, typeName(*c.TypeInfo.Type, actualElem), c.TypeInfo.NativeType, typeName(typ, elem))
		return Field{}, false
	}
	f := Field{Column: column, Type: typ, Element: elem, NativeType: c.TypeInfo.NativeType, Nullable: c.Nullable}
	if typ == ir.TypeDecimal && c.TypeInfo.Scale != nil {
		f.Scale = intPtr(*c.TypeInfo.Scale)
	}
	if isTemporal(typ) && c.TypeInfo.FractionalDigits != nil {
		f.FractionalDigits = intPtr(*c.TypeInfo.FractionalDigits)
	}
	return f, true
}

// precisionTransformation records an allowed precision loss for a field.
func (b *builder) precisionTransformation(p *Plan, f Field, entity string) {
	limited := (isTemporal(f.Type) && f.FractionalDigits != nil && *f.FractionalDigits < 9) ||
		(f.Type == ir.TypeDecimal && f.Scale != nil)
	if !limited {
		return
	}
	tr := Transformation{Entity: entity, Property: f.Property, Column: f.Column, Type: f.Type, Element: f.Element,
		StoredAs: f.NativeType, LossKind: string(graph.LossPrecision)}
	switch b.m.Policies.Loss {
	case mapping.LossFlexible:
		tr.AllowedBy = "flexible-policy"
	default:
		rule, ok := b.allowRule(graph.LossPrecision, f.Type, f.Element, entity, "", f.Property)
		if !ok {
			return // strict without a rule: values that do not fit fail individually (RTG-EXEC-007)
		}
		tr.AllowedBy, tr.Reason = "allow-rule", rule.Reason
	}
	p.Transformations = append(p.Transformations, tr)
}

func (b *builder) reverseEntity(e *mapping.Entity, index int, p *Plan) *revEntity {
	path := fmt.Sprintf("/entities/%d", index)
	r := &revEntity{e: e, index: index, schema: b.destSchemaOf(e), ok: true}
	for _, l := range e.Labels {
		if _, ok := b.labelMeta(l); !ok {
			b.add(rtgerr.PlanSourceShape, path+"/labels", "label %s has no nodes in the inspected graph", l)
		}
	}
	t, exists := b.destTable(r.schema, e.Table)
	if exists {
		r.table = t
	} else if !b.m.Destination.CreateTables {
		b.add(rtgerr.PlanTableMissing, path+"/table", "destination table %s.%s does not exist; create it or set destination.createTables", r.schema, e.Table)
		r.ok = false
		return r
	}

	keyCols := map[string]bool{}
	for _, k := range e.Key {
		keyCols[k] = true
	}
	for i, prop := range e.Properties {
		ppath := fmt.Sprintf("%s/properties/%d", path, i)
		if len(b.in.SourceTypes.ReadableFrom(prop.Type, prop.Element)) == 0 || b.in.DestinationTypes.ColumnType(prop.Type, prop.Element) == "" {
			b.add(rtgerr.PlanUnsupported, ppath+"/type", "%s values cannot be read from the graph by this engine version yet", typeName(prop.Type, prop.Element))
			r.ok = false
			continue
		}
		if !b.checkGraphProperty(e, prop, ppath, keyCols[prop.Column]) {
			r.ok = false
			continue
		}
		f, ok := b.destField(r.table, r.schema, e.Table, prop.Column, ppath+"/column", prop.Type, prop.Element, keyCols[prop.Column])
		if !ok {
			r.ok = false
			continue
		}
		f.Property = prop.Property
		b.precisionTransformation(p, f, e.Name)
		r.props = append(r.props, f)
	}
	for i, col := range e.Key {
		for _, f := range r.props {
			if f.Column == col {
				if _, permitted := b.in.SourceTypes.KeyStoredAs(f.Type); !permitted {
					b.add(rtgerr.PlanUnsupported, fmt.Sprintf("%s/key/%d", path, i), "%s key properties cannot be read in key order", f.Type)
					r.ok = false
				}
				r.key = append(r.key, f)
			}
		}
	}

	if r.table != nil && r.ok && !hasUniqueKey(r.table, e.Key) {
		b.add(rtgerr.PlanNoUniqueKey, path+"/key", "destination table %s.%s has no primary key or unique constraint on (%s)",
			r.schema, e.Table, strings.Join(e.Key, ", "))
		r.ok = false
	}
	return r
}

func hasUniqueKey(t *schemameta.Table, key []string) bool {
	same := func(cols []string) bool {
		if len(cols) != len(key) {
			return false
		}
		set := map[string]bool{}
		for _, c := range cols {
			set[c] = true
		}
		for _, k := range key {
			if !set[k] {
				return false
			}
		}
		return true
	}
	if t.PrimaryKey != nil && same(t.PrimaryKey.Columns) {
		return true
	}
	for _, u := range t.UniqueConstraints {
		if same(u.Columns) {
			return true
		}
	}
	return false
}

// buildReverse plans a graph → relational migration.
func (b *builder) buildReverse(p *Plan) (*Plan, rtgerr.List) {
	m := b.m
	if b.in.Source == nil || b.in.Source.Kind != schemameta.KindGraph {
		return nil, rtgerr.List{rtgerr.New(rtgerr.Internal, "graph → relational planning needs graph source metadata")}
	}
	if b.in.DestinationMeta == nil || b.in.DestinationMeta.Kind != schemameta.KindRelational {
		return nil, rtgerr.List{rtgerr.New(rtgerr.Internal, "graph → relational planning needs relational destination metadata")}
	}
	if b.in.SourceTypes == nil || b.in.DestinationTypes == nil {
		return nil, rtgerr.List{rtgerr.New(rtgerr.Internal, "graph → relational planning needs source and destination type rules")}
	}
	p.Source = Endpoint{Product: b.in.Source.Database.Product, Database: m.Source.Database}
	p.Destination = Endpoint{Product: b.in.DestinationMeta.Database.Product}

	entities := make([]*revEntity, len(m.Entities))
	byName := map[string]*revEntity{}
	for i := range m.Entities {
		entities[i] = b.reverseEntity(&m.Entities[i], i, p)
		byName[m.Entities[i].Name] = entities[i]
	}

	// Foreign keys.
	var fks []revFK
	for i := range m.Relationships {
		r := &m.Relationships[i]
		path := fmt.Sprintf("/relationships/%d", i)
		if r.Junction != nil {
			b.add(rtgerr.PlanUnsupported, path+"/junction", "junction-table relationships are not implemented yet")
			continue
		}
		if len(r.Properties) > 0 {
			b.add(rtgerr.PlanUnsupported, path+"/properties", "relationship properties are not implemented yet for graph → relational migrations")
			continue
		}
		fk := r.ForeignKey
		owner, ref := byName[fk.Entity], byName[fk.References]
		if !owner.ok || !ref.ok {
			continue
		}
		if !b.relTypeKnown(r.Type) {
			b.add(rtgerr.PlanSourceShape, path+"/type", "no :%s relationship exists in the inspected graph", r.Type)
		}
		load := ForeignKeyLoad{
			Relationship: r.Name, RelationshipType: r.Type, Direction: "outgoing",
			References: ref.e.Name, ReferencedLabels: append([]string{}, ref.e.Labels...), ReferencedKey: ref.key,
		}
		if fk.Direction == mapping.ReferencedToOwner {
			load.Direction = "incoming"
		}
		good := true
		for j, col := range fk.Columns {
			refKey := ref.key[j]
			f, ok := b.destField(owner.table, owner.schema, owner.e.Table, col, fmt.Sprintf("%s/foreignKey/columns/%d", path, j), refKey.Type, refKey.Element, false)
			if !ok {
				good = false
				continue
			}
			load.Columns = append(load.Columns, f)
		}
		if good {
			fks = append(fks, revFK{load: load, owner: owner, ref: ref, index: i})
		}
	}

	// Order: owners after the entities they reference. Self-references and
	// edges inside cycles are deferred to update steps.
	deps := map[string]map[string]bool{}
	for _, f := range fks {
		if f.owner != f.ref {
			if deps[f.owner.e.Name] == nil {
				deps[f.owner.e.Name] = map[string]bool{}
			}
			deps[f.owner.e.Name][f.ref.e.Name] = true
		}
	}
	cyclic := cyclicEdges(m.Entities, deps)
	for i := range fks {
		f := &fks[i]
		if f.owner == f.ref || cyclic[[2]string{f.owner.e.Name, f.ref.e.Name}] {
			f.load.Deferred = true
			for j, c := range f.load.Columns {
				for _, p := range f.owner.props {
					if p.Column == c.Column {
						b.add(rtgerr.PlanUnsupported, fmt.Sprintf("/relationships/%d/foreignKey/columns/%d", f.index, j),
							"column %s is filled after all rows are inserted (self-reference or cycle), so it cannot also be a mapped property", c.Column)
					}
				}
				if !c.Nullable {
					b.add(rtgerr.PlanDeferredNotNull, fmt.Sprintf("/relationships/%d/foreignKey/columns/%d", f.index, j),
						"column %s.%s.%s is filled after all rows are inserted (self-reference or cycle), so it must allow NULL",
						f.owner.schema, f.owner.e.Table, c.Column)
				}
			}
		}
	}
	order := topoOrder(m.Entities, deps, cyclic)

	if b.issues.HasErrors() {
		return nil, b.issues
	}

	// Steps: tables, rows, deferred foreign keys.
	tableStep := map[string]string{}
	created := map[string]bool{}
	for _, name := range order {
		r := byName[name]
		if r.table != nil {
			continue
		}
		id := "table:" + r.schema + "." + r.e.Table
		if _, done := tableStep[r.schema+"."+r.e.Table]; done {
			continue
		}
		var owned []revFK
		for _, f := range fks {
			if f.owner == r {
				owned = append(owned, f)
			}
		}
		sql, warnings := createTableSQL(r, owned2loads(owned, created, r))
		for _, w := range warnings {
			b.add(rtgerr.PlanSourceShape, fmt.Sprintf("/entities/%d/table", r.index), "%s", w)
		}
		created[r.schema+"."+r.e.Table] = true
		tableStep[r.schema+"."+r.e.Table] = id
		p.Steps = append(p.Steps, Step{ID: id, Kind: StepTable, DependsOn: []string{},
			Table: &TableRef{Schema: r.schema, Name: r.e.Table}, SQL: sql})
	}
	for _, name := range order {
		r := byName[name]
		step := Step{
			ID: "rows:" + r.e.Name, Kind: StepRows, DependsOn: []string{},
			Entity: r.e.Name, Labels: append([]string{}, r.e.Labels...),
			Table: &TableRef{Schema: r.schema, Name: r.e.Table}, Key: r.key, Properties: r.props,
		}
		if id, ok := tableStep[r.schema+"."+r.e.Table]; ok {
			step.DependsOn = append(step.DependsOn, id)
		}
		for _, f := range fks {
			if f.owner == r && !f.load.Deferred {
				step.ForeignKeys = append(step.ForeignKeys, f.load)
				step.DependsOn = append(step.DependsOn, "rows:"+f.ref.e.Name)
			}
		}
		step.DependsOn = uniqueStrings(step.DependsOn)
		p.Steps = append(p.Steps, step)
	}
	for _, f := range fks {
		if !f.load.Deferred {
			continue
		}
		deps := uniqueStrings([]string{"rows:" + f.owner.e.Name, "rows:" + f.ref.e.Name})
		p.Steps = append(p.Steps, Step{
			ID: "foreign-keys:" + f.load.Relationship, Kind: StepForeignKeys, DependsOn: deps,
			Entity: f.owner.e.Name, Labels: append([]string{}, f.owner.e.Labels...),
			Table: &TableRef{Schema: f.owner.schema, Name: f.owner.e.Table}, Key: f.owner.key,
			ForeignKeys: []ForeignKeyLoad{f.load},
		})
	}

	for _, issue := range b.issues {
		if issue.Severity() == rtgerr.SeverityWarning {
			p.Warnings = append(p.Warnings, Diagnostic{Code: string(issue.Code), Path: issue.Path, Message: issue.Message})
		}
	}
	return p, b.issues
}

// revFK is a resolved foreign-key relationship.
type revFK struct {
	load  ForeignKeyLoad
	owner *revEntity
	ref   *revEntity
	index int
}

func owned2loads(owned []revFK, created map[string]bool, self *revEntity) []tableFK {
	var out []tableFK
	for _, f := range owned {
		out = append(out, tableFK{load: f.load, refTable: f.ref.schema + "." + f.ref.e.Table, refSchema: f.ref.schema,
			refName: f.ref.e.Table, refKey: f.ref.e.Key,
			// A constraint can reference this table, an existing table, or one created earlier.
			constrain: f.ref == self || f.ref.table != nil || created[f.ref.schema+"."+f.ref.e.Table]})
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// cyclicEdges returns the owner→referenced edges that lie on a cycle.
func cyclicEdges(entities []mapping.Entity, deps map[string]map[string]bool) map[[2]string]bool {
	reach := func(from, to string) bool {
		seen := map[string]bool{}
		stack := []string{from}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n == to {
				return true
			}
			if seen[n] {
				continue
			}
			seen[n] = true
			for d := range deps[n] {
				stack = append(stack, d)
			}
		}
		return false
	}
	out := map[[2]string]bool{}
	for _, e := range entities {
		for d := range deps[e.Name] {
			if reach(d, e.Name) {
				out[[2]string{e.Name, d}] = true
			}
		}
	}
	return out
}

// topoOrder orders entities so that referenced entities come first, keeping
// mapping order among independent ones. Cyclic edges are ignored.
func topoOrder(entities []mapping.Entity, deps map[string]map[string]bool, cyclic map[[2]string]bool) []string {
	done := map[string]bool{}
	var order []string
	for len(order) < len(entities) {
		progressed := false
		for _, e := range entities {
			if done[e.Name] {
				continue
			}
			ready := true
			var refs []string
			for d := range deps[e.Name] {
				refs = append(refs, d)
			}
			sort.Strings(refs)
			for _, d := range refs {
				if !done[d] && !cyclic[[2]string{e.Name, d}] {
					ready = false
				}
			}
			if ready {
				done[e.Name] = true
				order = append(order, e.Name)
				progressed = true
				break // restart, so earlier mapping entries win
			}
		}
		if !progressed {
			break // unreachable: cyclic edges are ignored
		}
	}
	return order
}
