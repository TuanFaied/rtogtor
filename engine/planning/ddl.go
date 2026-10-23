package planning

import (
	"fmt"
	"strings"
)

// tableFK is a foreign key of a table the plan creates.
type tableFK struct {
	load      ForeignKeyLoad
	refTable  string
	refSchema string
	refName   string
	refKey    []string
	// constrain: the referenced table exists when this one is created, so
	// a FOREIGN KEY constraint can be declared.
	constrain bool
}

// createTableSQL renders CREATE TABLE IF NOT EXISTS for an entity's table:
// mapped columns, foreign-key columns, the primary key on the mapping key,
// and foreign-key constraints where the referenced table already exists.
func createTableSQL(r *revEntity, fks []tableFK) (string, []string) {
	var lines []string
	var warnings []string
	seen := map[string]bool{}
	column := func(f Field) {
		if seen[f.Column] {
			return
		}
		seen[f.Column] = true
		null := ""
		if !f.Nullable {
			null = " NOT NULL"
		}
		lines = append(lines, fmt.Sprintf("    %s %s%s", sqlIdent(f.Column), f.NativeType, null))
	}
	for _, f := range r.props {
		column(f)
	}
	for _, fk := range fks {
		for _, f := range fk.load.Columns {
			column(f)
		}
	}
	keys := make([]string, len(r.e.Key))
	for i, k := range r.e.Key {
		keys[i] = sqlIdent(k)
	}
	lines = append(lines, "    PRIMARY KEY ("+strings.Join(keys, ", ")+")")
	for _, fk := range fks {
		cols := make([]string, len(fk.load.Columns))
		for i, c := range fk.load.Columns {
			cols[i] = sqlIdent(c.Column)
		}
		if !fk.constrain {
			warnings = append(warnings, fmt.Sprintf("foreign key (%s) to %s is not declared as a constraint: that table is created later (cycle)",
				strings.Join(cols, ", "), fk.refTable))
			continue
		}
		refs := make([]string, len(fk.refKey))
		for i, k := range fk.refKey {
			refs[i] = sqlIdent(k)
		}
		lines = append(lines, fmt.Sprintf("    FOREIGN KEY (%s) REFERENCES %s.%s (%s)",
			strings.Join(cols, ", "), sqlIdent(fk.refSchema), sqlIdent(fk.refName), strings.Join(refs, ", ")))
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (\n%s\n)", sqlIdent(r.schema), sqlIdent(r.e.Table), strings.Join(lines, ",\n")), warnings
}
