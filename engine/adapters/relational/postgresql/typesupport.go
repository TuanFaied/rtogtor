package postgresql

import (
	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/ir"
)

// TypeSupport describes PostgreSQL column types for logical types.
type TypeSupport struct{}

var _ relational.TypeSupport = TypeSupport{}

// Product implements relational.TypeSupport.
func (TypeSupport) Product() string { return Product }

var columnTypes = map[ir.LogicalType]string{
	ir.TypeBool:        "boolean",
	ir.TypeInt64:       "bigint",
	ir.TypeDecimal:     "numeric",
	ir.TypeFloat64:     "double precision",
	ir.TypeString:      "text",
	ir.TypeUUID:        "uuid",
	ir.TypeDate:        "date",
	ir.TypeTimestamp:   "timestamp(6) without time zone",
	ir.TypeTimestampTZ: "timestamp(6) with time zone",
	ir.TypeBytes:       "bytea",
	ir.TypeJSON:        "jsonb",
}

// ColumnType implements relational.TypeSupport.
func (TypeSupport) ColumnType(t, elem ir.LogicalType) string {
	if t == ir.TypeArray {
		if c, ok := columnTypes[elem]; ok {
			return c + "[]"
		}
		return ""
	}
	return columnTypes[t]
}

// DefaultFractionalDigits implements relational.TypeSupport: PostgreSQL keeps
// microseconds.
func (TypeSupport) DefaultFractionalDigits() int { return 6 }
