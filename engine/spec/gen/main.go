// Command gen copies the machine-readable specification files (specs/*.json)
// from the repository root into engine/spec/data so they can be embedded.
//
// Run it with `go generate ./...` from engine/.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	// go generate runs in the package directory: engine/spec.
	srcDir := filepath.Join("..", "..", "specs")
	dstDir := "data"

	sources, err := filepath.Glob(filepath.Join(srcDir, "*.json"))
	if err != nil {
		fail(err)
	}
	if len(sources) == 0 {
		fail(fmt.Errorf("no *.json files found in %s", srcDir))
	}

	// Remove stale copies so deleted spec files do not linger.
	stale, err := filepath.Glob(filepath.Join(dstDir, "*.json"))
	if err != nil {
		fail(err)
	}
	for _, f := range stale {
		if err := os.Remove(f); err != nil {
			fail(err)
		}
	}

	// Checked-in source files: world-readable like the rest of the repository.
	if err := os.MkdirAll(dstDir, 0o755); err != nil { //nolint:gosec // see above
		fail(err)
	}
	for _, src := range sources {
		b, err := os.ReadFile(src)
		if err != nil {
			fail(err)
		}
		name := filepath.Base(src)
		if err := os.WriteFile(filepath.Join(dstDir, name), b, 0o644); err != nil { //nolint:gosec // see above
			fail(err)
		}
		fmt.Printf("copied specs/%s\n", name)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "spec gen:", err)
	os.Exit(1)
}
