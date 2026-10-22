package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// The model is language-neutral: schemas are reduced to classes with typed
// fields. Validation-only keywords (allOf, if/then/else, not, pattern,
// minimum, …) do not affect types and are ignored here; the engine and the
// SDKs validate documents against the schemas themselves.

// Kind of a type reference.
type Kind int

// Kinds.
const (
	KString Kind = iota
	KInt
	KFloat
	KBool
	KJSON   // any JSON value
	KObject // a generated class
	KList
)

// TypeRef is a field type.
type TypeRef struct {
	Kind     Kind
	Class    string   // for KObject
	Elem     *TypeRef // for KList
	Nullable bool     // JSON null is a valid value
}

// Field is one property of a class.
type Field struct {
	JSONName string
	Name     string
	Type     TypeRef
	Required bool
	Doc      string
}

// Class is a generated type.
type Class struct {
	Name   string
	Doc    string
	Fields []Field
	File   string // schema file the class comes from
	key    string // file#pointer
}

// Model holds every class reachable from the roots.
type Model struct {
	files   map[string]*object
	classes map[string]*Class // by key
	order   []string          // keys in discovery order
	names   map[string]string // key -> final name
}

// Root names a schema location to generate, with the class name to use.
type Root struct {
	File    string
	Pointer string
	Name    string
}

func newModel(files map[string]*object) *Model {
	return &Model{files: files, classes: map[string]*Class{}, names: map[string]string{}}
}

// filePrefix is used to disambiguate class names shared by several schemas.
var filePrefix = map[string]string{
	"mapping.schema.json":         "Mapping",
	"schema-metadata.schema.json": "Metadata",
	"plan.schema.json":            "Plan",
	"report.schema.json":          "Report",
	"run-state.schema.json":       "RunState",
	"protocol.schema.json":        "Protocol",
	"ir.schema.json":              "Ir",
}

// pascal converts "run-state", "keyComponent" or "rtg_id" to "RunState",
// "KeyComponent", "RtgId".
func pascal(s string) string {
	var b strings.Builder
	upper := true
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			upper = true
			continue
		}
		if upper {
			b.WriteRune(unicode.ToUpper(r))
			upper = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// resolve follows a $ref relative to file.
func (m *Model) resolve(file, ref string) (string, string, *object, error) {
	target, ptr, _ := strings.Cut(ref, "#")
	if target == "" {
		target = file
	}
	root, ok := m.files[target]
	if !ok {
		return "", "", nil, fmt.Errorf("%s: $ref to unknown file %q", file, target)
	}
	node := any(root)
	if ptr != "" {
		for _, seg := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
			seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
			o, ok := node.(*object)
			if !ok {
				return "", "", nil, fmt.Errorf("%s: cannot resolve %s", file, ref)
			}
			node, ok = o.get(seg)
			if !ok {
				return "", "", nil, fmt.Errorf("%s: cannot resolve %s", file, ref)
			}
		}
	}
	o, ok := node.(*object)
	if !ok {
		return "", "", nil, fmt.Errorf("%s: %s is not a schema object", file, ref)
	}
	return target, ptr, o, nil
}

// baseName derives a class name from a location: definitions use their own
// name; nested objects append their property name to their parent's.
func (m *Model) baseName(file, ptr string, roots map[string]string) string {
	if n, ok := roots[file+"#"+ptr]; ok {
		return n
	}
	segs := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	last := len(segs) - 1
	if last >= 1 && segs[last-1] == "$defs" {
		return pascal(segs[last])
	}
	parent := strings.Join(segs[:last], "/")
	if parent != "" {
		parent = "/" + parent
	}
	switch {
	case last >= 1 && segs[last-1] == "properties":
		parent = "/" + strings.Join(segs[:last-1], "/")
		if parent == "/" {
			parent = ""
		}
		return m.baseName(file, parent, roots) + pascal(segs[last])
	case segs[last] == "items":
		return m.baseName(file, parent, roots) + "Item"
	}
	return m.baseName(file, parent, roots) + pascal(segs[last])
}

// isObjectSchema reports whether a schema describes an object with
// properties (directly or through a union of objects).
func (m *Model) isObjectSchema(file string, s *object) bool {
	if s.has("properties") {
		return true
	}
	for _, kw := range []string{"oneOf", "anyOf"} {
		for _, v := range s.list(kw) {
			vo, ok := v.(*object)
			if !ok {
				continue
			}
			if r := vo.str("$ref"); r != "" {
				f, _, t, err := m.resolve(file, r)
				if err == nil && m.isObjectSchema(f, t) {
					return true
				}
			} else if vo.has("properties") && vo.str("type") != "null" {
				return true
			}
		}
	}
	return false
}

