// Package testutil holds helpers shared by engine tests.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// RepoPath returns the path of a file relative to the repository root, found
// by walking up from the current directory until a directory containing
// "specs" and "conformance" is found. The test is skipped when the engine is
// built outside the repository (for example from the module cache).
func RepoPath(t testing.TB, elem ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if isDir(filepath.Join(dir, "specs")) && isDir(filepath.Join(dir, "conformance")) {
			return filepath.Join(append([]string{dir}, elem...)...)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repository root not found; shared specs and vectors are unavailable")
		}
		dir = parent
	}
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
