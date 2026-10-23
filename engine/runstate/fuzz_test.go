package runstate

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzFailures: reading a corrupt or torn failures.jsonl never panics.
func FuzzFailures(f *testing.F) {
	f.Add([]byte(`{"stepId":"nodes:A","batch":0,"key":[],"code":"RTG-EXEC-001","message":"x","at":"2026-01-01T00:00:00Z"}` + "\n" + `{"stepId":`))
	f.Add([]byte("\n\n{}\n[]\nnull\n"))
	s := &Store{Dir: f.TempDir()}
	st := &State{RunID: NewRunID(), Streams: []Stream{{StepID: "nodes:A", Batches: 1}}}
	if err := s.Create(st, []byte("{}"), []byte("{}")); err != nil {
		f.Fatal(err)
	}
	dir, _ := s.runDir(st.RunID)
	path := filepath.Join(dir, "failures.jsonl")
	f.Fuzz(func(t *testing.T, data []byte) {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = s.Failures(st)
	})
}
