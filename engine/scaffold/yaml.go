package scaffold

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

// YAML renders the draft as a commented mapping document and checks that it
// decodes back to a valid mapping.
func (d *Draft) YAML() ([]byte, error) {
	var w writer
	m := d.Mapping
	w.line(0, "# RtoGtoR mapping drafted by `rtogtor scaffold`. Review it before migrating:")
	w.line(0, "# names are derived mechanically from the database, and every choice the")
	w.line(0, "# scaffold had to make is noted below. Check it with `rtogtor plan`.")
	for _, n := range d.Notes {
		w.comment(0, n)
	}
	w.line(0, "specVersion: "+quote(m.SpecVersion, true))
	w.kv(0, "name", m.Name)
	if m.Description != "" {
		w.kv(0, "description", m.Description)
	}
	w.kv(0, "direction", string(m.Direction))
	w.blank()
	w.endpoint("source", m.Source)
	w.endpoint("destination", m.Destination)
	w.blank()
	w.line(0, "identity:")
	w.kv(1, "strategy", m.Identity.Strategy)
	w.kv(1, "property", m.Identity.Property)
	w.blank()
	w.line(0, "policies:")
	w.kv(1, "loss", m.Policies.Loss)
	if len(m.Policies.Allow) > 0 {
		w.line(1, "allow:")
		for _, a := range m.Policies.Allow {
			w.line(2, "- kind: "+quote(a.Kind, false))
			if a.Type != "" {
				w.kv(3, "type", string(a.Type))
			}
			if a.Entity != "" {
				w.kv(3, "entity", a.Entity)
			}
			if a.Property != "" {
				w.kv(3, "property", a.Property)
			}
			w.kv(3, "reason", a.Reason)
		}
	}
	w.blank()
	w.line(0, "entities:")
	for i, e := range m.Entities {
		if i > 0 {
			w.blank()
		}
		for _, n := range d.EntityNotes[e.Name] {
			w.comment(1, n)
		}
		w.line(1, "- name: "+quote(e.Name, false))
		if e.Description != "" {
			w.kv(2, "description", e.Description)
		}
		if e.Schema != "" {
			w.kv(2, "schema", e.Schema)
		}
		w.kv(2, "table", e.Table)
		w.line(2, "labels: "+flow(e.Labels))
		w.line(2, "key: "+flow(e.Key))
		w.line(2, "properties:")
		for _, p := range e.Properties {
			w.line(3, "- "+property(p))
		}
	}
	if len(m.Relationships) > 0 {
		w.blank()
		w.line(0, "relationships:")
		for i, r := range m.Relationships {
			if i > 0 {
				w.blank()
			}
			w.line(1, "- name: "+quote(r.Name, false))
			if r.Description != "" {
				w.kv(2, "description", r.Description)
			}
			w.kv(2, "type", r.Type)
			if fk := r.ForeignKey; fk != nil {
				w.line(2, "foreignKey:")
				w.kv(3, "entity", fk.Entity)
				w.line(3, "columns: "+flow(fk.Columns))
				w.kv(3, "references", fk.References)
				w.kv(3, "direction", fk.Direction)
			}
			if j := r.Junction; j != nil {
				w.line(2, "junction:")
				if j.Schema != "" {
					w.kv(3, "schema", j.Schema)
				}
				w.kv(3, "table", j.Table)
				w.line(3, fmt.Sprintf("from: { entity: %s, columns: %s }", quote(j.From.Entity, false), flow(j.From.Columns)))
				w.line(3, fmt.Sprintf("to: { entity: %s, columns: %s }", quote(j.To.Entity, false), flow(j.To.Columns)))
			}
			if len(r.Properties) > 0 {
				w.line(2, "properties:")
				for _, p := range r.Properties {
					w.line(3, "- "+property(p))
				}
			}
		}
	}
	out := []byte(w.sb.String())

	// The document must decode to the same, valid mapping.
	back, issues := mapping.Decode(out, mapping.FormatYAML)
	if issues.HasErrors() {
		return nil, rtgerr.New(rtgerr.Internal, "scaffold produced an invalid mapping: %v", issues)
	}
	h1, err1 := back.Hash()
	h2, err2 := m.Hash()
	if err1 != nil || err2 != nil || h1 != h2 {
		return nil, rtgerr.New(rtgerr.Internal, "scaffold YAML does not round-trip")
	}
	return out, nil
}

type writer struct{ sb strings.Builder }

func (w *writer) line(indent int, s string) {
	w.sb.WriteString(strings.Repeat("  ", indent))
	w.sb.WriteString(s)
	w.sb.WriteByte('\n')
}

func (w *writer) blank() { w.sb.WriteByte('\n') }

func (w *writer) comment(indent int, s string) {
	for _, l := range strings.Split(s, "\n") {
		w.line(indent, "# "+l)
	}
}

func (w *writer) kv(indent int, k, v string) { w.line(indent, k+": "+quote(v, false)) }

func (w *writer) endpoint(name string, e mapping.Endpoint) {
	w.line(0, name+":")
	w.kv(1, "kind", e.Kind)
	if e.Schema != "" {
		w.kv(1, "schema", e.Schema)
	}
	if e.Database != "" {
		w.kv(1, "database", e.Database)
	}
	if e.CreateTables {
		w.line(1, "createTables: true")
	}
}

func property(p mapping.Property) string {
	s := fmt.Sprintf("{ column: %s, property: %s, type: %s", quote(p.Column, false), quote(p.Property, false), p.Type)
	if p.Element != "" {
		s += ", element: " + string(p.Element)
	}
	return s + " }"
}

func flow(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = quote(s, false)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

var plainScalar = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_\-]*$`)

// reserved are plain scalars YAML 1.1 or 1.2 would read as something other
// than a string.
var reserved = map[string]bool{"true": true, "false": true, "null": true, "yes": true, "no": true,
	"on": true, "off": true, "y": true, "n": true, "~": true}

// quote returns s as a YAML scalar: plain when that is unambiguous, else a
// JSON string (valid YAML double-quoted).
func quote(s string, always bool) string {
	if !always && plainScalar.MatchString(s) && !reserved[strings.ToLower(s)] {
		return s
	}
	b, _ := json.Marshal(s)
	return string(b)
}
