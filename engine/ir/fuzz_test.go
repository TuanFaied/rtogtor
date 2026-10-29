package ir

import "testing"

// FuzzCanonicalize: canonicalization never panics, and canonical text is a
// fixed point (canonicalizing it again changes nothing).
func FuzzCanonicalize(f *testing.F) {
	types := []LogicalType{TypeBool, TypeInt64, TypeDecimal, TypeFloat64, TypeString, TypeUUID, TypeDate,
		TypeTime, TypeTimeTZ, TypeTimestamp, TypeTimestampTZ, TypeDuration, TypeBytes}
	for _, s := range []string{"0", "-0.10", "1e309", "2026-02-29", "2026-03-20T10:15:00.123456789+14:00",
		"P1M-2DT3.5S", "AAEC", "550E8400-E29B-41D4-A716-446655440000", "\xff", "NaN", "24:00:00"} {
		f.Add(uint8(0), s)
		f.Add(uint8(len(s)), s)
	}
	f.Fuzz(func(t *testing.T, ti uint8, in string) {
		typ := types[int(ti)%len(types)]
		c, err := Canonicalize(typ, in)
		if err != nil {
			return
		}
		again, err := Canonicalize(typ, c)
		if err != nil || again != c {
			t.Fatalf("%s %q -> %q is not a fixed point (%q, %v)", typ, in, c, again, err)
		}
	})
}
