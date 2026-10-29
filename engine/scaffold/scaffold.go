// Package scaffold writes starting mappings: from an inspected relational
// schema (relational → graph), or by reversing an existing mapping.
//
// A scaffold is a draft for a person to review, never a migration to run
// blindly: names are derived mechanically, every choice the scaffold had to
// make is written as a comment, and the result is validated before it is
// returned.
package scaffold

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph/neo4j"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// Draft is a scaffolded mapping with the notes explaining its choices.
type Draft struct {
	Mapping *mapping.Mapping
	// Notes apply to the whole mapping; EntityNotes to one entity, by name.
	Notes       []string
	EntityNotes map[string][]string
}

func (d *Draft) note(entity, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if entity == "" {
		d.Notes = append(d.Notes, msg)
		return
	}
	d.EntityNotes[entity] = append(d.EntityNotes[entity], msg)
}

// words splits snake_case, kebab-case, spaces and camelCase into lowercase
// words.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

func title(w string) string {
	if w == "" {
		return w
	}
	r := []rune(w)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func pascal(s string) string {
	var sb strings.Builder
	for _, w := range words(s) {
		sb.WriteString(title(w))
	}
	return sb.String()
}

func camel(s string) string {
	ws := words(s)
	if len(ws) == 0 {
		return s
	}
	var sb strings.Builder
	sb.WriteString(ws[0])
	for _, w := range ws[1:] {
		sb.WriteString(title(w))
	}
	return sb.String()
}

func upperSnake(s string) string { return strings.ToUpper(strings.Join(words(s), "_")) }

// singular makes a plural table name's last word singular, conservatively:
// unknown endings are kept rather than guessed.
func singular(s string) string {
	ws := words(s)
	if len(ws) == 0 {
		return s
	}
	last := ws[len(ws)-1]
	switch {
	case strings.HasSuffix(last, "ies") && len(last) > 3:
		last = last[:len(last)-3] + "y"
	case strings.HasSuffix(last, "sses"), strings.HasSuffix(last, "xes"), strings.HasSuffix(last, "ches"), strings.HasSuffix(last, "shes"):
		last = last[:len(last)-2]
	case strings.HasSuffix(last, "s") && !strings.HasSuffix(last, "ss") && !strings.HasSuffix(last, "us") && !strings.HasSuffix(last, "is") && len(last) > 1:
		last = last[:len(last)-1]
	}
	ws[len(ws)-1] = last
	return strings.Join(ws, "_")
}

// Options configures FromRelational.
type Options struct {
	// Name of the mapping; defaults to the database name.
	Name string
}

type tableInfo struct {
	schema string
	t      *schemameta.Table
	entity string
	key    []string
}

// FromRelational drafts a relational → graph mapping for every table with a
// usable key in doc: one entity (label) per table, one property per column
// of a supported type, and one relationship per foreign key between mapped
// tables.
func FromRelational(doc *schemameta.Document, opts Options) (*Draft, error) {
	if doc == nil || doc.Kind != schemameta.KindRelational {
		return nil, rtgerr.New(rtgerr.Internal, "scaffolding needs relational schema metadata")
	}
	d := &Draft{EntityNotes: map[string][]string{}}
	name := opts.Name
	if name == "" {
		name = strings.Join(words(doc.Database.Name), "-")
	}
	if name == "" {
		name = "scaffold"
	}
	m := &mapping.Mapping{
		SpecVersion: spec.Version, Name: name, Direction: mapping.RelationalToGraph,
		Description: "Drafted by rtogtor scaffold from database " + doc.Database.Name + "; review every entity before migrating.",
		Source:      mapping.Endpoint{Kind: "postgresql"}, Destination: mapping.Endpoint{Kind: "neo4j"},
		Identity: mapping.Identity{Strategy: mapping.StrategyHash, Property: mapping.DefaultIdentityProperty},
		Policies: mapping.Policies{Loss: mapping.LossStrict},
	}
	d.Mapping = m
	if len(doc.Schemas) > 0 {
		m.Source.Schema = doc.Schemas[0].Name
	}

	// Pass 1: tables that can be entities.
	var tables []*tableInfo
	byTable := map[string]*tableInfo{}
	used := map[string]bool{}
	for si := range doc.Schemas {
		s := &doc.Schemas[si]
		for ti := range s.Tables {
			t := &s.Tables[ti]
			info := &tableInfo{schema: s.Name, t: t}
			full := s.Name + "." + t.Name
			switch {
			case t.PrimaryKey != nil:
				info.key = t.PrimaryKey.Columns
			default:
				for _, u := range t.UniqueConstraints {
					if allNotNull(t, u.Columns) {
						info.key = u.Columns
						break
					}
				}
			}
			if info.key == nil {
				d.note("", "table %s is not mapped: it has no primary key or unique key on NOT NULL columns", full)
				continue
			}
			if bad := badKeyColumn(t, info.key); bad != "" {
				d.note("", "table %s is not mapped: key column %s has a type that cannot be part of an identity", full, bad)
				continue
			}
			entity := pascal(singular(t.Name))
			for base, n := entity, 2; used[entity] || entity == ""; n++ {
				entity = fmt.Sprintf("%s%d", base, n)
			}
			used[entity] = true
			info.entity = entity
			tables = append(tables, info)
			byTable[full] = info
		}
	}

	// Pass 2: entities, properties and relationships.
	reps := map[ir.LogicalType]bool{}
	relNames := map[string]bool{}
	types := neo4j.TypeSupport{}
	for _, info := range tables {
		t := info.t
		e := mapping.Entity{Name: info.entity, Table: t.Name, Labels: []string{info.entity}, Key: info.key}
		if info.schema != m.Source.Schema {
			e.Schema = info.schema
		}
		if t.PrimaryKey == nil {
			d.note(info.entity, "no primary key: keyed by the unique constraint on (%s)", strings.Join(info.key, ", "))
		}
		if singular(t.Name) == t.Name {
			d.note(info.entity, "entity and label named after table %s; rename them if they should be singular", t.Name)
		}
		// Foreign keys to mapped tables become relationships, so their
		// columns are not also properties unless they are part of the key.
		relCols := map[string]bool{}
		var rels []mapping.Relationship
		for _, fk := range t.ForeignKeys {
			ref, ok := byTable[fk.ReferencedSchema+"."+fk.ReferencedTable]
			if !ok || !sameColumns(fk.ReferencedColumns, ref.key) {
				d.note(info.entity, "foreign key (%s) to %s.%s is kept as properties: the referenced table is not mapped or is not referenced by its key",
					strings.Join(fk.Columns, ", "), fk.ReferencedSchema, fk.ReferencedTable)
				continue
			}
			// Order the columns as the referenced key.
			cols := make([]string, len(ref.key))
			for i, rk := range ref.key {
				for j, rc := range fk.ReferencedColumns {
					if rc == rk {
						cols[i] = fk.Columns[j]
					}
				}
			}
			typ := relType(cols, ref.entity)
			rname := info.entity + pascal(strings.ToLower(typ))
			for base, n := rname, 2; relNames[rname]; n++ {
				rname = fmt.Sprintf("%s%d", base, n)
			}
			relNames[rname] = true
			rels = append(rels, mapping.Relationship{Name: rname, Type: typ, ForeignKey: &mapping.ForeignKey{
				Entity: info.entity, Columns: cols, References: ref.entity, Direction: mapping.OwnerToReferenced}})
			for _, c := range cols {
				relCols[c] = true
			}
		}
		key := map[string]bool{}
		for _, k := range info.key {
			key[k] = true
		}
		props := map[string]bool{}
		for _, c := range t.Columns {
			if relCols[c.Name] && !key[c.Name] {
				continue
			}
			if c.TypeInfo.Type == nil {
				d.note(info.entity, "column %s (%s) is not mapped: its type is not supported", c.Name, c.TypeInfo.NativeType)
				continue
			}
			typ := *c.TypeInfo.Type
			var elem ir.LogicalType
			if c.TypeInfo.Element != nil && c.TypeInfo.Element.Type != nil {
				elem = *c.TypeInfo.Element.Type
			}
			if !types.Supports(typ, elem) {
				d.note(info.entity, "column %s (%s) is not mapped: %s values cannot be written to Neo4j yet", c.Name, c.TypeInfo.NativeType, typ)
				continue
			}
			if types.StorageLoss(typ, elem) != "" {
				lossType := typ
				if typ == ir.TypeArray {
					lossType = elem
				}
				reps[lossType] = true
			}
			prop := camel(c.Name)
			for base, n := prop, 2; props[prop] || prop == m.Identity.Property; n++ {
				prop = fmt.Sprintf("%s%d", base, n)
			}
			props[prop] = true
			e.Properties = append(e.Properties, mapping.Property{Column: c.Name, Property: prop, Type: typ, Element: elem})
		}
		m.Entities = append(m.Entities, e)
		m.Relationships = append(m.Relationships, rels...)
	}
	if len(m.Entities) == 0 {
		return nil, rtgerr.New(rtgerr.SchemaNotFound, "no table can be mapped: every table lacks a usable key")
	}

	// Representation losses are unavoidable for these types in Neo4j; the
	// strict policy needs them allowed explicitly.
	var lossTypes []string
	for t := range reps {
		lossTypes = append(lossTypes, string(t))
	}
	sort.Strings(lossTypes)
	for _, t := range lossTypes {
		m.Policies.Allow = append(m.Policies.Allow, mapping.LossAllowance{Kind: "representation", Type: ir.LogicalType(t),
			Reason: fmt.Sprintf("Neo4j has no native %s type; values are stored as canonical strings (added by rtogtor scaffold; review)", t)})
	}
	return d, nil
}

func relType(cols []string, refEntity string) string {
	if len(cols) == 1 {
		w := words(cols[0])
		if len(w) > 1 && w[len(w)-1] == "id" {
			w = w[:len(w)-1]
		}
		if t := strings.ToUpper(strings.Join(w, "_")); t != "" && t != "ID" {
			return t
		}
	}
	return "HAS_" + upperSnake(refEntity)
}

func allNotNull(t *schemameta.Table, cols []string) bool {
	for _, c := range cols {
		col, ok := column(t, c)
		if !ok || col.Nullable {
			return false
		}
	}
	return true
}

func badKeyColumn(t *schemameta.Table, cols []string) string {
	for _, c := range cols {
		col, ok := column(t, c)
		if !ok || col.TypeInfo.Type == nil || !col.TypeInfo.Type.IdentityPermitted() {
			return c
		}
	}
	return ""
}

func column(t *schemameta.Table, name string) (*schemameta.Column, bool) {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i], true
		}
	}
	return nil, false
}

