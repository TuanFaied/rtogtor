package planning

import (
	"fmt"
	"strings"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// Input holds everything a plan is built from.
type Input struct {
	Mapping *mapping.Mapping
	// Source is the inspected source metadata: relational schemas for
	// relational → graph, labels and relationship types for graph → relational.
	Source *schemameta.Document
	// Destination describes how a graph destination stores logical types
	// (relational → graph).
	Destination graph.TypeSupport
	// DestinationMeta is the inspected relational destination
	// (graph → relational): the tables that already exist.
	DestinationMeta *schemameta.Document
	// SourceTypes and DestinationTypes describe a graph source and a
	// relational destination (graph → relational).
	SourceTypes      graph.TypeSupport
	DestinationTypes relational.TypeSupport
}

// Build validates the mapping against the source and destination and
// returns a plan. The list holds every error and warning found; the plan is
// nil if the list contains errors.
func Build(in Input) (*Plan, rtgerr.List) {
	b := &builder{in: in, m: in.Mapping}
	return b.build()
}

type builder struct {
	in     Input
	m      *mapping.Mapping
	issues rtgerr.List
}

func (b *builder) add(code rtgerr.Code, path, format string, args ...any) {
	b.issues = append(b.issues, rtgerr.New(code, format, args...).At(path))
}

func (b *builder) build() (*Plan, rtgerr.List) {
	m := b.m
	hash, err := m.Hash()
	if err != nil {
		return nil, rtgerr.List{rtgerr.New(rtgerr.Internal, "hashing the mapping: %v", err)}
	}

	// Features this engine version cannot execute yet.
	if m.Identity.Strategy != mapping.StrategyHash {
		b.add(rtgerr.PlanUnsupported, "/identity/strategy", "identity strategy %q is not implemented yet; use \"hash\"", m.Identity.Strategy)
	}
	if b.issues.HasErrors() {
		return nil, b.issues
	}
	if m.Direction == mapping.GraphToRelational {
		return b.buildReverse(&Plan{
			SpecVersion: spec.Version, IdentityAlgorithm: spec.IdentityAlgorithm,
			Mapping: MappingRef{Name: m.Name, Hash: hash}, Direction: m.Direction,
			IdentityProperty: m.Identity.Property, LossPolicy: m.Policies.Loss,
			Steps: []Step{}, Transformations: []Transformation{}, Warnings: []Diagnostic{},
		})
	}
	if b.in.Source == nil || b.in.Source.Kind != schemameta.KindRelational {
		return nil, rtgerr.List{rtgerr.New(rtgerr.Internal, "planning needs relational source metadata")}
	}

	p := &Plan{
		SpecVersion:       spec.Version,
		IdentityAlgorithm: spec.IdentityAlgorithm,
		Mapping:           MappingRef{Name: m.Name, Hash: hash},
		Direction:         m.Direction,
		Source:            Endpoint{Product: b.in.Source.Database.Product},
		Destination:       Endpoint{Product: b.in.Destination.Product(), Database: m.Destination.Database},
		IdentityProperty:  m.Identity.Property,
		LossPolicy:        m.Policies.Loss,
		Steps:             []Step{},
		Transformations:   []Transformation{},
		Warnings:          []Diagnostic{},
	}

	// One identity constraint per distinct primary label, in mapping order.
	constraintFor := map[string]string{}
	for _, e := range m.Entities {
		label := e.Labels[0]
		if _, ok := constraintFor[label]; ok {
			continue
		}
		id := "constraint:" + label
		constraintFor[label] = id
		p.Steps = append(p.Steps, Step{
			ID: id, Kind: StepConstraint, DependsOn: []string{},
			Label: label, Property: m.Identity.Property,
		})
	}

	// Node loads.
	nodeStep := map[string]string{}
	for i := range m.Entities {
		e := &m.Entities[i]
		path := fmt.Sprintf("/entities/%d", i)
		step, ok := b.nodeStep(e, path, p)
		if !ok {
			continue
		}
		step.DependsOn = []string{constraintFor[e.Labels[0]]}
		nodeStep[e.Name] = step.ID
		p.Steps = append(p.Steps, *step)
	}

	// Relationship loads, after every node load they connect.
	for i := range m.Relationships {
		r := &m.Relationships[i]
		path := fmt.Sprintf("/relationships/%d", i)
		step, ok := b.relationshipStep(r, path, p)
		if !ok {
			continue
		}
		deps := []string{nodeStep[step.From.Entity]}
		if step.To.Entity != step.From.Entity {
			deps = append(deps, nodeStep[step.To.Entity])
		}
		step.DependsOn = deps
		p.Steps = append(p.Steps, *step)
	}

	for _, issue := range b.issues {
		if issue.Severity() == rtgerr.SeverityWarning {
			p.Warnings = append(p.Warnings, Diagnostic{Code: string(issue.Code), Path: issue.Path, Message: issue.Message})
		}
	}
	if b.issues.HasErrors() {
		return nil, b.issues
	}
	return p, b.issues
}

// tableMeta finds a table in the source metadata.
func (b *builder) tableMeta(schema, table string) (*schemameta.Table, bool) {
	for i := range b.in.Source.Schemas {
		s := &b.in.Source.Schemas[i]
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

func columnMeta(t *schemameta.Table, name string) (*schemameta.Column, bool) {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i], true
		}
	}
	return nil, false
}

