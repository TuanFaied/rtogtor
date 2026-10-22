package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
)

func run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := run("version", "--format", "json")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	var v versionInfo
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if v.SpecVersion != "0.1.0" || v.IdentityAlgorithm != "rtg-id/v1" || v.ProtocolVersions == nil {
		t.Errorf("unexpected version info: %+v", v)
	}
}

func TestValidateExample(t *testing.T) {
	path := testutil.RepoPath(t, "examples", "ecommerce", "mapping.yaml")

	code, out, _ := run("validate", "--mapping", path)
	if code != ExitOK || !strings.Contains(out, `OK: mapping "ecommerce" is valid (1 warning)`) {
		t.Errorf("exit %d, output:\n%s", code, out)
	}

	code, out, _ = run("validate", "--mapping", path, "--format", "json")
	var res validateResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if code != ExitOK || !res.Valid || res.Mapping != "ecommerce" {
		t.Errorf("exit %d, result %+v", code, res)
	}
}

func TestValidateInvalidMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	doc := `specVersion: "0.1.0"
name: bad
direction: relational-to-graph
source: { kind: postgresql }
destination: { kind: neo4j }
entities:
  - name: OrderItem
    table: order_items
    labels: [OrderItem]
    key: [order_id, line_no]
    properties: [{ column: order_id, property: orderId, type: int64 }]
`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("validate", "--mapping", path, "--format", "json")
	if code != ExitFailure {
		t.Fatalf("exit %d, want %d", code, ExitFailure)
	}
	var res validateResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == "RTG-MAP-005" && d.Path == "/entities/0/key/1" && d.Severity == "error" {
			found = true
		}
	}
	if res.Valid || !found {
		t.Errorf("expected RTG-MAP-005 at /entities/0/key/1, got %+v", res)
	}
}

func TestUsageErrors(t *testing.T) {
	cases := [][]string{
		{"validate"}, // missing --mapping
		{"validate", "--mapping", "x.yaml", "--bogus"}, // unknown flag
		{"version", "--format", "xml"},                 // bad format
		{"version", "extra"},                           // unexpected argument
		{"no-such-command"},
	}
	for _, args := range cases {
		code, _, errOut := run(args...)
		if code != ExitUsage || !strings.Contains(errOut, "RTG-CLI-001") {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
}

func TestInspectConnectionSettings(t *testing.T) {
	t.Setenv("RTG_TEST_EMPTY_URL", "")
	t.Setenv("RTG_TEST_MYSQL_URL", "mysql://user:Secret123@localhost/db")
	cases := map[string]string{
		"RTG_TEST_UNSET_URL": "is not set or is empty",
		"RTG_TEST_EMPTY_URL": "is not set or is empty",
		"RTG_TEST_MYSQL_URL": "must hold a postgres://",
	}
	for env, want := range cases {
		code, _, errOut := run("inspect", "--source-env", env)
		if code != ExitFailure || !strings.Contains(errOut, "RTG-CONN-002") || !strings.Contains(errOut, want) {
			t.Errorf("%s: exit %d, stderr %q", env, code, errOut)
		}
		if strings.Contains(errOut, "Secret123") {
			t.Errorf("%s: stderr leaks the URL: %q", env, errOut)
		}
	}
	if code, _, _ := run("inspect"); code != ExitUsage {
		t.Errorf("missing --source-env: exit %d, want %d", code, ExitUsage)
	}
}