func sameColumns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if !set[x] {
			return false
		}
	}
	return true
}

// Reverse drafts the graph → relational mapping that reads back what m
// writes: the same entities and relationships, with source and destination
// swapped. The destination schema is m's source schema, and tables that do
// not exist are created (createTables), since a reversed mapping often
// targets a new database.
func Reverse(m *mapping.Mapping) (*Draft, error) {
	if m.Direction != mapping.RelationalToGraph {
		return nil, rtgerr.New(rtgerr.CLIUsage, "only relational-to-graph mappings can be reversed; %q is %s", m.Name, m.Direction)
	}
	d := &Draft{EntityNotes: map[string][]string{}}
	r := *m
	r.SpecVersion = spec.Version
	r.Name = m.Name + "-reverse"
	r.Description = "The reverse of mapping " + m.Name + ", drafted by rtogtor scaffold --reverse; review before migrating."
	r.Direction = mapping.GraphToRelational
	r.Source = mapping.Endpoint{Kind: m.Destination.Kind, Database: m.Destination.Database}
	r.Destination = mapping.Endpoint{Kind: m.Source.Kind, Schema: m.Source.Schema, CreateTables: true}
	// Reading values back loses nothing the forward direction did not, so
	// the forward allowances are not needed; precision limits of existing
	// tables are checked per value (RTG-EXEC-007).
	r.Policies = mapping.Policies{Loss: m.Policies.Loss}
	r.Entities = append([]mapping.Entity{}, m.Entities...)
	r.Relationships = nil
	for _, rel := range m.Relationships {
		switch {
		case rel.Junction != nil:
			d.note("", "relationship %s is left out: junction-table relationships cannot be reversed yet", rel.Name)
		case len(rel.Properties) > 0:
			d.note("", "relationship %s is left out: relationship properties cannot be reversed yet", rel.Name)
		default:
			r.Relationships = append(r.Relationships, rel)
		}
	}
	d.note("", "tables that do not exist in the destination are created (createTables); existing ones are checked, never altered")
	d.Mapping = &r
	return d, nil
}
