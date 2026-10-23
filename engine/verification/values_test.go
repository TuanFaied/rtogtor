package verification

import (
	"testing"

	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/planning"
)

func TestEqualValues(t *testing.T) {
	text := ir.TextValue
	arr := func(v ...ir.Value) ir.Value { return ir.Value{Elements: v} }
	cases := []struct {
		a, b ir.Value
		want bool
	}{
		{text("19.9"), text("19.9"), true},
		{text("19.9"), text("19.90"), false}, // canonical forms are compared; both sides canonicalize
		{ir.NullValue(), ir.NullValue(), true},
		{ir.NullValue(), text(""), false},
		{text(""), ir.NullValue(), false},
		{ir.Value{JSON: []byte(`{"a":1}`)}, ir.Value{JSON: []byte(`{"a":1}`)}, true},
		{ir.Value{JSON: []byte(`{"a":1}`)}, ir.Value{JSON: []byte(`{"a":2}`)}, false},
		{arr(text("1"), ir.NullValue()), arr(text("1"), ir.NullValue()), true},
		{arr(text("1")), arr(text("1"), text("2")), false},
		{arr(), arr(), true},
	}
	for i, c := range cases {
		if got := equalValues(c.a, c.b); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}

func TestComparedSkipsChangingLosses(t *testing.T) {
	plan := &planning.Plan{Transformations: []planning.Transformation{
		{Entity: "P", Property: "price", LossKind: "representation"},
		{Entity: "P", Property: "at", LossKind: "precision"},
		{Entity: "Q", Property: "name", LossKind: "precision"},
	}}
	fields := []planning.Field{{Property: "price"}, {Property: "at"}, {Property: "name"}}
	if got := compared(plan, "P", fields); len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("compared = %v", got)
	}
}
