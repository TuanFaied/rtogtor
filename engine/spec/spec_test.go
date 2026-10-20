package spec

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedSpecsMatchSource fails when engine/spec/data drifts from the
// repository's specs/ directory. Fix it with `go generate ./...`.
func TestEmbeddedSpecsMatchSource(t *testing.T) {
	srcDir := filepath.Join("..", "..", "specs")
	if _, err := os.Stat(srcDir); err != nil {
		t.Skip("specs/ not available (module built outside the repository)")
	}

	sources, err := filepath.Glob(filepath.Join(srcDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := data.ReadDir("data")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != len(embedded) {
		t.Fatalf("specs/ has %d JSON files but %d are embedded; run `go generate ./...`", len(sources), len(embedded))
	}
	for _, src := range sources {
		name := filepath.Base(src)
		want, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := File(name)
		if err != nil {
			t.Fatalf("%s: %v; run `go generate ./...`", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("embedded %s differs from specs/%s; run `go generate ./...`", name, name)
		}
	}
}

func TestFilesAreEmbedded(t *testing.T) {
	for _, name := range Files {
		if _, err := File(name); err != nil {
			t.Error(err)
		}
	}
}

func TestSupports(t *testing.T) {
	cases := map[string]bool{
		"0.1.0":  true,
		"0.1.7":  true,
		"0.2.0":  false,
		"0.0.9":  false,
		"1.1.0":  false,
		"0.1":    false,
		"0.01.0": false,
		"v0.1.0": false,
		"":       false,
	}
	for v, want := range cases {
		if got := Supports(v); got != want {
			t.Errorf("Supports(%q) = %v, want %v", v, got, want)
		}
	}
}
