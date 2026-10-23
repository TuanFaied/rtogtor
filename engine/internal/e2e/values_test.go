//go:build integration

package e2e

import (
	"strings"
	"testing"

	"github.com/TuanFaied/rtogtor/engine/execution"
	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
	"github.com/TuanFaied/rtogtor/engine/reporting"
	"github.com/TuanFaied/rtogtor/engine/runstate"
	"github.com/TuanFaied/rtogtor/engine/verification"
)

func mismatchesOf(v *reporting.Verification) map[string]int64 {
	out := map[string]int64{}
	for _, e := range v.Entities {
		if e.ValueMismatches != nil {
			out[e.Entity] = *e.ValueMismatches
		}
	}
	return out
}

func TestValueVerificationForward(t *testing.T) {
	w := newWorld(t, ecommerce(t))
	m := w.load(testutil.RepoPath(t, "examples", "ecommerce", "mapping.yaml"))
	p := w.plan(m)
	if st, err := execution.Run(w.ctx, w.options(m, p, &runstate.Store{Dir: t.TempDir()}, 2)); err != nil || st.Status != runstate.StatusSucceeded {
		t.Fatalf("migrate: %v", err)
	}
	check := func() *reporting.Verification {
		t.Helper()
		v, err := verification.Verify(w.ctx, p, w.src, w.dst, 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := verification.CompareValues(w.ctx, p, w.src, w.dst, 2, v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := check()
	if v.Status != "passed" || len(mismatchesOf(v)) != 4 {
		t.Fatalf("values: %s %v %+v", v.Status, mismatchesOf(v), v.Issues)
	}
	for e, n := range mismatchesOf(v) {
		if n != 0 {
			t.Errorf("%s: %d mismatches", e, n)
		}
	}

	// Another writer changes a price and removes a name.
	w.cypher("MATCH (p:Product_"+w.sfx+" {id: 11}) SET p.price = '18.90' REMOVE p.name", nil)
	v = check()
	if v.Status != "failed" || mismatchesOf(v)["Product"] != 1 {
		t.Fatalf("after a change: %s %v", v.Status, mismatchesOf(v))
	}
	msg := v.Issues[len(v.Issues)-1].Message
	if !strings.Contains(msg, "(11): name, price") || strings.Contains(msg, "18.9") || strings.Contains(msg, "T-shirt") {
		t.Errorf("message must name the key and properties, not values: %s", msg)
	}
}

func TestValueVerificationReverse(t *testing.T) {
	w := newWorld(t, ecommerce(t))
	w.forward()
	back, conn := w.backSchema(true)
	m := w.reverseMapping(back)
	p := w.reversePlan(m)
	if st, err := execution.Run(w.ctx, w.reverseOptions(m, p, &runstate.Store{Dir: t.TempDir()}, 2)); err != nil || st.Status != runstate.StatusSucceeded {
		t.Fatalf("reverse: %v", err)
	}
	check := func() *reporting.Verification {
		t.Helper()
		v, err := verification.VerifyReverse(w.ctx, p, w.dst, w.src, 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := verification.CompareValuesReverse(w.ctx, p, w.dst, w.src, 2, v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := check(); v.Status != "passed" || mismatchesOf(v)["OrderItem"] != 0 || len(mismatchesOf(v)) != 4 {
		t.Fatalf("values: %s %v %+v", v.Status, mismatchesOf(v), v.Issues)
	}
	if _, err := conn.Exec(w.ctx, "UPDATE "+back+".order_items SET quantity = 7 WHERE order_id = 1001 AND line_no = 2"); err != nil {
		t.Fatal(err)
	}
	v := check()
	if v.Status != "failed" || mismatchesOf(v)["OrderItem"] != 1 || !strings.Contains(v.Issues[len(v.Issues)-1].Message, "(1001, 2): quantity") {
		t.Fatalf("after a change: %s %v %+v", v.Status, mismatchesOf(v), v.Issues)
	}
}
