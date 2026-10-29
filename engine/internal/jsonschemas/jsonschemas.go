// Package jsonschemas compiles and applies the embedded specification JSON
// Schemas.
package jsonschemas

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/TuanFaied/rtogtor/engine/spec"
)

var (
	once     sync.Once
	compiler *jsonschema.Compiler
	errInit  error

	mu       sync.Mutex // guards compiled and compiler.Compile
	compiled = map[string]*jsonschema.Schema{}
)

// loadAll registers every embedded schema and compiles each file once, so a
// broken schema is reported on first use.
func loadAll() {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	for _, name := range spec.Files {
		if name == spec.ErrorCodesFile {
			continue
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(spec.MustFile(name)))
		if err != nil {
			errInit = fmt.Errorf("embedded schema %s: %w", name, err)
			return
		}
		if err := c.AddResource(spec.SchemaBaseURL+name, doc); err != nil {
			errInit = fmt.Errorf("embedded schema %s: %w", name, err)
			return
		}
	}
	compiler = c
	for _, name := range spec.Files {
		if name == spec.ErrorCodesFile {
			continue
		}
		if _, err := schemaFor(name); err != nil {
			errInit = err
			return
		}
	}
}

// schemaFor returns a compiled schema for "file" or "file#/json/pointer".
func schemaFor(ref string) (*jsonschema.Schema, error) {
	mu.Lock()
	defer mu.Unlock()
	if s, ok := compiled[ref]; ok {
		return s, nil
	}
	s, err := compiler.Compile(spec.SchemaBaseURL + ref)
	if err != nil {
		return nil, fmt.Errorf("compiling schema %s: %w", ref, err)
	}
	compiled[ref] = s
	return s, nil
}

// Violation is one schema validation failure.
type Violation struct {
	// Path is a JSON Pointer to the offending value ("" for the root).
	Path    string
	Message string
}

// Validate validates a decoded JSON instance (as produced by
// jsonschema.UnmarshalJSON) against an embedded schema: a file name such as
// "plan.schema.json", optionally followed by a JSON Pointer fragment such as
// "#/$defs/request".
//
// The returned error is non-nil only if the embedded schemas themselves are
// broken or the reference is unknown, which are engine defects.
func Validate(schemaRef string, instance any) ([]Violation, error) {
	once.Do(loadAll)
	if errInit != nil {
		return nil, errInit
	}
	s, err := schemaFor(schemaRef)
	if err != nil {
		return nil, err
	}
	err = s.Validate(instance)
	if err == nil {
		return nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil, err
	}
	return flatten(ve), nil
}

// ValidateJSON parses data and validates it against the named schema.
func ValidateJSON(schemaFile string, data []byte) ([]Violation, error) {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return Validate(schemaFile, inst)
}

var printer = message.NewPrinter(language.English)

// flatten reports the leaf causes of a validation error: they carry the
// concrete reason ("missing property 'labels'"), whereas inner nodes only say
// that a subschema failed.
func flatten(ve *jsonschema.ValidationError) []Violation {
	seen := map[Violation]bool{}
	var out []Violation
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		v := Violation{Path: pointer(e.InstanceLocation), Message: e.ErrorKind.LocalizedString(printer)}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	walk(ve)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Message < out[j].Message
	})
	return out
}

func pointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1"))
	}
	return b.String()
}
