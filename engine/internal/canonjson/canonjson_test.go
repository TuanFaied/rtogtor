package canonjson

import (
	"errors"
	"testing"
)

func TestCanonicalize(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"whitespace and key order", `{ "b": 1, "a": [ true, false, null ] }`, `{"a":[true,false,null],"b":1}`},
		{"nested objects", `{"z":{"y":2,"x":1},"a":{}}`, `{"a":{},"z":{"x":1,"y":2}}`},
		// RFC 8785 §3.2.2.3 number examples.
		// (333333333.33333329 is rejected instead: see TestRejectsInexactNumbers.)
		{"numbers", `[333333333.3333333, 1E30, 4.50, 2e-3, 0.000000000000000000000000001, -0, 1e21, 1e20]`,
			`[333333333.3333333,1e+30,4.5,0.002,1e-27,0,1e+21,100000000000000000000]`},
		// RFC 8785 §3.2.2.2: only required escapes; non-ASCII stays literal.
		{"strings", `"\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/"`, "\"€$\\u000f\\nA'B\\\"\\\\\\\\\\\"/\""},
		{"html characters are not escaped", `"<a & b>"`, `"<a & b>"`},
		// RFC 8785 §3.2.3: sorting by UTF-16 code units. UTF-8 byte order
		// would place the emoji (U+1F600, surrogates D83D DE00) after U+FB33.
		{"utf-16 key order",
			`{"\ufb33":7,"\ud83d\ude00":6,"\u20ac":5,"\u00f6":4,"\u0080":3,"1":2,"\r":1}`,
			"{\"\\r\":1,\"1\":2,\"\u0080\":3,\"ö\":4,\"€\":5,\"😀\":6,\"דּ\":7}"},
	}
	for _, c := range cases {
		got, err := Canonicalize([]byte(c.in))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestRejectsInvalid(t *testing.T) {
	for _, in := range []string{`{} {}`, `[1e400]`, `{"a":}`} {
		if _, err := Canonicalize([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", in)
		}
	}
}

func TestMarshalStruct(t *testing.T) {
	v := struct {
		Zeta  string `json:"zeta"`
		Alpha int    `json:"alpha"`
	}{"<ok>", 42}
	got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"alpha":42,"zeta":"<ok>"}` {
		t.Errorf("got %s", got)
	}
}

func TestRejectsInexactNumbers(t *testing.T) {
	for _, in := range []string{`333333333.33333329`, `12345678901234567890`, `[0.1000000000000000000001]`, `{"n":9007199254740993}`} {
		if _, err := Canonicalize([]byte(in)); !errors.Is(err, ErrInexactNumber) {
			t.Errorf("%s: got %v, want ErrInexactNumber", in, err)
		}
	}
	for _, in := range []string{`0.1`, `4.50`, `1E30`, `9007199254740992`, `-0.0`} {
		if _, err := Canonicalize([]byte(in)); err != nil {
			t.Errorf("%s: unexpected error %v", in, err)
		}
	}
}
