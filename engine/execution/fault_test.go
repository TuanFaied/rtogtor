package execution

import (
	"context"
	"testing"
	"time"

	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/runstate"
)

func TestRetryPolicyIsRecordedAndKeptOnResume(t *testing.T) {
	src, g, store := &fakeSource{rows: people(9)}, newFakeGraph(), &runstate.Store{Dir: t.TempDir()}
	opts := options(t, src, g, store)
	opts.Retry = RetryPolicy{MaxAttempts: 5, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}
	opts.Fault = func(step string, batch int64, point FaultPoint) error {
		if step == "nodes:Person" && batch == 1 && point == AfterCheckpoint {
			return rtgerr.New(rtgerr.ExecInterrupted, "stop")
		}
		return nil
	}
	st, err := Run(context.Background(), opts)
	if !rtgerr.IsCode(err, rtgerr.ExecInterrupted) {
		t.Fatal(err)
	}
	want := runstate.RetryPolicy{MaxAttempts: 5, InitialBackoffMs: 1, MaxBackoffMs: 2}
	if st.Retry == nil || *st.Retry != want {
		t.Fatalf("retry = %+v", st.Retry)
	}

	// Resuming without a policy keeps the stored one.
	opts.Retry, opts.Fault, opts.ResumeRunID = RetryPolicy{}, nil, st.RunID
	st, err = Run(context.Background(), opts)
	if err != nil || st.Retry == nil || *st.Retry != want {
		t.Fatalf("resume: %v, retry %+v", err, st.Retry)
	}
	validate(t, store, st.RunID)
}

func TestWriteFaultsAreRetriedAndCounted(t *testing.T) {
	src, g, store := &fakeSource{rows: people(6)}, newFakeGraph(), &runstate.Store{Dir: t.TempDir()}
	opts := options(t, src, g, store)
	fired := 0
	opts.Fault = func(step string, batch int64, point FaultPoint) error {
		if step == "nodes:Person" && point == Write && fired < 2 {
			fired++
			return rtgerr.New(rtgerr.ExecBatchFailed, "transient")
		}
		return nil
	}
	st, err := Run(context.Background(), opts)
	if err != nil || st.Status != runstate.StatusSucceeded || st.Retries != 2 {
		t.Fatalf("got %v, status %s, retries %d", err, st.Status, st.Retries)
	}
	if len(g.nodes["Person"]) != 6 {
		t.Errorf("nodes = %d", len(g.nodes["Person"]))
	}
	rep, _ := store.ReadFile(st.RunID, "report.json")
	if len(rep) == 0 {
		t.Fatal("no report")
	}
	validate(t, store, st.RunID)

	// One attempt: no retries, the run stops as interrupted.
	opts = options(t, &fakeSource{rows: people(6)}, newFakeGraph(), store)
	opts.Retry = RetryPolicy{MaxAttempts: 1}
	opts.Fault = func(step string, _ int64, point FaultPoint) error {
		if point == Write && step == "nodes:Person" {
			return rtgerr.New(rtgerr.ExecBatchFailed, "transient")
		}
		return nil
	}
	st, err = Run(context.Background(), opts)
	if !rtgerr.IsCode(err, rtgerr.ExecBatchFailed) || st.Status != runstate.StatusInterrupted || st.Retries != 0 {
		t.Fatalf("got %v, status %s, retries %d", err, st.Status, st.Retries)
	}
}

func TestInvalidRetryPolicy(t *testing.T) {
	opts := options(t, &fakeSource{}, newFakeGraph(), &runstate.Store{Dir: t.TempDir()})
	opts.Retry = RetryPolicy{MaxAttempts: 101}
	if _, err := Run(context.Background(), opts); !rtgerr.IsCode(err, rtgerr.CLIUsage) {
		t.Fatalf("got %v", err)
	}
}

func TestFaultFromEnv(t *testing.T) {
	t.Setenv(FaultEnv, "step=nodes:Person;batch=1;point=before-checkpoint|point=write;times=2")
	f, err := FaultFromEnv()
	if err != nil || f == nil {
		t.Fatal(err)
	}
	if err := f("nodes:Person", 0, BeforeCheckpoint); err != nil {
		t.Errorf("batch 0 must not fire: %v", err)
	}
	if err := f("nodes:Person", 1, BeforeCheckpoint); !rtgerr.IsCode(err, rtgerr.ExecInterrupted) {
		t.Errorf("got %v", err)
	}
	if err := f("nodes:Person", 1, BeforeCheckpoint); err != nil {
		t.Errorf("times=1 fired twice: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := f("any", int64(i), Write); !rtgerr.IsCode(err, rtgerr.ExecBatchFailed) {
			t.Errorf("write fault %d: %v", i, err)
		}
	}
	if err := f("any", 5, Write); err != nil {
		t.Errorf("times=2 fired three times: %v", err)
	}

	for _, bad := range []string{"point=nowhere", "step", "point=write;times=0", "point=write;code=RTG-NOPE-001", "point=write;batch=x", "point=write;color=red"} {
		t.Setenv(FaultEnv, bad)
		if _, err := FaultFromEnv(); !rtgerr.IsCode(err, rtgerr.CLIUsage) {
			t.Errorf("%q: got %v", bad, err)
		}
	}
	t.Setenv(FaultEnv, "")
	if f, err := FaultFromEnv(); f != nil || err != nil {
		t.Errorf("unset: %v %v", f != nil, err)
	}
}