func (b *builder) schemaOf(e *mapping.Entity) string {
	if e.Schema != "" {
		return e.Schema
	}
	return b.m.Source.Schema
}

func typeName(t ir.LogicalType, elem ir.LogicalType) string {
	if t == ir.TypeArray {
		return "array<" + string(elem) + ">"
	}
	return string(t)
}

// field resolves a column against the metadata and checks its declared type.
// declared may be empty when the type is implied (foreign-key columns).
func (b *builder) field(t *schemameta.Table, schema, column, path string, declared, declaredElem ir.LogicalType) (Field, bool) {
	c, ok := columnMeta(t, column)
	if !ok {
		b.add(rtgerr.PlanColumnNotFound, path, "column %q does not exist in table %s.%s", column, schema, t.Name)
		return Field{}, false
	}
	if c.TypeInfo.Type == nil {
		b.add(rtgerr.PlanTypeMismatch, path, "column %s.%s.%s has type %s, which is not supported in this specification version",
			schema, t.Name, column, c.TypeInfo.NativeType)
		return Field{}, false
	}
	actual := *c.TypeInfo.Type
	var actualElem ir.LogicalType
	if c.TypeInfo.Element != nil && c.TypeInfo.Element.Type != nil {
		actualElem = *c.TypeInfo.Element.Type
	}
	if declared != "" && (declared != actual || declaredElem != actualElem) {
		b.add(rtgerr.PlanTypeMismatch, path, "column %s.%s.%s is %s (%s) in the source, but the mapping declares %s",
			schema, t.Name, column, typeName(actual, actualElem), c.TypeInfo.NativeType, typeName(declared, declaredElem))
		return Field{}, false
	}
	return Field{
		Column: column, Type: actual, Element: actualElem,
		NativeType: c.TypeInfo.NativeType, Nullable: c.Nullable,
	}, true
}

// properties resolves mapped properties and applies the loss policy.
func (b *builder) properties(t *schemameta.Table, schema string, props []mapping.Property, path, entity, relationship string, p *Plan) ([]Field, bool) {
	ok := true
	out := make([]Field, 0, len(props))
	for i, prop := range props {
		ppath := fmt.Sprintf("%s/%d", path, i)
		f, good := b.field(t, schema, prop.Column, ppath+"/column", prop.Type, prop.Element)
		if !good {
			ok = false
			continue
		}
		f.Property = prop.Property
		dest := b.in.Destination
		if !dest.Supports(f.Type, f.Element) {
			b.add(rtgerr.PlanUnsupported, ppath+"/type", "%s values cannot be written to %s by this engine version yet",
				typeName(f.Type, f.Element), dest.Product())
			ok = false
			continue
		}
		if kind := dest.StorageLoss(f.Type, f.Element); kind != graph.LossNone {
			tr := Transformation{
				Entity: entity, Relationship: relationship, Property: prop.Property, Column: prop.Column,
				Type: f.Type, Element: f.Element, StoredAs: dest.StoredAs(f.Type, f.Element), LossKind: string(kind),
			}
			if b.m.Policies.Loss == mapping.LossFlexible {
				tr.AllowedBy = "flexible-policy"
			} else if rule, found := b.allowRule(kind, f.Type, f.Element, entity, relationship, prop.Property); found {
				tr.AllowedBy, tr.Reason = "allow-rule", rule.Reason
			} else {
				b.add(rtgerr.PlanLossNotAllowed, ppath, "storing %s as %s in %s is a %s loss; add a policies.allow entry (kind: %s) or use the flexible policy",
					typeName(f.Type, f.Element), tr.StoredAs, dest.Product(), kind, kind)
				ok = false
				continue
			}
			p.Transformations = append(p.Transformations, tr)
		}
		out = append(out, f)
	}
	return out, ok
}

