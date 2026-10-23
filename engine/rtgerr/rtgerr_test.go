package rtgerr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var allConstants = []Code{
	SpecUnsupportedVersion,
	MapSchema, MapParse, MapDuplicateEntity, MapUnknownEntity, MapKeyNotMapped,
	MapKeyTypeNotAllowed, MapForeignKeyArity, MapDuplicateProperty,
	MapDuplicateColumn, MapDirectionPending, MapDuplicateRelation,
	MapReservedProperty,
	ValInvalid, ValUnsupportedType,
	IDNullComponent, IDTypeNotPermitted, IDEmptyInput,
	SchemaUnsupportedType, SchemaNoPrimaryKey, SchemaNotFound,
	ConnFailed, ConnConfig, PGQuery, PGRowRejected,
	PlanTableNotFound, PlanColumnNotFound, PlanTypeMismatch, PlanNullableKey,
	PlanLossNotAllowed, PlanUnsupported, PlanForeignKeyTypes,
	PlanNoUniqueKey, PlanTableMissing, PlanDeferredNotNull, PlanSourceShape,
	ExecConversion, ExecMissingEndpoint, ExecBatchFailed, ExecInterrupted,
	ExecMultipleRelated, ExecDuplicateKey, ExecPrecisionLoss,
	RunNotFound, RunMappingChanged, RunCompleted, RunStateCorrupt,
	NeoQuery,
	VerifyMissingEntity, VerifyEntityCount, VerifyRelationCount, VerifyDanglingSource,
	ProtoUnsupportedMajor, ProtoMalformed, ProtoUnknownMethod, ProtoInvalidParams,
	ProtoDuplicateID, ProtoHandshake, ProtoEngineVersion, ProtoEngineMissing,
	CLIUsage, Internal,
}

var codePattern = regexp.MustCompile(`^RTG-[A-Z]+-[0-9]{3}$`)

func TestEveryConstantIsDefined(t *testing.T) {
	for _, c := range allConstants {
		if _, ok := Lookup(c); !ok {
			t.Errorf("code %s is used by the engine but missing from specs/error-codes.json", c)
		}
	}
}

func TestEveryDefinitionIsWellFormed(t *testing.T) {
	valid := map[Severity]bool{SeverityInfo: true, SeverityWarning: true, SeverityError: true, SeverityFatal: true}
	for _, d := range All() {
		if !codePattern.MatchString(string(d.Code)) {
			t.Errorf("%s: malformed code", d.Code)
		}
		if !valid[d.Severity] {
			t.Errorf("%s: invalid severity %q", d.Code, d.Severity)
		}
		if d.Title == "" || d.Meaning == "" || d.Remediation == "" {
			t.Errorf("%s: title, meaning and remediation are required", d.Code)
		}
	}
}

// TestMarkdownListsSameCodes keeps specs/error-codes.md in sync with the JSON.
func TestMarkdownListsSameCodes(t *testing.T) {
	md, err := os.ReadFile(filepath.Join("..", "..", "specs", "error-codes.md"))
	if err != nil {
		t.Skip("specs/ not available")
	}
	rowCode := regexp.MustCompile("(?m)^\\| `(RTG-[A-Z]+-[0-9]{3})` \\|")
	inMD := map[Code]bool{}
	for _, m := range rowCode.FindAllSubmatch(md, -1) {
		inMD[Code(m[1])] = true
	}
	for _, d := range All() {
		if !inMD[d.Code] {
			t.Errorf("%s is in error-codes.json but not in the error-codes.md table", d.Code)
		}
		delete(inMD, d.Code)
	}
	for c := range inMD {
		t.Errorf("%s is in error-codes.md but not in error-codes.json", c)
	}
}

func TestErrorFormattingAndMatching(t *testing.T) {
	e := New(MapKeyNotMapped, "key column %q is not mapped", "line_no").At("/entities/3/key/1")
	if got, want := e.Error(), `RTG-MAP-005 at /entities/3/key/1: key column "line_no" is not mapped`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if e.Severity() != SeverityError || e.Retryable() {
		t.Errorf("unexpected severity/retryable: %s %v", e.Severity(), e.Retryable())
	}
	wrapped := fmt.Errorf("loading: %w", e)
	if !IsCode(wrapped, MapKeyNotMapped) || IsCode(wrapped, MapSchema) {
		t.Error("IsCode did not match through wrapping")
	}
	cause := errors.New("boom")
	if !errors.Is(New(Internal, "x").Wrap(cause), cause) {
		t.Error("Unwrap does not expose the cause")
	}
}

func TestListSeverity(t *testing.T) {
	warnOnly := List{New(MapDirectionPending, "not yet")}
	if warnOnly.HasErrors() || warnOnly.Err() != nil {
		t.Error("a warning-only list must not be an error")
	}
	mixed := append(warnOnly, New(MapSchema, "bad"))
	if !mixed.HasErrors() || mixed.Err() == nil {
		t.Error("a list with an error must report it")
	}
}
