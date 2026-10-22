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
	compiled map[string]*jsonschema.Schema
	errInit  error
)

func compileAll() {
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
	compiled = map[string]*jsonschema.Schema{}
	for _, name := range spec.Files {
		if name == spec.ErrorCodesFile {
			continue
		}
		s, err := c.Compile(spec.SchemaBaseURL + name)
		if err != nil {
			errInit = fmt.Errorf("compiling schema %s: %w", name, err)
			return
		}
		compiled[name] = s
	}
}

// Violation is one schema validation failure.
type Violation struct {
	// Path is a JSON Pointer to the offending value ("" for the root).
	Path    string
	Message string
}

// Validate validates a decoded JSON instance (as produced by
// jsonschema.UnmarshalJSON) against the named embedded schema.
//
// The returned error is non-nil only if the embedded schemas themselves are
// broken, which is an engine defect.
func Validate(schemaFile string, instance any) ([]Violation, error) {
	once.Do(compileAll)
	if errInit != nil {
		return nil, errInit
	}
	s, ok := compiled[schemaFile]
	if !ok {
		return nil, fmt.Errorf("schema %s is not embedded", schemaFile)
	}
	err := s.Validate(instance)
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
