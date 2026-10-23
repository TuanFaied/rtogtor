package ir

import "testing"

// Benchmarks for the per-value hot paths (docs/performance.md).

func BenchmarkIdentityCompositeKey(b *testing.B) {
	comps := []KeyComponent{{Type: TypeInt64, Value: "1001"}, {Type: TypeInt64, Value: "2"}}
	for b.Loop() {
		if _, err := Identity("OrderItem", comps...); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCanonicalize(b *testing.B) {
	for _, c := range []struct {
		t  LogicalType
		in string
	}{
		{TypeInt64, "-9223372036854775808"},
		{TypeDecimal, "000123456789.123456789000"},
		{TypeTimestampTZ, "2026-03-20T10:15:00.123456+02:00"},
		{TypeUUID, "550E8400-E29B-41D4-A716-446655440000"},
		{TypeString, "Grace Hopper — rear admiral"},
	} {
		b.Run(string(c.t), func(b *testing.B) {
			for b.Loop() {
				if _, err := Canonicalize(c.t, c.in); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
