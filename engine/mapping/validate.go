package mapping

import (
	"fmt"

	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

// ExecutableDirections lists the directions this engine version can execute.
// Valid mappings in other directions produce an RTG-MAP-010 warning.
var ExecutableDirections = map[Direction]bool{RelationalToGraph: true, GraphToRelational: true}

// validateSemantics runs checks that JSON Schema cannot express.
func validateSemantics(m *Mapping) rtgerr.List {
	var issues rtgerr.List
	add := func(code rtgerr.Code, path, format string, args ...any) {
		issues = append(issues, rtgerr.New(code, format, args...).At(path))
	}

	if !ExecutableDirections[m.Direction] {
		add(rtgerr.MapDirectionPending, "/direction",
			"mapping is valid, but this engine version cannot yet execute %s migrations", m.Direction)
	}

	reserved := ""
	if m.Identity.Strategy == StrategyHash {
		reserved = m.Identity.Property
	}

	entityIndex := map[string]int{}
	for i := range m.Entities {
		e := &m.Entities[i]
		base := fmt.Sprintf("/entities/%d", i)
		if first, dup := entityIndex[e.Name]; dup {
			add(rtgerr.MapDuplicateEntity, base+"/name", "entity %q is already defined at /entities/%d", e.Name, first)
		} else {
			entityIndex[e.Name] = i
		}

		checkProperties(e.Properties, base+"/properties", reserved, add)

		for k, col := range e.Key {
			p, ok := e.PropertyByColumn(col)
			if !ok {
				add(rtgerr.MapKeyNotMapped, fmt.Sprintf("%s/key/%d", base, k),
					"key column %q of entity %q is not mapped as a property", col, e.Name)
				continue
			}
			if !p.Type.IdentityPermitted() {
				add(rtgerr.MapKeyTypeNotAllowed, fmt.Sprintf("%s/key/%d", base, k),
					"key column %q of entity %q has type %s, which cannot be an identity component", col, e.Name, p.Type)
			}
		}
	}

	keyLen := func(entity string) (int, bool) {
		i, ok := entityIndex[entity]
		if !ok {
			return 0, false
		}
		return len(m.Entities[i].Key), true
	}
	checkRef := func(path, entity string, columns []string, role string) {
		n, ok := keyLen(entity)
		if !ok {
			add(rtgerr.MapUnknownEntity, path+"/entity", "%s entity %q is not defined", role, entity)
			return
		}
		if len(columns) != n {
			add(rtgerr.MapForeignKeyArity, path+"/columns",
				"%d column(s) reference entity %q, whose key has %d field(s)", len(columns), entity, n)
		}
	}

	relIndex := map[string]int{}
	for i := range m.Relationships {
		r := &m.Relationships[i]
		base := fmt.Sprintf("/relationships/%d", i)
		if first, dup := relIndex[r.Name]; dup {
			add(rtgerr.MapDuplicateRelation, base+"/name", "relationship %q is already defined at /relationships/%d", r.Name, first)
		} else {
			relIndex[r.Name] = i
		}

		switch {
		case r.ForeignKey != nil:
			fk := r.ForeignKey
			path := base + "/foreignKey"
			if _, ok := entityIndex[fk.Entity]; !ok {
				add(rtgerr.MapUnknownEntity, path+"/entity", "foreign-key owner entity %q is not defined", fk.Entity)
			}
			n, ok := keyLen(fk.References)
			if !ok {
				add(rtgerr.MapUnknownEntity, path+"/references", "referenced entity %q is not defined", fk.References)
			} else if len(fk.Columns) != n {
				add(rtgerr.MapForeignKeyArity, path+"/columns",
					"%d foreign-key column(s) reference entity %q, whose key has %d field(s)", len(fk.Columns), fk.References, n)
			}
			checkDuplicateColumns(fk.Columns, path+"/columns", add)
		case r.Junction != nil:
			j := r.Junction
			checkRef(base+"/junction/from", j.From.Entity, j.From.Columns, "junction source")
			checkRef(base+"/junction/to", j.To.Entity, j.To.Columns, "junction target")
		}

		checkProperties(r.Properties, base+"/properties", reserved, add)
	}

	for i, a := range m.Policies.Allow {
		if a.Entity != "" {
			if _, ok := entityIndex[a.Entity]; !ok {
				add(rtgerr.MapUnknownEntity, fmt.Sprintf("/policies/allow/%d/entity", i), "entity %q is not defined", a.Entity)
			}
		}
	}

	return issues
}

type addFunc func(code rtgerr.Code, path, format string, args ...any)

func checkProperties(props []Property, base, reserved string, add addFunc) {
	names := map[string]int{}
	columns := map[string]int{}
	for i, p := range props {
		path := fmt.Sprintf("%s/%d", base, i)
		if reserved != "" && p.Property == reserved {
			add(rtgerr.MapReservedProperty, path+"/property",
				"property name %q is reserved for the identity property", p.Property)
		}
		if first, dup := names[p.Property]; dup {
			add(rtgerr.MapDuplicateProperty, path+"/property", "property %q is already mapped at %s/%d", p.Property, base, first)
		} else {
			names[p.Property] = i
		}
		if first, dup := columns[p.Column]; dup {
			add(rtgerr.MapDuplicateColumn, path+"/column", "column %q is already mapped at %s/%d", p.Column, base, first)
		} else {
			columns[p.Column] = i
		}
	}
}

func checkDuplicateColumns(cols []string, base string, add addFunc) {
	seen := map[string]int{}
	for i, c := range cols {
		if first, dup := seen[c]; dup {
			add(rtgerr.MapDuplicateColumn, fmt.Sprintf("%s/%d", base, i), "column %q is already listed at %s/%d", c, base, first)
		} else {
			seen[c] = i
		}
	}
}
