package execution

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

// FaultEnv names the environment variable holding test faults. It exists so
// that tests in every language can stop a real engine at a precise point
// (docs/development.md); it must never be set in production.
//
// The value is one or more faults separated by "|", each a list of
// key=value pairs separated by ";":
//
//	step=rows:Customer;batch=1;point=before-checkpoint;times=1;code=RTG-EXEC-004
//
// step is a plan step ID or "*"; batch is a batch number or "*" (default
// "*"); point is a FaultPoint; times is how often the fault fires in this
// process (default 1); code is the error code returned (default
// RTG-EXEC-003, a retryable transient error, for point=write and
// RTG-EXEC-004, an interruption, otherwise).
const FaultEnv = "RTOGTOR_TEST_FAULT"

type envFault struct {
	step, batch string
	point       FaultPoint
	code        rtgerr.Code
	remaining   int
}

var (
	envFaultsMu   sync.Mutex
	envFaults     []*envFault
	envFaultsRead string
	envFaultsSet  bool
)

// FaultFromEnv returns the injector described by RTOGTOR_TEST_FAULT, or nil
// when it is not set. The faults' remaining counts are kept per process, so
// a run resumed by the same engine process does not fail again.
func FaultFromEnv() (FaultInjector, error) {
	raw := strings.TrimSpace(os.Getenv(FaultEnv))
	if raw == "" {
		return nil, nil
	}
	envFaultsMu.Lock()
	defer envFaultsMu.Unlock()
	if !envFaultsSet || envFaultsRead != raw {
		faults, err := parseFaults(raw)
		if err != nil {
			return nil, err
		}
		envFaults, envFaultsRead, envFaultsSet = faults, raw, true
	}
	return func(step string, batch int64, point FaultPoint) error {
		envFaultsMu.Lock()
		defer envFaultsMu.Unlock()
		for _, f := range envFaults {
			if f.remaining == 0 || f.point != point || (f.step != "*" && f.step != step) ||
				(f.batch != "*" && f.batch != strconv.FormatInt(batch, 10)) {
				continue
			}
			f.remaining--
			return rtgerr.New(f.code, "injected test fault (%s) at %s, batch %d, %s", FaultEnv, step, batch, point)
		}
		return nil
	}, nil
}

func parseFaults(raw string) ([]*envFault, error) {
	bad := func(format string, args ...any) error {
		return rtgerr.New(rtgerr.CLIUsage, FaultEnv+": "+format, args...)
	}
	var out []*envFault
	for _, spec := range strings.Split(raw, "|") {
		f := &envFault{step: "*", batch: "*", remaining: 1}
		for _, kv := range strings.Split(spec, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
			if !ok {
				return nil, bad("%q is not key=value", kv)
			}
			switch k {
			case "step":
				f.step = v
			case "batch":
				if v != "*" {
					if _, err := strconv.ParseInt(v, 10, 64); err != nil {
						return nil, bad("batch %q is not a number or *", v)
					}
				}
				f.batch = v
			case "point":
				f.point = FaultPoint(v)
			case "times":
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 {
					return nil, bad("times %q is not a positive number", v)
				}
				f.remaining = n
			case "code":
				if _, ok := rtgerr.Lookup(rtgerr.Code(v)); !ok {
					return nil, bad("unknown error code %q", v)
				}
				f.code = rtgerr.Code(v)
			default:
				return nil, bad("unknown key %q", k)
			}
		}
		switch f.point {
		case BeforeWrite, BeforeCheckpoint, AfterCheckpoint:
			if f.code == "" {
				f.code = rtgerr.ExecInterrupted
			}
		case Write:
			if f.code == "" {
				f.code = rtgerr.ExecBatchFailed
			}
		default:
			return nil, bad("point %q is not one of before-write, write, before-checkpoint, after-checkpoint", f.point)
		}
		out = append(out, f)
	}
	return out, nil
}