// build discovers every class reachable from the roots and names them.
func (m *Model) build(roots []Root) error {
	rootNames := map[string]string{}
	for _, r := range roots {
		rootNames[r.File+"#"+r.Pointer] = r.Name
	}
	var visit func(file, ptr string, s *object) error
	visit = func(file, ptr string, s *object) error {
		key := file + "#" + ptr
		if _, done := m.classes[key]; done {
			return nil
		}
		c := &Class{File: file, key: key, Doc: s.str("description")}
		m.classes[key] = c
		m.order = append(m.order, key)
		fields, err := m.fields(file, ptr, s, visit)
		if err != nil {
			return err
		}
		c.Fields = fields
		return nil
	}
	for _, r := range roots {
		_, _, s, err := m.resolve(r.File, "#"+r.Pointer)
		if err != nil {
			return err
		}
		if err := visit(r.File, r.Pointer, s); err != nil {
			return err
		}
	}

	// Name classes; names shared by several locations get a schema prefix.
	base := map[string][]string{}
	for _, key := range m.order {
		file, ptr, _ := strings.Cut(key, "#")
		n := m.baseName(file, ptr, rootNames)
		base[n] = append(base[n], key)
	}
	used := map[string]string{}
	for _, key := range m.order {
		file, ptr, _ := strings.Cut(key, "#")
		n := m.baseName(file, ptr, rootNames)
		if len(base[n]) > 1 && rootNames[key] == "" {
			n = filePrefix[file] + n
		}
		if other, clash := used[n]; clash {
			return fmt.Errorf("class name %s is used by both %s and %s", n, other, key)
		}
		used[n] = key
		m.names[key] = n
		m.classes[key].Name = n
	}
	return nil
}

type visitFunc func(file, ptr string, s *object) error

// fields collects a class's fields, merging union variants: a field present
// in every variant keeps its requiredness, others become optional.
func (m *Model) fields(file, ptr string, s *object, visit visitFunc) ([]Field, error) {
	type part struct {
		file, ptr string
		s         *object
	}
	parts := []part{{file, ptr, s}}
	var variants []part
	for _, kw := range []string{"oneOf", "anyOf"} {
		for i, v := range s.list(kw) {
			vo, ok := v.(*object)
			if !ok {
				continue
			}
			if r := vo.str("$ref"); r != "" {
				f, p, t, err := m.resolve(file, r)
				if err != nil {
					return nil, err
				}
				if t.has("properties") {
					variants = append(variants, part{f, p, t})
				}
			} else if vo.has("properties") {
				variants = append(variants, part{file, fmt.Sprintf("%s/%s/%d", ptr, kw, i), vo})
			}
		}
	}
	parts = append(parts, variants...)

	var out []Field
	index := map[string]int{}
	variantRequired := map[string]int{} // how many variants require a field
	for pi, p := range parts {
		req := map[string]bool{}
		for _, r := range p.s.list("required") {
			req[r.(string)] = true
		}
		if pi > 0 {
			for name := range req {
				variantRequired[name]++
			}
		}
		props := p.s.obj("properties")
		if props == nil {
			continue
		}
		for _, name := range props.keys {
			ps, ok := props.vals[name].(*object)
			if !ok {
				continue // `true`: the field is declared by another part
			}
			t, err := m.typeOf(p.file, p.ptr+"/properties/"+name, ps, visit)
			if err != nil {
				return nil, err
			}
			if i, seen := index[name]; seen {
				if !sameType(out[i].Type, t) {
					out[i].Type = TypeRef{Kind: KJSON, Nullable: true}
				}
				continue
			}
			index[name] = len(out)
			out = append(out, Field{JSONName: name, Name: pascal(name), Type: t, Doc: ps.str("description"), Required: pi == 0 && req[name]})
		}
	}
	// A field declared only by union variants is required only when every
	// variant requires it.
	baseProps := s.obj("properties")
	for i := range out {
		if _, inBase := baseProps.get(out[i].JSONName); !inBase && len(variants) > 0 {
			out[i].Required = variantRequired[out[i].JSONName] == len(variants)
		}
	}
	return out, nil
}

