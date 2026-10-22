// Package spec exposes the RtoGtoR specification version and the
// machine-readable specification files embedded into the engine.
//
// The files under data/ are byte-for-byte copies of the repository's specs/
// directory, refreshed with `go generate ./...`. They are embedded so the engine
// works when installed from the Go module proxy, where specs/ is not available.
package spec

import (
	"embed"
	"fmt"
	"strconv"
	"strings"
)

//go:generate go run ./gen

// Version is the specification version this engine implements.
const Version = "0.1.0"

// IdentityAlgorithm is the identity derivation implemented by this engine.
const IdentityAlgorithm = "rtg-id/v1"

// Embedded file names.
const (
	MappingSchemaFile        = "mapping.schema.json"
	SchemaMetadataSchemaFile = "schema-metadata.schema.json"
	IRSchemaFile             = "ir.schema.json"
	ErrorCodesFile           = "error-codes.json"
	PlanSchemaFile           = "plan.schema.json"
	ReportSchemaFile         = "report.schema.json"
	RunStateSchemaFile       = "run-state.schema.json"
	ProtocolSchemaFile       = "protocol.schema.json"
)

// ProtocolVersion is the engine ↔ SDK protocol version (specs/protocol.md).
const ProtocolVersion = "1.0"

// Files lists every embedded specification file. The generator copies exactly
// these files from specs/.
var Files = []string{
	MappingSchemaFile,
	SchemaMetadataSchemaFile,
	IRSchemaFile,
	ErrorCodesFile,
	PlanSchemaFile,
	ReportSchemaFile,
	RunStateSchemaFile,
	ProtocolSchemaFile,
}

// SchemaBaseURL is the base of every schema's $id.
const SchemaBaseURL = "https://github.com/TuanFaied/rtogtor/specs/"

//go:embed data/*.json
var data embed.FS

// File returns the contents of an embedded specification file.
func File(name string) ([]byte, error) {
	b, err := data.ReadFile("data/" + name)
	if err != nil {
		return nil, fmt.Errorf("spec file %q is not embedded: %w", name, err)
	}
	return b, nil
}

// MustFile is like File but panics if the file is missing. Embedded files are
// fixed at build time, so a missing file is a build defect.
func MustFile(name string) []byte {
	b, err := File(name)
	if err != nil {
		panic(err)
	}
	return b
}

// Supports reports whether this engine can process documents declaring the
// given specVersion. Before 1.0, a minor version change may be breaking, so
// the major and minor versions must both match; patch versions are
// compatible.
func Supports(version string) bool {
	maj, min, ok := majorMinor(version)
	if !ok {
		return false
	}
	wantMaj, wantMin, _ := majorMinor(Version)
	if wantMaj == 0 {
		return maj == wantMaj && min == wantMin
	}
	return maj == wantMaj && min <= wantMin
}

func majorMinor(v string) (int, int, bool) {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return 0, 0, false
		}
		nums[i] = n
	}
	return nums[0], nums[1], true
}
