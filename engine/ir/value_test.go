package ir

import "testing"

func TestEncodeJSON(t *testing.T) {
	cases := []struct {
		v         Value
		typ, elem LogicalType
		want      string
	}{
		{NullValue(), TypeInt64, "", "null"},
		{TextValue("9007199254740993"), TypeInt64, "", `"9007199254740993"`},
		{TextValue("12.5"), TypeDecimal, "", `"12.5"`},
		{TextValue("1e+21"), TypeFloat64, "", `1e+21`},
		{TextValue("NaN"), TypeFloat64, "", `"NaN"`},
		{TextValue("-0"), TypeFloat64, "", `"-0"`},
		{TextValue("true"), TypeBool, "", `true`},
		{TextValue(`say "hi"`), TypeString, "", `"say \"hi\""`},
		{Value{JSON: []byte(`{"a":1}`)}, TypeJSON, "", `{"a":1}`},
		{Value{Elements: []Value{TextValue("1"), NullValue()}}, TypeArray, TypeInt64, `["1",null]`},
	}
	for _, c := range cases {
		got, err := c.v.EncodeJSON(c.typ, c.elem)
		if err != nil {
			t.Errorf("%s %+v: %v", c.typ, c.v, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s %+v: got %s, want %s", c.typ, c.v, got, c.want)
		}
	}
	if _, err := TextValue("yes").EncodeJSON(TypeBool, ""); err == nil {
		t.Error("invalid bool text must fail")
	}
}
