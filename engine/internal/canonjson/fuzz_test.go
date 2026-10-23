package canonjson

import "testing"

// FuzzCanonicalize: RFC 8785 canonicalization never panics and is
// idempotent.
func FuzzCanonicalize(f *testing.F) {
	for _, s := range []string{`{"b":1,"a":[true,null,"é"]}`, `1e21`, `-0`, `"\ud800"`, `[1.5,{"":{}}]`, `12345678901234567890`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		c, err := Canonicalize(in)
		if err != nil {
			return
		}
		again, err := Canonicalize(c)
		if err != nil || string(again) != string(c) {
			t.Fatalf("%q -> %q is not idempotent (%q, %v)", in, c, again, err)
		}
	})
}