// typeOf maps a schema to a type, registering classes as needed.
func (m *Model) typeOf(file, ptr string, s *object, visit visitFunc) (TypeRef, error) {
	if s == nil || len(s.keys) == 0 {
		return TypeRef{Kind: KJSON, Nullable: true}, nil
	}
	if r := s.str("$ref"); r != "" {
		f, p, t, err := m.resolve(file, r)
		if err != nil {
			return TypeRef{}, err
		}
		if m.isObjectSchema(f, t) {
			if err := visit(f, p, t); err != nil {
				return TypeRef{}, err
			}
			return TypeRef{Kind: KObject, Class: f + "#" + p}, nil
		}
		return m.typeOf(f, p, t, visit)
	}
	if s.has("enum") {
		nullable := false
		for _, v := range s.list("enum") {
			if v == nil {
				nullable = true
			}
		}
		return TypeRef{Kind: KString, Nullable: nullable}, nil
	}
	if c, ok := s.get("const"); ok {
		if _, isStr := c.(string); isStr {
			return TypeRef{Kind: KString}, nil
		}
		return TypeRef{Kind: KJSON, Nullable: true}, nil
	}

	// Unions that carry types (as opposed to required-property constraints).
	for _, kw := range []string{"oneOf", "anyOf"} {
		vs := s.list(kw)
		if len(vs) == 0 || m.isObjectSchema(file, s) {
			continue
		}
		var typed []*object
		var typedPtr []string
		nullable := false
		for i, v := range vs {
			vo, ok := v.(*object)
			if !ok {
				continue
			}
			if vo.str("type") == "null" {
				nullable = true
				continue
			}
			if vo.has("type") || vo.has("$ref") || vo.has("enum") || vo.has("const") || vo.has("items") {
				typed = append(typed, vo)
				typedPtr = append(typedPtr, fmt.Sprintf("%s/%s/%d", ptr, kw, i))
			}
		}
		switch {
		case len(typed) == 1:
			t, err := m.typeOf(file, typedPtr[0], typed[0], visit)
			if err != nil {
				return TypeRef{}, err
			}
			t.Nullable = t.Nullable || nullable
			return t, nil
		case len(typed) > 1:
			return TypeRef{Kind: KJSON, Nullable: true}, nil
		case nullable:
			return TypeRef{Kind: KJSON, Nullable: true}, nil
		}
	}

	types := []string{}
	switch t := s.vals["type"].(type) {
	case string:
		types = []string{t}
	case []any:
		for _, x := range t {
			types = append(types, x.(string))
		}
	}
	nullable := false
	var nonNull []string
	for _, t := range types {
		if t == "null" {
			nullable = true
		} else {
			nonNull = append(nonNull, t)
		}
	}
	if len(nonNull) == 0 && s.has("properties") {
		nonNull = []string{"object"}
	}
	if len(nonNull) != 1 {
		return TypeRef{Kind: KJSON, Nullable: true}, nil
	}
	switch nonNull[0] {
	case "string":
		return TypeRef{Kind: KString, Nullable: nullable}, nil
	case "integer":
		return TypeRef{Kind: KInt, Nullable: nullable}, nil
	case "number":
		return TypeRef{Kind: KFloat, Nullable: nullable}, nil
	case "boolean":
		return TypeRef{Kind: KBool, Nullable: nullable}, nil
	case "array":
		items, _ := s.vals["items"].(*object)
		elem, err := m.typeOf(file, ptr+"/items", items, visit)
		if err != nil {
			return TypeRef{}, err
		}
		return TypeRef{Kind: KList, Elem: &elem, Nullable: nullable}, nil
	case "object":
		if !s.has("properties") {
			return TypeRef{Kind: KJSON, Nullable: true}, nil
		}
		if err := visit(file, ptr, s); err != nil {
			return TypeRef{}, err
		}
		return TypeRef{Kind: KObject, Class: file + "#" + ptr, Nullable: nullable}, nil
	}
	return TypeRef{Kind: KJSON, Nullable: true}, nil
}

// ErrorCode is one entry of specs/error-codes.json.
type ErrorCode struct {
	Code, Title, Severity, Meaning, Remediation string
	Retryable, Deprecated                       bool
}

func errorCodes(doc *object) []ErrorCode {
	var out []ErrorCode
	for _, v := range doc.list("codes") {
		o := v.(*object)
		ec := ErrorCode{Code: o.str("code"), Title: o.str("title"), Severity: o.str("severity"),
			Meaning: o.str("meaning"), Remediation: o.str("remediation")}
		ec.Retryable, _ = o.vals["retryable"].(bool)
		ec.Deprecated, _ = o.vals["deprecated"].(bool)
		out = append(out, ec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// sameType compares types structurally.
func sameType(a, b TypeRef) bool {
	if a.Kind != b.Kind || a.Class != b.Class || a.Nullable != b.Nullable {
		return false
	}
	if a.Elem == nil || b.Elem == nil {
		return a.Elem == b.Elem
	}
	return sameType(*a.Elem, *b.Elem)
}
