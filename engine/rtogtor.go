// Package rtogtor is the public Go API of the RtoGtoR engine.
//
// Import path: github.com/TuanFaied/rtogtor/engine (package name rtogtor; see
// docs/adr/0002-go-module-path-and-tagging.md).
//
// STATUS: pre-alpha. This release provides mapping loading and validation;
// Connection, Migration, Plan and Report arrive with Milestone 2.
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