// allowRule finds a policies.allow entry covering the loss. Every field set
// on a rule narrows it; for arrays, a rule's type may name the array or its
// element type.
func (b *builder) allowRule(kind graph.LossKind, t, elem ir.LogicalType, entity, relationship, property string) (mapping.LossAllowance, bool) {
	owner := entity
	if relationship != "" {
		owner = relationship
	}
	for _, a := range b.m.Policies.Allow {
		if a.Kind != string(kind) {
			continue
		}
		if a.Type != "" && a.Type != t && (t != ir.TypeArray || a.Type != elem) {
			continue
		}
		if a.Entity != "" && a.Entity != owner {
			continue
		}
		if a.Property != "" && a.Property != property {
			continue
		}
		return a, true
	}
	return mapping.LossAllowance{}, false
}

// keyFields resolves an entity's key columns. warn controls the
// nullable-key warning, so it is reported once per entity.
func (b *builder) keyFields(e *mapping.Entity, t *schemameta.Table, schema, path string, warn bool) ([]Field, bool) {
	ok := true
	out := make([]Field, 0, len(e.Key))
	for i, col := range e.Key {
		prop, _ := e.PropertyByColumn(col) // guaranteed by mapping validation
		kpath := fmt.Sprintf("%s/key/%d", path, i)
		f, good := b.field(t, schema, col, kpath, prop.Type, prop.Element)
		if !good {
			ok = false
			continue
		}
		f.Property = prop.Property
		if f.Nullable && warn {
			b.add(rtgerr.PlanNullableKey, kpath, "key column %s.%s.%s allows NULL; rows with a NULL key will be recorded as failed records",
				schema, t.Name, col)
		}
		out = append(out, f)
	}
	return out, ok
}

func (b *builder) nodeStep(e *mapping.Entity, path string, p *Plan) (*Step, bool) {
	schema := b.schemaOf(e)
	t, ok := b.tableMeta(schema, e.Table)
	if !ok {
		b.add(rtgerr.PlanTableNotFound, path+"/table", "table %s.%s does not exist in the inspected source", schema, e.Table)
		return nil, false
	}
	key, kok := b.keyFields(e, t, schema, path, true)
	props, pok := b.properties(t, schema, e.Properties, path+"/properties", e.Name, "", p)
	if !kok || !pok {
		return nil, false
	}
	return &Step{
		ID: "nodes:" + e.Name, Kind: StepNodes,
		Entity: e.Name, Table: &TableRef{Schema: schema, Name: e.Table},
		Labels: append([]string{}, e.Labels...), Key: key, Properties: props,
	}, true
}

func (b *builder) relationshipStep(r *mapping.Relationship, path string, p *Plan) (*Step, bool) {
	if r.Junction != nil {
		b.add(rtgerr.PlanUnsupported, path+"/junction", "junction-table relationships are not implemented yet; map the junction table as an entity with two foreign-key relationships")
		return nil, false
	}
	fk := r.ForeignKey
	owner, _ := b.m.Entity(fk.Entity) // guaranteed by mapping validation
	target, _ := b.m.Entity(fk.References)
	schema := b.schemaOf(owner)
	t, ok := b.tableMeta(schema, owner.Table)
	if !ok {
		// Already reported by the owner's node step.
		return nil, false
	}
	key, kok := b.keyFields(owner, t, schema, path+"/foreignKey", false)
	if !kok {
		return nil, false
	}

	// Foreign-key columns must have the referenced key's types, position by
	// position, or identities could never match.
	fkFields := make([]Field, 0, len(fk.Columns))
	fkOK := true
	for i, col := range fk.Columns {
		cpath := fmt.Sprintf("%s/foreignKey/columns/%d", path, i)
		f, good := b.field(t, schema, col, cpath, "", "")
		if !good {
			fkOK = false
			continue
		}
		refProp, _ := target.PropertyByColumn(target.Key[i])
		if f.Type != refProp.Type {
			b.add(rtgerr.PlanForeignKeyTypes, cpath, "foreign-key column %s is %s, but key field %s of %s is %s",
				col, f.Type, target.Key[i], target.Name, refProp.Type)
			fkOK = false
			continue
		}
		fkFields = append(fkFields, f)
	}
	props, pok := b.properties(t, schema, r.Properties, path+"/properties", "", r.Name, p)
	if !fkOK || !pok {
		return nil, false
	}

	ownerRef := &NodeRef{Entity: owner.Name, Label: owner.Labels[0]}
	targetRef := &NodeRef{Entity: target.Name, Label: target.Labels[0]}
	from, to := ownerRef, targetRef
	if fk.Direction == mapping.ReferencedToOwner {
		from, to = targetRef, ownerRef
	}
	return &Step{
		ID: "relationships:" + r.Name, Kind: StepRelationships,
		Entity: owner.Name, Table: &TableRef{Schema: schema, Name: owner.Table},
		Key: key, Properties: props,
		Relationship: r.Name, RelationshipType: r.Type, From: from, To: to,
		ForeignKey:    &ForeignKeyRef{Columns: fkFields, References: target.Name, Direction: fk.Direction},
		SelfReference: owner.Name == target.Name,
	}, true
}

