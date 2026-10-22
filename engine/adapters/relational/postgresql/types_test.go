package postgresql

import (
	"strings"
	"testing"

	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

func TestNumericTypmod(t *testing.T) {
	// numeric(12,2): typmod = (12 << 16 | 2) + 4
	if p, s, ok := numericPrecisionScale(12<<16 | 2 + 4); !ok || p != 12 || s != 2 {
		t.Errorf("numeric(12,2) decoded as %d,%d,%v", p, s, ok)
	}
	// numeric(5,-2) on PostgreSQL 15+: scale stored as 11-bit two's complement.
	if p, s, ok := numericPrecisionScale(5<<16 | (0x7ff & -2) + 4); !ok || p != 5 || s != -2 {
		t.Errorf("numeric(5,-2) decoded as %d,%d,%v", p, s, ok)
	}
	if _, _, ok := numericPrecisionScale(-1); ok {
		t.Error("unconstrained numeric must report no precision")
	}
}

func TestTimeAndIntervalTypmod(t *testing.T) {
	if timePrecision(-1) != 6 || timePrecision(3) != 3 || timePrecision(0) != 0 {
		t.Error("timePrecision")
	}
	// interval with no precision: fields mask in high bits, 0xFFFF precision.
	if intervalPrecision(-1) != 6 || intervalPrecision(0x7fff0000|0xffff) != 6 || intervalPrecision(0x7fff0000|3) != 3 {
		t.Error("intervalPrecision")
	}
	if n, ok := charLength(20 + 4); !ok || n != 20 {
		t.Error("charLength")
	}
}

// fakeTypes builds a lookup over a fixed type table.
func fakeTypes(types ...*pgType) typeLookup {
	m := map[uint32]*pgType{}
	for _, t := range types {
		m[t.OID] = t
	}
	return func(oid uint32) (*pgType, error) {
		if t, ok := m[oid]; ok {
			return t, nil
		}
		return nil, rtgerr.New(rtgerr.Internal, "unknown oid %d", oid)
	}
}

func TestMapType(t *testing.T) {
	lookup := fakeTypes(
		&pgType{OID: 20, Namespace: "pg_catalog", Name: "int8", Type: 'b'},
		&pgType{OID: 1700, Namespace: "pg_catalog", Name: "numeric", Type: 'b'},
		&pgType{OID: 1231, Namespace: "pg_catalog", Name: "_numeric", Type: 'b', Category: 'A', Elem: 1700},
		&pgType{OID: 600, Namespace: "pg_catalog", Name: "point", Type: 'b'},
		&pgType{OID: 1017, Namespace: "pg_catalog", Name: "_point", Type: 'b', Category: 'A', Elem: 600},
		&pgType{OID: 9001, Namespace: "public", Name: "mood", Type: 'e', EnumVals: []string{"sad", "ok", "happy"}},
		&pgType{OID: 9002, Namespace: "public", Name: "price", Type: 'd', BaseType: 1700, BaseMod: 10<<16 | 2 + 4},
		&pgType{OID: 9003, Namespace: "public", Name: "citext", Type: 'b'},
	)

	cases := []struct {
		oid    uint32
		typmod int32
		want   ir.LogicalType
	}{
		{oid: 20, typmod: -1, want: ir.TypeInt64},
		{oid: 1700, typmod: 12<<16 | 2 + 4, want: ir.TypeDecimal},
		{oid: 1231, typmod: 8<<16 | 3 + 4, want: ir.TypeArray},
		{oid: 9001, typmod: -1, want: ir.TypeString},
		{oid: 9002, typmod: -1, want: ir.TypeDecimal},
		{oid: 600, typmod: -1},
		{oid: 1017, typmod: -1},
		{oid: 9003, typmod: -1},
	}
	for _, c := range cases {
		info, reason, err := mapType(lookup, c.oid, c.typmod, 0)
		if err != nil {
			t.Fatal(err)
		}
		if c.want == "" {
			if info.Type != nil || reason == "" {
				t.Errorf("oid %d: expected unsupported with a reason, got %+v %q", c.oid, info, reason)
			}
			continue
		}
		if info.Type == nil || *info.Type != c.want {
			t.Errorf("oid %d: got %+v (%s), want %s", c.oid, info, reason, c.want)
		}
	}

	// Details.
	arr, _, _ := mapType(lookup, 1231, 8<<16|3+4, 0)
	if arr.Element == nil || *arr.Element.Type != ir.TypeDecimal || *arr.Element.Precision != 8 || *arr.Element.Scale != 3 {
		t.Errorf("numeric(8,3)[] element: %+v", arr.Element)
	}
	dom, _, _ := mapType(lookup, 9002, -1, 0)
	if *dom.Precision != 10 || *dom.Scale != 2 {
		t.Errorf("domain over numeric(10,2): %+v", dom)
	}
	enum, _, _ := mapType(lookup, 9001, -1, 0)
	if strings.Join(enum.EnumValues, ",") != "sad,ok,happy" {
		t.Errorf("enum values: %v", enum.EnumValues)
	}
}

func TestFKAction(t *testing.T) {
	for code, want := range map[string]string{"a": "no-action", "r": "restrict", "c": "cascade", "n": "set-null", "d": "set-default"} {
		if got := string(fkAction(code)); got != want {
			t.Errorf("fkAction(%q) = %q, want %q", code, got, want)
		}
	}
}
