package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// FuzzServe feeds untrusted lines to a handshaken server: it never panics,
// always returns, and every line it answers gets valid JSON. Methods that
// could reach a database are disabled, so fuzzing never dials a host.
func FuzzServe(f *testing.F) {
	f.Add([]byte(`{"protocolVersion":"1.0","id":"1","method":"canonicalize","params":{"type":"decimal","value":"1.50"}}`))
	f.Add([]byte(`{"protocolVersion":"1.0","id":"2","method":"identity","params":{"entityType":"A","key":[{"type":"int64","value":"1"}]}}`))
	f.Add([]byte(`{"protocolVersion":"1.0","id":"3","method":"validateMapping","params":{"mapping":{}}}`))
	f.Add([]byte(`{"protocolVersion":"1.0","id":"4","method":"plan","params":{"mapping":{},"sourceMetadata":{}}}`))
	f.Add([]byte(`{"protocolVersion":"9.0","id":"5","method":"cancel","params":{"id":"1"}}`))
	f.Add([]byte("not json\n{}\n{\"id\":\"x\"}"))
	f.Fuzz(func(t *testing.T, lines []byte) {
		var out bytes.Buffer
		s := NewServer(&out, io.Discard)
		s.lookup = func(string) (string, bool) { return "", false }
		for _, m := range []string{"inspect", "migrate", "verify"} {
			delete(s.handlers, m)
		}
		plan := s.handlers["plan"]
		s.handlers["plan"] = func(ctx context.Context, s *Server, id string, raw json.RawMessage) (any, error) {
			var p map[string]json.RawMessage
			if json.Unmarshal(raw, &p) == nil && (p["source"] != nil || p["destination"] != nil) {
				return nil, nil // would connect
			}
			return plan(ctx, s, id, raw)
		}
		in := `{"protocolVersion":"1.0","id":"h","method":"handshake","params":{"client":{"name":"fuzz","version":"0"},"protocolVersions":["1.0"]}}` + "\n"
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Serve(ctx, io.MultiReader(bytes.NewBufferString(in), bytes.NewReader(lines)))
		for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
			if len(line) > 0 && !json.Valid(line) {
				t.Fatalf("invalid JSON output: %q", line)
			}
		}
	})
}
