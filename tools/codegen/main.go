// Command codegen generates SDK types from the specification's JSON Schemas
// (ADR-0004, ADR-0005).
//
// Run from the repository root:
//
//	go -C tools/codegen run . -repo ../..          # write generated files
//	go -C tools/codegen run . -repo ../.. -check   # fail if out of date
//
// The generator reads specs/*.json into a language-neutral model (classes
// with typed fields) and emits it for each SDK language. Output is
// deterministic.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// roots are the schema locations exposed as SDK types. Everything they
// reference is generated too.
var roots = []Root{
	{File: "mapping.schema.json", Name: "MappingDocument"},
	{File: "schema-metadata.schema.json", Name: "SchemaMetadata"},
	{File: "plan.schema.json", Name: "Plan"},
	{File: "report.schema.json", Name: "Report"},
	{File: "run-state.schema.json", Name: "RunState"},
	{File: "run-state.schema.json", Pointer: "/$defs/failure", Name: "Failure"},
	// "Stream" would clash with System.IO.Stream and similar names.
	{File: "run-state.schema.json", Pointer: "/$defs/stream", Name: "StepState"},
	// "Warning" would shadow Python's built-in Warning exception.
	{File: "schema-metadata.schema.json", Pointer: "/$defs/warning", Name: "InspectionWarning"},
	{File: "protocol.schema.json", Pointer: "/$defs/connection", Name: "Connection"},
	{File: "protocol.schema.json", Pointer: "/$defs/handshakeResult", Name: "HandshakeResult"},
	{File: "protocol.schema.json", Pointer: "/$defs/validateMappingResult", Name: "ValidateMappingResult"},
	{File: "protocol.schema.json", Pointer: "/$defs/identityResult", Name: "IdentityResult"},
	{File: "protocol.schema.json", Pointer: "/$defs/canonicalizeResult", Name: "CanonicalizeResult"},
	{File: "protocol.schema.json", Pointer: "/$defs/failuresResult", Name: "FailuresResult"},
	{File: "protocol.schema.json", Pointer: "/$defs/progress", Name: "Progress"},
	{File: "protocol.schema.json", Pointer: "/$defs/error", Name: "ProtocolError"},
}

// target is one SDK language's generated output.
type target struct {
	dir     string // relative to the repository root
	pattern string // generated files in dir; others are left alone
	emit    func(m *Model, codes []ErrorCode) (map[string]string, error)
	// schemas copies specs/*.schema.json next to the generated code, for SDKs
	// that validate documents at run time and cannot embed files at build.
	schemas bool
}

var targets = []target{
	{dir: "sdks/dotnet/src/RtoGtoR/Generated", pattern: "*.g.cs", emit: (*Model).emitCSharp},
	{dir: "sdks/python/src/rtogtor/_generated", pattern: "*.*", emit: (*Model).emitPython, schemas: true},
	{dir: "sdks/typescript/src/generated", pattern: "*.*", emit: (*Model).emitTypeScript, schemas: true},
}

func main() {
	check := flag.Bool("check", false, "verify generated files are up to date instead of writing them")
	repo := flag.String("repo", ".", "repository root")
	flag.Parse()
	if err := run(*repo, *check); err != nil {
		fmt.Fprintln(os.Stderr, "codegen:", err)
		os.Exit(1)
	}
}

func run(repo string, check bool) error {
	files := map[string]*object{}
	rawSchemas := map[string]string{}
	paths, err := filepath.Glob(filepath.Join(repo, "specs", "*.json"))
	if err != nil {
		return err
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		v, err := parse(b)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		files[filepath.Base(p)] = v.(*object)
		if strings.HasSuffix(p, ".schema.json") {
			rawSchemas[filepath.Base(p)] = string(b)
		}
	}
	m := newModel(files)
	if err := m.build(roots); err != nil {
		return err
	}
	codes := errorCodes(files["error-codes.json"])

	var problems []string
	for _, t := range targets {
		files, err := t.emit(m, codes)
		if err != nil {
			return fmt.Errorf("%s: %w", t.dir, err)
		}
		if t.schemas {
			for name, content := range rawSchemas {
				files[name] = content
			}
		}
		if err := sync(filepath.Join(repo, t.dir), t.pattern, files, check); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// sync writes (or checks) the generated files of one target directory and
// removes stale generated files.
func sync(dir, pattern string, files map[string]string, check bool) error {
	var problems []string
	existing, _ := filepath.Glob(filepath.Join(dir, pattern))
	want := map[string]bool{}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
		want[n] = true
	}
	sort.Strings(names)
	if !check {
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // source files
			return err
		}
	}
	for _, n := range names {
		path := filepath.Join(dir, n)
		cur, err := os.ReadFile(path)
		if err == nil && bytes.Equal(cur, []byte(files[n])) {
			continue
		}
		if check {
			problems = append(problems, path+" is out of date")
			continue
		}
		if err := os.WriteFile(path, []byte(files[n]), 0o644); err != nil { //nolint:gosec // source files
			return err
		}
		fmt.Println("wrote", filepath.ToSlash(path))
	}
	for _, p := range existing {
		if !want[filepath.Base(p)] {
			if check {
				problems = append(problems, p+" is stale")
				continue
			}
			if err := os.Remove(p); err != nil {
				return err
			}
			fmt.Println("removed", filepath.ToSlash(p))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("generated code is out of date; run `go -C tools/codegen run . -repo ../..`:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