// Text renders a human-readable plan.
func (p *Plan) Text() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plan for mapping %q (%s)\n", p.Mapping.Name, p.Mapping.Hash[:12])
	fmt.Fprintf(&sb, "%s -> %s, identity property %s (%s), loss policy %s\n\n",
		p.Source.Product, p.Destination.Product, p.IdentityProperty, p.IdentityAlgorithm, p.LossPolicy)
	for i, s := range p.Steps {
		fmt.Fprintf(&sb, "%2d. ", i+1)
		switch s.Kind {
		case StepConstraint:
			fmt.Fprintf(&sb, "Ensure unique constraint on :%s(%s)\n", s.Label, s.Property)
		case StepNodes:
			fmt.Fprintf(&sb, "Load %s.%s as (:%s) nodes, keyed by (%s), %d properties\n",
				s.Table.Schema, s.Table.Name, strings.Join(s.Labels, ":"), columns(s.Key), len(s.Properties))
		case StepRelationships:
			self := ""
			if s.SelfReference {
				self = " [self-reference]"
			}
			fmt.Fprintf(&sb, "Create (:%s)-[:%s]->(:%s) from %s.%s(%s)%s\n",
				s.From.Label, s.RelationshipType, s.To.Label, s.Table.Schema, s.Table.Name, columns(s.ForeignKey.Columns), self)
		case StepTable:
			fmt.Fprintf(&sb, "Create table %s.%s if it does not exist:\n", s.Table.Schema, s.Table.Name)
			for _, line := range strings.Split(s.SQL, "\n") {
				fmt.Fprintf(&sb, "      %s\n", line)
			}
		case StepRows:
			fks := ""
			for _, l := range s.ForeignKeys {
				fks += fmt.Sprintf(", %s from :%s", columns(l.Columns), l.RelationshipType)
			}
			fmt.Fprintf(&sb, "Write (:%s) nodes as rows of %s.%s, keyed by (%s), %d columns%s\n",
				strings.Join(s.Labels, ":"), s.Table.Schema, s.Table.Name, columns(s.Key), len(s.Properties), fks)
		case StepForeignKeys:
			l := s.ForeignKeys[0]
			fmt.Fprintf(&sb, "Fill %s.%s(%s) from :%s relationships [deferred: self-reference or cycle]\n",
				s.Table.Schema, s.Table.Name, columns(l.Columns), l.RelationshipType)
		}
	}
	if len(p.Transformations) > 0 {
		sb.WriteString("\nLossy transformations:\n")
		for _, t := range p.Transformations {
			owner := t.Entity
			if t.Relationship != "" {
				owner = t.Relationship
			}
			fmt.Fprintf(&sb, "  - %s.%s: %s stored as %s (%s loss), allowed by %s", owner, t.Property,
				typeName(t.Type, t.Element), t.StoredAs, t.LossKind, t.AllowedBy)
			if t.Reason != "" {
				fmt.Fprintf(&sb, ": %s", t.Reason)
			}
			sb.WriteString("\n")
		}
	}
	if len(p.Warnings) > 0 {
		sb.WriteString("\nWarnings:\n")
		for _, w := range p.Warnings {
			fmt.Fprintf(&sb, "  - %s at %s: %s\n", w.Code, w.Path, w.Message)
		}
	}
	return sb.String()
}

func columns(fields []Field) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Column
	}
	return strings.Join(names, ", ")
}
