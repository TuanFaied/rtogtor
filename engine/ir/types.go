// Package ir implements the canonical intermediate representation: logical
// types, canonical value encoding (specs/values.md) and identity derivation
// (specs/identity.md).
package ir

// LogicalType is a logical type from specs/values.md §1.
type LogicalType string

// Logical types.
const (
	TypeBool        LogicalType = "bool"
	TypeInt64       LogicalType = "int64"
	TypeDecimal     LogicalType = "decimal"
	TypeFloat64     LogicalType = "float64"
	TypeString      LogicalType = "string"
	TypeUUID        LogicalType = "uuid"
	TypeDate        LogicalType = "date"
	TypeTime        LogicalType = "time"
	TypeTimeTZ      LogicalType = "timetz"
	TypeTimestamp   LogicalType = "timestamp"
	TypeTimestampTZ LogicalType = "timestamptz"
	TypeDuration    LogicalType = "duration"
	TypeBytes       LogicalType = "bytes"
	TypeJSON        LogicalType = "json"
	TypeArray       LogicalType = "array"
)

var knownTypes = map[LogicalType]bool{
	TypeBool: true, TypeInt64: true, TypeDecimal: true, TypeFloat64: true,
	TypeString: true, TypeUUID: true, TypeDate: true, TypeTime: true,
	TypeTimeTZ: true, TypeTimestamp: true, TypeTimestampTZ: true,
	TypeDuration: true, TypeBytes: true, TypeJSON: true, TypeArray: true,
}

// Known reports whether t is a logical type defined by the specification.
func (t LogicalType) Known() bool { return knownTypes[t] }

// identityTags are the frozen rtg-id/v1 tags (specs/identity.md §2).
var identityTags = map[LogicalType]string{
	TypeBool:        "bool",
	TypeInt64:       "i64",
	TypeDecimal:     "dec",
	TypeString:      "str",
	TypeUUID:        "uuid",
	TypeDate:        "date",
	TypeTimestamp:   "ts",
	TypeTimestampTZ: "tstz",
	TypeBytes:       "bytes",
}

// IdentityPermitted reports whether t may be an identity key component.
func (t LogicalType) IdentityPermitted() bool {
	_, ok := identityTags[t]
	return ok
}
