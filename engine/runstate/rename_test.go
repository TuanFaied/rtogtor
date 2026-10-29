package runstate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestRenameRetriesWhileTargetIsOpen holds the target open, as a virus
// scanner would, and releases it shortly after the rename starts.
func TestRenameRetriesWhileTargetIsOpen(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("replacing an open file only fails on Windows")
	}
	dir := t.TempDir()
	target, tmp := filepath.Join(dir, "state.json"), filepath.Join(dir, "new.tmp")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, target); err == nil {
		t.Skip("this Windows version replaces open files; nothing to retry")
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = f.Close()
	}()
	if err := rename(tmp, target); err != nil {
		t.Fatalf("rename did not retry: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Errorf("target = %q", b)
	}
}
