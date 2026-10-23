package neo4j

import (
	"encoding/base64"
	"time"

	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

// Product is the database product name.
const Product = "neo4j"

// TypeSupport describes Neo4j's storage of logical types (specs/values.md §7).
type TypeSupport struct{}

var _ graph.TypeSupport = TypeSupport{}

// Product implements graph.TypeSupport.
func (TypeSupport) Product() string { return Product }

// storage maps scalar logical types to Neo4j property types and losses.
// time, timetz and duration are not supported by this engine version yet.
var storage = map[ir.LogicalType]struct {
	as   string
	loss graph.LossKind
}{
	ir.TypeBool:        {"Boolean", graph.LossNone},
	ir.TypeInt64:       {"Integer", graph.LossNone},
	ir.TypeDecimal:     {"String", graph.LossRepresentation},
	ir.TypeFloat64:     {"Float", graph.LossNone},
	ir.TypeString:      {"String", graph.LossNone},
	ir.TypeUUID:        {"String", graph.LossRepresentation},
	ir.TypeDate:        {"Date", graph.LossNone},
	ir.TypeTimestamp:   {"LocalDateTime", graph.LossNone},
	ir.TypeTimestampTZ: {"DateTime", graph.LossNone},
	ir.TypeBytes:       {"ByteArray", graph.LossNone},
	ir.TypeJSON:        {"String", graph.LossRepresentation},
}

// Supports implements graph.TypeSupport.
func (TypeSupport) Supports(t, elem ir.LogicalType) bool {
	if t == ir.TypeArray {
		_, ok := storage[elem]
		// Neo4j lists cannot hold byte arrays or nested documents usefully.
		return ok && elem != ir.TypeBytes && elem != ir.TypeJSON
	}
	_, ok := storage[t]
	return ok
}

// StorageLoss implements graph.TypeSupport. Arrays inherit their element's
// loss; NULL elements are rejected per record rather than dropped.
func (TypeSupport) StorageLoss(t, elem ir.LogicalType) graph.LossKind {
	if t == ir.TypeArray {
		return storage[elem].loss
	}
	return storage[t].loss
}

// StoredAs implements graph.TypeSupport.
func (TypeSupport) StoredAs(t, elem ir.LogicalType) string {
	if t == ir.TypeArray {
		return "List<" + storage[elem].as + ">"
	}
	return storage[t].as
}

// readable lists the Neo4j property types each logical type can be read from.
var readable = map[ir.LogicalType][]string{
	ir.TypeBool:        {"Boolean"},
	ir.TypeInt64:       {"Long"},
	ir.TypeDecimal:     {"String", "Long"},
	ir.TypeFloat64:     {"Double", "Long"},
	ir.TypeString:      {"String"},
	ir.TypeUUID:        {"String"},
	ir.TypeDate:        {"Date"},
	ir.TypeTimestamp:   {"LocalDateTime"},
	ir.TypeTimestampTZ: {"DateTime"},
	ir.TypeBytes:       {"ByteArray"},
	ir.TypeJSON:        {"String"},
}

// ReadableFrom implements graph.TypeSupport.
func (TypeSupport) ReadableFrom(t, elem ir.LogicalType) []string {
	if t != ir.TypeArray {
		return readable[t]
	}
	if elem == ir.TypeBytes || elem == ir.TypeJSON {
		return nil
	}
	var out []string
	for _, r := range readable[elem] {
		out = append(out, r+"Array")
	}
	return out
}

// keyTypes are the property types key properties must have. They match what
// the forward direction writes (decimal and uuid as strings), and Cypher can
// order them. Byte arrays cannot be ordered, so bytes keys are excluded.
var keyTypes = map[ir.LogicalType]string{
	ir.TypeBool:        "Boolean",
	ir.TypeInt64:       "Long",
	ir.TypeDecimal:     "String",
	ir.TypeString:      "String",
	ir.TypeUUID:        "String",
	ir.TypeDate:        "Date",
	ir.TypeTimestamp:   "LocalDateTime",
	ir.TypeTimestampTZ: "DateTime",
}

// KeyStoredAs implements graph.TypeSupport.
func (TypeSupport) KeyStoredAs(t ir.LogicalType) (string, bool) {
	s, ok := keyTypes[t]
	return s, ok
}

// EncodeProperties implements graph.Adapter. Null values become nil, which
// MergeNodes uses to remove stale properties.
func (a *Adapter) EncodeProperties(props []graph.Property) (graph.Encoded, error) {
	return EncodeProperties(props)
}

// EncodeProperties converts properties for Neo4j without a connection, for
// example to find conversion failures during a dry run.
func EncodeProperties(props []graph.Property) (graph.Encoded, error) {
	out := make(graph.Encoded, len(props))
	for _, p := range props {
		if p.Value.Null {
			out[p.Name] = nil
			continue
		}
		v, err := encodeValue(p.Type, p.Element, p.Value)
		if err != nil {
			return nil, rtgerr.New(rtgerr.ExecConversion, "property %s: %v", p.Name, err)
		}
		out[p.Name] = v
	}
	return out, nil
}

func encodeValue(t, elem ir.LogicalType, v ir.Value) (any, error) {
	switch t {
	case ir.TypeArray:
		list := make([]any, len(v.Elements))
		for i, e := range v.Elements {
			if e.Null {
				return nil, rtgerr.New(rtgerr.ExecConversion, "array element %d is NULL; Neo4j lists cannot contain null", i)
			}
			x, err := encodeValue(elem, "", e)
			if err != nil {
				return nil, err
			}
			list[i] = x
		}
		return list, nil
	case ir.TypeBool:
		return v.Text == "true", nil
	case ir.TypeInt64:
		return parseInt64(v.Text)
	case ir.TypeFloat64:
		return parseFloat64(v.Text)
	case ir.TypeDecimal, ir.TypeString, ir.TypeUUID:
		return v.Text, nil
	case ir.TypeJSON:
		return string(v.JSON), nil
	case ir.TypeBytes:
		return base64.StdEncoding.DecodeString(v.Text)
	case ir.TypeDate:
		d, err := time.Parse("2006-01-02", v.Text)
		if err != nil {
			return nil, err
		}
		return driver.DateOf(d), nil
	case ir.TypeTimestamp:
		ts, err := time.Parse("2006-01-02T15:04:05.999999999", v.Text)
		if err != nil {
			return nil, err
		}
		return driver.LocalDateTimeOf(ts), nil
	case ir.TypeTimestampTZ:
		ts, err := time.Parse(time.RFC3339Nano, v.Text)
		if err != nil {
			return nil, err
		}
		// Canonical timestamptz values are UTC. They are sent with the UTC
		// location and stored as DateTime values in zone "UTC". (An anonymous
		// fixed zero offset is rejected by Neo4j 5.26 over Bolt 5.)
		return ts.UTC(), nil
	default:
		return nil, rtgerr.New(rtgerr.PlanUnsupported, "type %s cannot be written to Neo4j by this engine version", t)
	}
}
