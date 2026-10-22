// Package rtogtor is the public Go API of the RtoGtoR engine.
//
// Import path: github.com/TuanFaied/rtogtor/engine (package name rtogtor; see
// docs/adr/0002-go-module-path-and-tagging.md).
//
// The Go API is the engine itself: Go programs call it in-process, while the
// Python, TypeScript and C# SDKs reach the same code through `rtogtor serve
// --stdio` (specs/protocol.schema.json).
//
//	src := rtogtor.PostgreSQLFromEnv("SOURCE_URL")
//	dst := rtogtor.Neo4j("neo4j://localhost:7687", rtogtor.BasicAuthFromEnv("NEO4J"))
//	m, err := rtogtor.LoadMapping("mapping.yaml")
//	mig := rtogtor.NewMigration(src, dst, m)
//	defer mig.Close()
//	plan, err := mig.Plan(ctx)
//	report, err := mig.Run(ctx, rtogtor.RunOptions{})
//
// STATUS: pre-alpha; PostgreSQL → Neo4j only.
package rtogtor

import (
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// EngineVersion is the engine's semantic version.
const EngineVersion = "0.1.0-dev"

// SpecVersion is the specification version the engine implements.
const SpecVersion = spec.Version

// IdentityAlgorithm is the identity derivation the engine implements.
const IdentityAlgorithm = spec.IdentityAlgorithm

// Mapping is a validated mapping.
type Mapping = mapping.Mapping

// Error is an error carrying a stable RtoGtoR error code.
type Error = rtgerr.Error

// LoadMapping reads and validates a mapping from a .json, .yaml or .yml
// file. If the mapping is invalid, the returned error is an rtgerr.List
// holding every problem found. Warnings are available from
// LoadMappingWithWarnings.
func LoadMapping(path string) (*Mapping, error) {
	m, issues := mapping.LoadFile(path)
	if err := issues.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

// LoadMappingWithWarnings is like LoadMapping but also returns warnings for a
// valid mapping.
func LoadMappingWithWarnings(path string) (*Mapping, rtgerr.List, error) {
	m, issues := mapping.LoadFile(path)
	if err := issues.Err(); err != nil {
		return nil, nil, err
	}
	return m, issues, nil
}
