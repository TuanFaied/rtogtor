// Package rtgerr implements RtoGtoR's stable error codes.
//
// Code definitions (severity, retryability, meaning, remediation) come from
// specs/error-codes.json, embedded into the engine. This package never invents
// codes: every Code constant must exist in that file, which the tests enforce.
package rtgerr

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/TuanFaied/rtogtor/engine/spec"
)

// Code is a stable error code of the form RTG-<AREA>-<NNN>.
type Code string

// Codes used by the engine. Keep in sync with specs/error-codes.json.
const (
	SpecUnsupportedVersion Code = "RTG-SPEC-001"

	MapSchema             Code = "RTG-MAP-001"
	MapParse              Code = "RTG-MAP-002"
	MapDuplicateEntity    Code = "RTG-MAP-003"
	MapUnknownEntity      Code = "RTG-MAP-004"
	MapKeyNotMapped       Code = "RTG-MAP-005"
	MapKeyTypeNotAllowed  Code = "RTG-MAP-006"
	MapForeignKeyArity    Code = "RTG-MAP-007"
	MapDuplicateProperty  Code = "RTG-MAP-008"
	MapDuplicateColumn    Code = "RTG-MAP-009"
	MapDirectionPending   Code = "RTG-MAP-010"
	MapDuplicateRelation  Code = "RTG-MAP-011"
	MapReservedProperty   Code = "RTG-MAP-012"
	ValInvalid            Code = "RTG-VAL-001"
	ValUnsupportedType    Code = "RTG-VAL-002"
	IDNullComponent       Code = "RTG-ID-001"
	IDTypeNotPermitted    Code = "RTG-ID-002"
	IDEmptyInput          Code = "RTG-ID-003"
	SchemaUnsupportedType Code = "RTG-SCHEMA-001"
	SchemaNoPrimaryKey    Code = "RTG-SCHEMA-002"
	SchemaNotFound        Code = "RTG-SCHEMA-003"
	ConnFailed            Code = "RTG-CONN-001"
	ConnConfig            Code = "RTG-CONN-002"
	PGQuery               Code = "RTG-PG-001"
	PlanTableNotFound     Code = "RTG-PLAN-001"
	PlanColumnNotFound    Code = "RTG-PLAN-002"
	PlanTypeMismatch      Code = "RTG-PLAN-003"
	PlanNullableKey       Code = "RTG-PLAN-004"
	PlanLossNotAllowed    Code = "RTG-PLAN-005"
	PlanUnsupported       Code = "RTG-PLAN-006"
	PlanForeignKeyTypes   Code = "RTG-PLAN-007"
	ExecConversion        Code = "RTG-EXEC-001"
	ExecMissingEndpoint   Code = "RTG-EXEC-002"
	ExecBatchFailed       Code = "RTG-EXEC-003"
	ExecInterrupted       Code = "RTG-EXEC-004"
	RunNotFound           Code = "RTG-RUN-001"
	RunMappingChanged     Code = "RTG-RUN-002"
	RunCompleted          Code = "RTG-RUN-003"
	RunStateCorrupt       Code = "RTG-RUN-004"
	NeoQuery              Code = "RTG-NEO-001"
	VerifyMissingEntity   Code = "RTG-VERIFY-001"
	VerifyEntityCount     Code = "RTG-VERIFY-002"
	VerifyRelationCount   Code = "RTG-VERIFY-003"
	VerifyDanglingSource  Code = "RTG-VERIFY-004"
	ProtoUnsupportedMajor Code = "RTG-PROTO-001"
	ProtoMalformed        Code = "RTG-PROTO-002"
	ProtoUnknownMethod    Code = "RTG-PROTO-003"
	ProtoInvalidParams    Code = "RTG-PROTO-004"
	ProtoDuplicateID      Code = "RTG-PROTO-005"
	ProtoHandshake        Code = "RTG-PROTO-006"
	ProtoEngineVersion    Code = "RTG-PROTO-007"
	ProtoEngineMissing    Code = "RTG-PROTO-008"
	CLIUsage              Code = "RTG-CLI-001"
	Internal              Code = "RTG-INTERNAL-001"
)

// Severity of an error code.
type Severity string

// Severities defined by specs/error-codes.md.
const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
	SeverityFatal   Severity = "fatal"
)

// Definition is one entry of specs/error-codes.json.
type Definition struct {
	Code        Code     `json:"code"`
	Title       string   `json:"title"`
	Severity    Severity `json:"severity"`
	Retryable   bool     `json:"retryable"`
	Meaning     string   `json:"meaning"`
	Remediation string   `json:"remediation"`
	Deprecated  bool     `json:"deprecated,omitempty"`
}

var (
	registryOnce sync.Once
	registry     map[Code]Definition
)

func loadRegistry() {
	var doc struct {
		Codes []Definition `json:"codes"`
	}
	if err := json.Unmarshal(spec.MustFile(spec.ErrorCodesFile), &doc); err != nil {
		panic(fmt.Sprintf("rtgerr: embedded %s is invalid: %v", spec.ErrorCodesFile, err))
	}
	registry = make(map[Code]Definition, len(doc.Codes))
	for _, d := range doc.Codes {
		registry[d.Code] = d
	}
}

// Lookup returns the definition of a code.
func Lookup(c Code) (Definition, bool) {
	registryOnce.Do(loadRegistry)
	d, ok := registry[c]
	return d, ok
}

// All returns every defined code, sorted.
func All() []Definition {
	registryOnce.Do(loadRegistry)
	out := make([]Definition, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Error is an RtoGtoR error carrying a stable code.
//
// Messages must never contain credentials or connection strings.
type Error struct {
	Code    Code
	Message string
	// Path locates the problem, e.g. a JSON Pointer into a mapping
	// ("/entities/0/key/1"). Optional.
	Path string
	// Cause is an optional underlying error.
	Cause error
}

// New creates an Error with a formatted message.
func New(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// At returns a copy of e with Path set.
func (e *Error) At(path string) *Error {
	c := *e
	c.Path = path
	return &c
}

// Wrap returns a copy of e with Cause set.
func (e *Error) Wrap(cause error) *Error {
	c := *e
	c.Cause = cause
	return &c
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Code))
	if e.Path != "" {
		b.WriteString(" at ")
		b.WriteString(e.Path)
	}
	b.WriteString(": ")
	b.WriteString(e.Message)
	return b.String()
}

func (e *Error) Unwrap() error { return e.Cause }

// Severity returns the code's severity. Unknown codes are reported as fatal,
// since they indicate an engine defect.
func (e *Error) Severity() Severity {
	if d, ok := Lookup(e.Code); ok {
		return d.Severity
	}
	return SeverityFatal
}

// Retryable reports whether the code is retryable.
func (e *Error) Retryable() bool {
	d, ok := Lookup(e.Code)
	return ok && d.Retryable
}

// IsCode reports whether err is or wraps an *Error with the given code.
func IsCode(err error, code Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// List is an ordered collection of errors and warnings, such as the result of
// validating a mapping.
type List []*Error

// HasErrors reports whether any entry has severity error or fatal.
func (l List) HasErrors() bool {
	for _, e := range l {
		if s := e.Severity(); s == SeverityError || s == SeverityFatal {
			return true
		}
	}
	return false
}

// Err returns l as an error if it contains errors, otherwise nil.
func (l List) Err() error {
	if l.HasErrors() {
		return l
	}
	return nil
}

func (l List) Error() string {
	parts := make([]string, len(l))
	for i, e := range l {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}
