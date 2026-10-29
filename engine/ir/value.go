package ir

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Value is one attribute value in canonical form (specs/values.md). Its
// logical type is carried separately, by the plan or type metadata.
type Value struct {
	// Null marks SQL NULL. When Null is true the other fields are empty.
	Null bool
	// Text is the canonical text form, for scalar types.
	Text string
	// JSON is the RFC 8785 canonical JSON document, for TypeJSON.
	JSON []byte
	// Elements holds the elements, for TypeArray.
	Elements []Value
}

// NullValue returns a null value.
func NullValue() Value { return Value{Null: true} }

// TextValue returns a scalar value with the given canonical text.
func TextValue(text string) Value { return Value{Text: text} }

// EncodeJSON returns the JSON encoding of v for logical type t (and element
// type elem for arrays), as defined in specs/values.md §1.
func (v Value) EncodeJSON(t, elem LogicalType) ([]byte, error) {
	if v.Null {
		return []byte("null"), nil
	}
	switch t {
	case TypeBool:
		if v.Text != "true" && v.Text != "false" {
			return nil, fmt.Errorf("ir: invalid bool text %q", v.Text)
		}
		return []byte(v.Text), nil
	case TypeFloat64:
		switch v.Text {
		case "NaN", "Infinity", "-Infinity", "-0":
			return json.Marshal(v.Text)
		}
		return []byte(v.Text), nil
	case TypeJSON:
		if len(v.JSON) == 0 {
			return nil, fmt.Errorf("ir: json value has no document")
		}
		return v.JSON, nil
	case TypeArray:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, e := range v.Elements {
			if i > 0 {
				buf.WriteByte(',')
			}
			b, err := e.EncodeJSON(elem, "")
			if err != nil {
				return nil, err
			}
			buf.Write(b)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil
	default:
		if !t.Known() {
			return nil, fmt.Errorf("ir: unknown logical type %q", string(t))
		}
		return json.Marshal(v.Text)
	}
}
