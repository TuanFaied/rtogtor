package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TuanFaied/rtogtor/engine/internal/jsonschemas"
	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// client drives a server over pipes and validates every message it receives.
type client struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Scanner
	done chan error
	srv  *Server
	n    int
}

func start(t *testing.T, configure func(*Server)) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := NewServer(outW, io.Discard)
	s.lookup = func(string) (string, bool) { return "", false }
	if configure != nil {
		configure(s)
	}
	c := &client{t: t, in: inW, out: bufio.NewScanner(outR), done: make(chan error, 1), srv: s}
	c.out.Buffer(make([]byte, 0, 1<<20), MaxMessageSize)
	go func() {
		err := s.Serve(context.Background(), inR)
		_ = outW.Close()
		c.done <- err
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return c
}

type message struct {
	ProtocolVersion string          `json:"protocolVersion"`
	ID              string          `json:"id"`
	Result          json.RawMessage `json:"result"`
	Error           *struct {
		Code        string            `json:"code"`
		Message     string            `json:"message"`
		Path        string            `json:"path"`
		Diagnostics []json.RawMessage `json:"diagnostics"`
	} `json:"error"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

func (c *client) send(id, method string, params any) {
	c.t.Helper()
	b, err := json.Marshal(map[string]any{"protocolVersion": "1.0", "id": id, "method": method, "params": params})
	if err != nil {
		c.t.Fatal(err)
	}
	c.raw(string(b))
}

func (c *client) raw(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.in, line+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) read() message {
	c.t.Helper()
	got := make(chan bool, 1)
	go func() { got <- c.out.Scan() }()
	select {
	case ok := <-got:
		if !ok {
			c.t.Fatalf("server closed the stream: %v", c.out.Err())
		}
	case <-time.After(30 * time.Second):
		c.t.Fatal("timed out waiting for a message")
	}
	line := c.out.Bytes()
	if v, err := jsonschemas.ValidateJSON(spec.ProtocolSchemaFile, line); err != nil || len(v) > 0 {
		c.t.Errorf("engine sent a message that violates protocol.schema.json: %v %v\n%s", err, v, line)
	}
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		c.t.Fatal(err)
	}
	return m
}

func (c *client) call(method string, params any) message {
	c.t.Helper()
	c.n++
	id := fmt.Sprint(c.n)
	c.send(id, method, params)
	for {
		m := c.read()
		if m.ID == id && m.Event == "" {
			return m
		}
	}
}

func (c *client) handshake() {
	c.t.Helper()
	m := c.call("handshake", map[string]any{"client": map[string]string{"name": "test", "version": "0"}, "protocolVersions": []string{"1.0"}})
	if m.Error != nil {
		c.t.Fatalf("handshake: %+v", m.Error)
	}
}

func errCode(m message) string {
	if m.Error == nil {
		return ""
	}
	return m.Error.Code
}

func TestHandshakeIsRequiredAndVersioned(t *testing.T) {
	c := start(t, nil)
	if got := errCode(c.call("canonicalize", map[string]string{"type": "int64", "value": "1"})); got != "RTG-PROTO-006" {
		t.Errorf("before handshake: %s", got)
	}
	m := c.call("handshake", map[string]any{"client": map[string]string{"name": "t", "version": "0"}, "protocolVersions": []string{"2.0"}})
	if errCode(m) != "RTG-PROTO-001" {
		t.Errorf("incompatible client: %+v", m.Error)
	}
	c.handshake()
	c.raw(`{"protocolVersion":"9.1","id":"x","method":"report","params":{"runId":"a"}}`)
	if got := errCode(c.read()); got != "RTG-PROTO-001" {
		t.Errorf("unsupported major: %s", got)
	}
}

func TestMalformedAndUnknownRequests(t *testing.T) {
	c := start(t, nil)
	c.handshake()
	c.raw(`this is not json`) // ignored: no id to answer
	c.raw(`{"protocolVersion":"1.0","id":"u","method":"teleport","params":{}}`)
	if got := errCode(c.read()); got != "RTG-PROTO-003" {
		t.Errorf("unknown method: %s", got)
	}
	c.raw(`{"protocolVersion":"1.0","id":"p","method":"canonicalize","params":{"type":"int64"}}`)
	if got := errCode(c.read()); got != "RTG-PROTO-004" {
		t.Errorf("missing param: %s", got)
	}
	c.raw(`{"protocolVersion":"1.0","id":"e","method":"canonicalize"}`)
	if got := errCode(c.read()); got != "RTG-PROTO-002" {
		t.Errorf("missing params: %s", got)
	}
}

func TestCredentialsNeverInMessages(t *testing.T) {
	c := start(t, nil)
	c.handshake()
	m := c.call("inspect", map[string]any{"source": map[string]string{"product": "postgresql", "uri": "postgres://app:S3cretPw@db/x"}})
	if errCode(m) != "RTG-PROTO-004" || strings.Contains(m.Error.Message, "S3cretPw") {
		t.Errorf("uri with credentials: %+v", m.Error)
	}
	m = c.call("inspect", map[string]any{"source": map[string]string{"product": "postgresql", "urlEnv": "RTG_UNSET_VAR"}})
	if errCode(m) != "RTG-CONN-002" || !strings.Contains(m.Error.Message, "RTG_UNSET_VAR") {
		t.Errorf("missing variable: %+v", m.Error)
	}
}

func TestVectorsThroughProtocol(t *testing.T) {
	c := start(t, nil)
	c.handshake()

	var ids struct {
		Vectors []struct {
			Name, EntityType, SerializationHex, RtgID string
			Key, CanonicalKey                         []map[string]*string
		} `json:"vectors"`
		Invalid []struct {
			Name, EntityType, ErrorCode string
			Key                         []map[string]*string
		} `json:"invalid"`
	}
	loadJSON(t, testutil.RepoPath(t, "conformance", "vectors", "identity.json"), &ids)
	for _, v := range ids.Vectors {
		m := c.call("identity", map[string]any{"entityType": v.EntityType, "key": v.Key})
		var r struct {
			RtgID, SerializationHex string
		}
		if m.Error != nil || json.Unmarshal(m.Result, &r) != nil || r.RtgID != v.RtgID || r.SerializationHex != v.SerializationHex {
			t.Errorf("identity %s: %s %+v", v.Name, m.Result, m.Error)
		}
	}
	for _, v := range ids.Invalid {
		if got := errCode(c.call("identity", map[string]any{"entityType": v.EntityType, "key": v.Key})); got != v.ErrorCode {
			t.Errorf("identity %s: got %s, want %s", v.Name, got, v.ErrorCode)
		}
	}

	var vals struct {
		Valid   []struct{ Type, Input, Canonical string } `json:"valid"`
		Invalid []struct{ Type, Input, ErrorCode string } `json:"invalid"`
	}
	loadJSON(t, testutil.RepoPath(t, "conformance", "vectors", "values.json"), &vals)
	for _, v := range vals.Valid {
		m := c.call("canonicalize", map[string]string{"type": v.Type, "value": v.Input})
		var r struct{ Canonical string }
		if m.Error != nil || json.Unmarshal(m.Result, &r) != nil || r.Canonical != v.Canonical {
			t.Errorf("canonicalize %s %q: %s %+v", v.Type, v.Input, m.Result, m.Error)
		}
	}
	for _, v := range vals.Invalid {
		if got := errCode(c.call("canonicalize", map[string]string{"type": v.Type, "value": v.Input})); got != v.ErrorCode {
			t.Errorf("canonicalize %s %q: got %s", v.Type, v.Input, got)
		}
	}
}

func loadJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

// ecommerceMapping returns the example mapping as a JSON object.
func ecommerceMapping(t *testing.T) json.RawMessage {
	m, issues := mapping.LoadFile(testutil.RepoPath(t, "examples", "ecommerce", "mapping.yaml"))
	if issues.HasErrors() {
		t.Fatal(issues)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPlanConformanceCaseThroughProtocol(t *testing.T) {
	c := start(t, nil)
	c.handshake()
	meta, err := os.ReadFile(testutil.RepoPath(t, "conformance", "cases", "ecommerce", "expected-schema-metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(testutil.RepoPath(t, "conformance", "cases", "ecommerce", "expected-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := c.call("plan", map[string]any{"mapping": ecommerceMapping(t), "sourceMetadata": json.RawMessage(meta)})
	if m.Error != nil {
		t.Fatalf("plan: %+v", m.Error)
	}
	if string(m.Result)+"\n" != string(want) {
		t.Errorf("plan result is not byte-identical to expected-plan.json:\n%s", m.Result)
	}
}

func TestInvalidMappingReportsEveryProblem(t *testing.T) {
	c := start(t, nil)
	c.handshake()
	bad := map[string]any{
		"specVersion": "0.1.0", "name": "bad", "direction": "relational-to-graph",
		"source": map[string]string{"kind": "postgresql"}, "destination": map[string]string{"kind": "neo4j"},
		"entities": []any{
			map[string]any{"name": "A", "table": "a", "labels": []string{"A"}, "key": []string{"id", "x"},
				"properties": []any{map[string]string{"column": "id", "property": "id", "type": "float64"}}},
		},
	}
	m := c.call("validateMapping", map[string]any{"mapping": bad})
	var r struct {
		Valid       bool
		Diagnostics []struct{ Code string }
	}
	if err := json.Unmarshal(m.Result, &r); err != nil || r.Valid || len(r.Diagnostics) != 2 {
		t.Errorf("validateMapping: %s", m.Result)
	}
	meta, err := os.ReadFile(testutil.RepoPath(t, "conformance", "cases", "ecommerce", "expected-schema-metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	m = c.call("plan", map[string]any{"mapping": bad, "sourceMetadata": json.RawMessage(meta)})
	if errCode(m) != "RTG-MAP-006" || len(m.Error.Diagnostics) != 2 || !strings.HasSuffix(m.Error.Message, "(and 1 more problem)") {
		t.Errorf("plan with invalid mapping: %+v", m.Error)
	}
}

func TestCancelAndDuplicateIDs(t *testing.T) {
	started := make(chan struct{}, 1)
	c := start(t, func(s *Server) {
		s.handlers["inspect"] = func(ctx context.Context, _ *Server, _ string, _ json.RawMessage) (any, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
	})
	c.handshake()
	src := map[string]any{"source": map[string]string{"product": "postgresql", "urlEnv": "X"}}
	c.send("slow", "inspect", src)
	<-started
	c.send("slow", "inspect", src)
	if m := c.read(); m.ID != "slow" || errCode(m) != "RTG-PROTO-005" {
		t.Errorf("duplicate id: %+v", m)
	}
	c.send("c1", "cancel", map[string]string{"requestId": "slow"})
	got := map[string]string{}
	for len(got) < 2 {
		m := c.read()
		got[m.ID] = errCode(m)
	}
	if got["slow"] != "RTG-EXEC-004" || got["c1"] != "" {
		t.Errorf("cancel: %v", got)
	}
}

func TestShutdownAndEOFEndTheSession(t *testing.T) {
	c := start(t, nil)
	c.handshake()
	if m := c.call("shutdown", map[string]any{}); m.Error != nil {
		t.Fatal(m.Error)
	}
	select {
	case err := <-c.done:
		if err != nil {
			t.Errorf("serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop after shutdown")
	}

	c2 := start(t, nil)
	_ = c2.in.Close()
	select {
	case <-c2.done:
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop at end of input")
	}
}
