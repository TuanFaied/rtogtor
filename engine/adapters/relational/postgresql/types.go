package postgresql

import (
	"fmt"

	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
)

// pgType is the subset of a pg_type row needed to map a type.
type pgType struct {
	OID       uint32
	Namespace string
	Name      string
	Type      byte   // typtype: b base, d domain, e enum, c composite, r range, m multirange, p pseudo
	Category  byte   // typcategory: A = array
	Elem      uint32 // typelem
	BaseType  uint32 // typbasetype (domains)
	BaseMod   int32  // typtypmod (domains)
	EnumVals  []string
}

// defaultFractionalDigits is PostgreSQL's default precision for time types.
const defaultFractionalDigits = 6

// numericPrecisionScale decodes a numeric typmod. ok is false when the column
// has no declared precision. PostgreSQL 15+ allows negative scale, encoded in
// the low 11 bits as a signed value.
func numericPrecisionScale(typmod int32) (precision, scale int, ok bool) {
	if typmod < 4 {
		return 0, 0, false
	}
	v := typmod - 4
	precision = int((v >> 16) & 0xffff)
	scale = int(((v & 0x7ff) ^ 1024) - 1024)
	return precision, scale, true
}

// charLength decodes a varchar/bpchar typmod.
func charLength(typmod int32) (int, bool) {
	if typmod < 4 {
		return 0, false
	}
	return int(typmod - 4), true
}

// timePrecision decodes time, timetz, timestamp and timestamptz typmods.
func timePrecision(typmod int32) int {
	if typmod < 0 {
		return defaultFractionalDigits
	}
	return int(typmod)
}

// intervalPrecision decodes an interval typmod (field mask in the high bits,
// precision in the low 16 bits; 0xFFFF means unspecified).
func intervalPrecision(typmod int32) int {
	if typmod < 0 {
		return defaultFractionalDigits
	}
	p := int(typmod & 0xffff)
	if p == 0xffff {
		return defaultFractionalDigits
	}
	return p
}

func ptr[T any](v T) *T { return &v }

// typeLookup returns pg_type information for an OID.
type typeLookup func(oid uint32) (*pgType, error)

// mapType converts a PostgreSQL type and typmod into type metadata. When the
// type has no logical equivalent, info.Type is nil and reason explains why.
func mapType(lookup typeLookup, oid uint32, typmod int32, depth int) (info schemameta.TypeInfo, reason string, err error) {
	if depth > 8 {
		return info, "type nesting is too deep", nil
	}
	t, err := lookup(oid)
	if err != nil {
		return info, "", err
	}

	switch {
	case t.Type == 'd':
		// Domains behave like their base type; the domain's own typmod wins.
		mod := t.BaseMod
		if mod < 0 {
			mod = typmod
		}
		return mapType(lookup, t.BaseType, mod, depth+1)

	case t.Type == 'e':
		info.Type = ptr(ir.TypeString)
		info.EnumValues = append([]string{}, t.EnumVals...)
		return info, "", nil

	case t.Category == 'A' && t.Elem != 0:
		elem, why, err := mapType(lookup, t.Elem, typmod, depth+1)
		if err != nil {
			return info, "", err
		}
		if elem.Type == nil {
			return info, "array element: " + why, nil
		}
		if *elem.Type == ir.TypeArray {
			return info, "nested arrays are not supported", nil
		}
		info.Type = ptr(ir.TypeArray)
		info.Element = &elem
		return info, "", nil

	case t.Namespace != "pg_catalog" || t.Type != 'b':
		return info, fmt.Sprintf("type %s.%s has no logical equivalent in this specification version", t.Namespace, t.Name), nil
	}

	switch t.Name {
	case "int2", "int4", "int8":
		info.Type = ptr(ir.TypeInt64)
	case "numeric":
		info.Type = ptr(ir.TypeDecimal)
		if p, s, ok := numericPrecisionScale(typmod); ok {
			if s < 0 {
				return schemameta.TypeInfo{}, "numeric with negative scale is not supported", nil
			}
			info.Precision, info.Scale = ptr(p), ptr(s)
		}
	case "float4", "float8":
		info.Type = ptr(ir.TypeFloat64)
	case "text":
		info.Type = ptr(ir.TypeString)
	case "varchar", "bpchar":
		info.Type = ptr(ir.TypeString)
		if n, ok := charLength(typmod); ok {
			info.MaxLength = ptr(n)
		}
	case "bool":
		info.Type = ptr(ir.TypeBool)
	case "uuid":
		info.Type = ptr(ir.TypeUUID)
	case "date":
		info.Type = ptr(ir.TypeDate)
	case "time":
		info.Type, info.FractionalDigits = ptr(ir.TypeTime), ptr(timePrecision(typmod))
	case "timetz":
		info.Type, info.FractionalDigits = ptr(ir.TypeTimeTZ), ptr(timePrecision(typmod))
	case "timestamp":
		info.Type, info.FractionalDigits = ptr(ir.TypeTimestamp), ptr(timePrecision(typmod))
	case "timestamptz":
		info.Type, info.FractionalDigits = ptr(ir.TypeTimestampTZ), ptr(timePrecision(typmod))
	case "interval":
		info.Type, info.FractionalDigits = ptr(ir.TypeDuration), ptr(intervalPrecision(typmod))
	case "bytea":
		info.Type = ptr(ir.TypeBytes)
	case "json", "jsonb":
		info.Type = ptr(ir.TypeJSON)
	default:
		return info, fmt.Sprintf("type %s has no logical equivalent in this specification version", t.Name), nil
	}
	return info, "", nil
}

// fkAction maps pg_constraint.confdeltype / confupdtype codes.
func fkAction(code string) schemameta.FKAction {
	switch code {
	case "r":
		return schemameta.FKRestrict
	case "c":
		return schemameta.FKCascade
	case "n":
		return schemameta.FKSetNull
	case "d":
		return schemameta.FKSetDefault
	default: // "a"
		return schemameta.FKNoAction
	}
}
