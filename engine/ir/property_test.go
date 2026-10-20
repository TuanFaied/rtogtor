package ir

import (
	"bytes"
	"encoding/binary"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Canonicalization must be idempotent for every scalar type.
func TestCanonicalizeIsIdempotent(t *testing.T) {
	gens := map[LogicalType]*rapid.Generator[string]{
		TypeInt64:       rapid.Map(rapid.Int64(), func(n int64) string { return strconv.FormatInt(n, 10) }),
		TypeDecimal:     genDecimalText(),
		TypeFloat64:     rapid.Map(rapid.Float64(), func(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }),
		TypeTimestampTZ: genTimestampTZText(),
		TypeString:      rapid.String(),
	}
	for typ, gen := range gens {
		t.Run(string(typ), func(t *testing.T) {
			rapid.Check(t, func(t *rapid.T) {
				in := gen.Draw(t, "input")
				c1, err := Canonicalize(typ, in)
				if err != nil {
					t.Skip("input rejected")
				}
				c2, err := Canonicalize(typ, c1)
				if err != nil || c1 != c2 {
					t.Fatalf("not idempotent: %q -> %q -> %q (%v)", in, c1, c2, err)
				}
			})
		})
	}
}

// Leading zeros and a '+' sign never change an int64 or decimal.
func TestIntegerNormalization(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Int64Min(0).Draw(t, "n")
		zeros := strings.Repeat("0", rapid.IntRange(0, 5).Draw(t, "zeros"))
		want := strconv.FormatInt(n, 10)
		for _, typ := range []LogicalType{TypeInt64, TypeDecimal} {
			got, err := Canonicalize(typ, "+"+zeros+want)
			if err != nil || got != want {
				t.Fatalf("%s: +%s%s -> %q, %v; want %q", typ, zeros, want, got, err, want)
			}
		}
	})
}

// Canonical float text must parse back to the identical float64 bits.
func TestFloat64RoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := math.Float64frombits(rapid.Uint64().Draw(t, "bits"))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			t.Skip("special value")
		}
		text := FormatFloat64(f)
		var back float64
		if text == "-0" {
			back = math.Copysign(0, -1)
		} else {
			var err error
			back, err = strconv.ParseFloat(text, 64)
			if err != nil {
				t.Fatalf("FormatFloat64(%v) = %q does not parse: %v", f, text, err)
			}
		}
		if math.Float64bits(back) != math.Float64bits(f) {
			t.Fatalf("round trip changed value: %v -> %q -> %v", f, text, back)
		}
	})
}

// A timestamptz and its UTC canonical form denote the same instant.
func TestTimestampTZPreservesInstant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genTimestampTZText().Draw(t, "ts")
		c, err := Canonicalize(TypeTimestampTZ, in)
		if err != nil {
			t.Skip("out of range")
		}
		a, err1 := time.Parse(time.RFC3339Nano, in)
		b, err2 := time.Parse(time.RFC3339Nano, c)
		if err1 != nil || err2 != nil {
			t.Fatalf("parse: %v %v", err1, err2)
		}
		if !a.Equal(b) {
			t.Fatalf("%s and canonical %s are different instants", in, c)
		}
	})
}

// The serialization is unambiguous: it can be decoded back into exactly the
// components it was built from, so distinct keys never share a serialization.
func TestSerializationIsDecodable(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		entity := rapid.StringN(1, 20, -1).Draw(t, "entity")
		n := rapid.IntRange(1, 4).Draw(t, "n")
		comps := make([]KeyComponent, n)
		for i := range comps {
			comps[i] = KeyComponent{Type: TypeString, Value: rapid.String().Draw(t, "v")}
		}
		k, err := NewKey(entity, comps)
		if err != nil {
			t.Fatal(err)
		}
		gotEntity, gotValues := decodeSerialization(t, k.Serialize())
		if gotEntity != entity || len(gotValues) != n {
			t.Fatalf("decoded %q with %d values, want %q with %d", gotEntity, len(gotValues), entity, n)
		}
		for i := range comps {
			if gotValues[i] != comps[i].Value {
				t.Fatalf("component %d decoded as %q, want %q", i, gotValues[i], comps[i].Value)
			}
		}
	})
}

func decodeSerialization(t *rapid.T, b []byte) (string, []string) {
	if !bytes.HasPrefix(b, []byte(identityDomain)) {
		t.Fatal("missing domain separator")
	}
	b = b[len(identityDomain):]
	readLP := func() string {
		l := binary.BigEndian.Uint32(b)
		s := string(b[4 : 4+l])
		b = b[4+l:]
		return s
	}
	entity := readLP()
	count := binary.BigEndian.Uint32(b)
	b = b[4:]
	values := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		colon := bytes.IndexByte(b, ':')
		b = b[colon+1:]
		values = append(values, readLP())
	}
	if len(b) != 0 {
		t.Fatalf("%d trailing bytes", len(b))
	}
	return entity, values
}

func genDecimalText() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		sign := rapid.SampledFrom([]string{"", "+", "-"}).Draw(t, "sign")
		intPart := rapid.StringMatching(`[0-9]{1,30}`).Draw(t, "int")
		if rapid.Bool().Draw(t, "hasFrac") {
			return sign + intPart + "." + rapid.StringMatching(`[0-9]{1,20}`).Draw(t, "frac")
		}
		return sign + intPart
	})
}

func genTimestampTZText() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		sec := rapid.Int64Range(time.Date(1, 1, 2, 0, 0, 0, 0, time.UTC).Unix(), time.Date(9999, 12, 30, 0, 0, 0, 0, time.UTC).Unix()).Draw(t, "sec")
		ns := rapid.IntRange(0, 999_999_999).Draw(t, "ns")
		offMin := rapid.IntRange(-18*60, 18*60).Draw(t, "offset")
		zone := time.FixedZone("", offMin*60)
		return time.Unix(sec, int64(ns)).In(zone).Format(time.RFC3339Nano)
	})
}
