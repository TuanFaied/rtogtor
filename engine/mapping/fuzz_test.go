package mapping

import (
	"os"
	"testing"

	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
)

// FuzzDecode: decoding and validating untrusted mapping documents never
// panics, in either format.
func FuzzDecode(f *testing.F) {
	for _, name := range []string{"mapping.yaml", "mapping-reverse.yaml"} {
		b, err := os.ReadFile(testutil.RepoPath(f, "examples", "ecommerce", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, true)
	}
	f.Add([]byte(`{"specVersion":"0.1.0","entities":[{"key":[]}]}`), false)
	f.Add([]byte("a: &x [*x]\n"), true)
	f.Fuzz(func(t *testing.T, doc []byte, yaml bool) {
		format := FormatJSON
		if yaml {
			format = FormatYAML
		}
		m, issues := Decode(doc, format)
		if m == nil && !issues.HasErrors() {
			t.Fatal("no mapping and no error")
		}
	})
}
