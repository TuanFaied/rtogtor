// Package protocol implements the engine side of the engine ↔ SDK protocol
// (specs/protocol.md): JSON Lines over stdin/stdout, served by
// `rtogtor serve --stdio`.
//
// Every result is written as RFC 8785 canonical JSON, so SDKs can compare
// results byte for byte.
package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	rtogtor "github.com/TuanFaied/rtogtor/engine"
	"github.com/TuanFaied/rtogtor/engine/internal/canonjson"
	"github.com/TuanFaied/rtogtor/engine/internal/jsonschemas"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// MaxMessageSize bounds one protocol line.
const MaxMessageSize = 64 << 20

// handler serves one method. It returns a result to be encoded as canonical
// JSON, or an error.
type handler func(ctx context.Context, s *Server, id string, params json.RawMessage) (any, error)

// Server serves one protocol session.
type Server struct {
	out    io.Writer
	logw   io.Writer
	lookup func(string) (string, bool)

	writeMu sync.Mutex

	mu         sync.Mutex
	handshaken bool
	inflight   map[string]context.CancelFunc

	wg       sync.WaitGroup
	handlers map[string]handler
}

// NewServer creates a server writing to out. Diagnostics go to logw.
func NewServer(out, logw io.Writer) *Server {
	return &Server{
		out: out, logw: logw, lookup: os.LookupEnv,
		inflight: map[string]context.CancelFunc{},
		handlers: defaultHandlers(),
	}
}

// Serve reads requests from in until shutdown, end of input, or ctx is
// cancelled. In-flight requests are cancelled and awaited before it returns.
func (s *Server) Serve(ctx context.Context, in io.Reader) error {
	ctx, cancelAll := context.WithCancel(ctx)
	defer func() {
		cancelAll()
		s.wg.Wait()
	}()

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 0, 64*1024), MaxMessageSize)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		readErr <- sc.Err()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			if errors.Is(err, bufio.ErrTooLong) {
				s.logf("message larger than %d bytes; closing the session", MaxMessageSize)
			}
			return err
		case line := <-lines:
			if done := s.handleLine(ctx, line); done {
				return nil
			}
		}
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.logw != nil {
		fmt.Fprintf(s.logw, "rtogtor serve: "+format+"\n", args...)
	}
}

// envelope is the common shape of a request.
type envelope struct {
	ProtocolVersion string          `json:"protocolVersion"`
	ID              string          `json:"id"`
	Method          string          `json:"method"`
	Params          json.RawMessage `json:"params"`
}

// handleLine processes one line and reports whether the session ended.
func (s *Server) handleLine(ctx context.Context, line []byte) bool {
	if len(bytes.TrimSpace(line)) == 0 {
		return false
	}
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		s.logf("ignoring a line that is not a JSON object: %v", err)
		return false
	}
	if env.ID == "" {
		s.logf("ignoring a message without an id")
		return false
	}
	if major, ok := majorOf(env.ProtocolVersion); !ok || major != majorOf1(spec.ProtocolVersion) {
		s.respondError(env.ID, rtgerr.New(rtgerr.ProtoUnsupportedMajor,
			"protocol version %q is not supported; this engine speaks %s", env.ProtocolVersion, spec.ProtocolVersion))
		return false
	}
	h, known := s.handlers[env.Method]
	if !known {
		s.respondError(env.ID, rtgerr.New(rtgerr.ProtoUnknownMethod, "unknown method %q", env.Method))
		return false
	}
	if err := validateRequest(line); err != nil {
		s.respondError(env.ID, err)
		return false
	}

	switch env.Method {
	case "handshake":
		result, err := s.handshake(env.Params)
		s.respond(env.ID, result, err)
		return false
	case "shutdown":
		s.respondResult(env.ID, struct{}{})
		return true
	}

	s.mu.Lock()
	handshaken := s.handshaken
	s.mu.Unlock()
	if !handshaken {
		s.respondError(env.ID, rtgerr.New(rtgerr.ProtoHandshake, "send handshake before %s", env.Method))
		return false
	}
	if env.Method == "cancel" {
		result, err := s.cancel(env.Params)
		s.respond(env.ID, result, err)
		return false
	}

	reqCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if _, dup := s.inflight[env.ID]; dup {
		s.mu.Unlock()
		cancel()
		s.respondError(env.ID, rtgerr.New(rtgerr.ProtoDuplicateID, "request %q is already in flight", env.ID))
		return false
	}
	s.inflight[env.ID] = cancel
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.inflight, env.ID)
			s.mu.Unlock()
			cancel()
		}()
		defer func() {
			if r := recover(); r != nil {
				s.respondError(env.ID, rtgerr.New(rtgerr.Internal, "engine panic: %v", r))
			}
		}()
		result, err := h(reqCtx, s, env.ID, env.Params)
		if err != nil {
			s.respondError(env.ID, err)
			return
		}
		s.respondResult(env.ID, result)
	}()
	return false
}

func majorOf(v string) (int, bool) {
	major, minor, ok := strings.Cut(v, ".")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(major)
	if _, err2 := strconv.Atoi(minor); err != nil || err2 != nil {
		return 0, false
	}
	return n, true
}

func majorOf1(v string) int {
	n, _ := majorOf(v)
	return n
}

// validateRequest checks a request against the protocol schema.
func validateRequest(line []byte) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(line))
	if err != nil {
		return rtgerr.New(rtgerr.ProtoMalformed, "invalid JSON: %v", err)
	}
	violations, err := jsonschemas.Validate(spec.ProtocolSchemaFile+"#/$defs/request", inst)
	if err != nil {
		return rtgerr.New(rtgerr.Internal, "protocol schema: %v", err)
	}
	if len(violations) == 0 {
		return nil
	}
	v := violations[0]
	code := rtgerr.ProtoMalformed
	if strings.HasPrefix(v.Path, "/params") {
		code = rtgerr.ProtoInvalidParams
	}
	return rtgerr.New(code, "%s", v.Message).At(v.Path)
}

// ---- output ------------------------------------------------------------------

type response struct {
	ProtocolVersion string          `json:"protocolVersion"`
	ID              string          `json:"id"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           *wireError      `json:"error,omitempty"`
}

type event struct {
	ProtocolVersion string          `json:"protocolVersion"`
	ID              string          `json:"id"`
	Event           string          `json:"event"`
	Data            json.RawMessage `json:"data"`
}

type wireDiagnostic struct {
	Code      rtgerr.Code     `json:"code"`
	Severity  rtgerr.Severity `json:"severity"`
	Retryable bool            `json:"retryable"`
	Message   string          `json:"message"`
	Path      string          `json:"path,omitempty"`
}

type wireError struct {
	wireDiagnostic
	Diagnostics []wireDiagnostic `json:"diagnostics,omitempty"`
	Report      json.RawMessage  `json:"report,omitempty"`
}

func (s *Server) write(v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep embedded canonical JSON byte-identical
	if err := enc.Encode(v); err != nil {
		s.logf("encoding a message failed: %v", err)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.out.Write(buf.Bytes()); err != nil {
		s.logf("writing a message failed: %v", err)
	}
}

func (s *Server) respond(id string, result any, err error) {
	if err != nil {
		s.respondError(id, err)
		return
	}
	s.respondResult(id, result)
}

func (s *Server) respondResult(id string, result any) {
	b, err := canonjson.Marshal(result)
	if err != nil {
		s.respondError(id, rtgerr.New(rtgerr.Internal, "encoding the result: %v", err))
		return
	}
	s.write(response{ProtocolVersion: spec.ProtocolVersion, ID: id, Result: b})
}

func (s *Server) emit(id, name string, data any) {
	b, err := canonjson.Marshal(data)
	if err != nil {
		s.logf("encoding a %s event failed: %v", name, err)
		return
	}
	s.write(event{ProtocolVersion: spec.ProtocolVersion, ID: id, Event: name, Data: b})
}

func diag(e *rtgerr.Error) wireDiagnostic {
	return wireDiagnostic{Code: e.Code, Severity: e.Severity(), Retryable: e.Retryable(), Message: e.Message, Path: e.Path}
}

// toWire converts any error into a protocol error.
func toWire(err error) *wireError {
	var list rtgerr.List
	if errors.As(err, &list) && len(list) > 0 {
		first := list[0]
		for _, e := range list {
			if s := e.Severity(); s == rtgerr.SeverityError || s == rtgerr.SeverityFatal {
				first = e
				break
			}
		}
		w := &wireError{wireDiagnostic: diag(first)}
		for _, e := range list {
			w.Diagnostics = append(w.Diagnostics, diag(e))
		}
		switch n := len(list) - 1; {
		case n == 1:
			w.Message = first.Message + " (and 1 more problem)"
		case n > 1:
			w.Message = fmt.Sprintf("%s (and %d more problems)", first.Message, n)
		}
		return w
	}
	var e *rtgerr.Error
	if errors.As(err, &e) {
		return &wireError{wireDiagnostic: diag(e)}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &wireError{wireDiagnostic: diag(rtgerr.New(rtgerr.ExecInterrupted, "the request was cancelled"))}
	}
	return &wireError{wireDiagnostic: diag(rtgerr.New(rtgerr.Internal, "%v", err))}
}

// reportError carries a report with an error (failed or interrupted migrate).
type reportError struct {
	err    error
	report any
}

func (r *reportError) Error() string { return r.err.Error() }
func (r *reportError) Unwrap() error { return r.err }

func (s *Server) respondError(id string, err error) {
	w := toWire(err)
	var re *reportError
	if errors.As(err, &re) && re.report != nil {
		if b, mErr := canonjson.Marshal(re.report); mErr == nil {
			w.Report = b
		}
	}
	s.write(response{ProtocolVersion: spec.ProtocolVersion, ID: id, Error: w})
}

// ---- built-in methods ----------------------------------------------------------

type handshakeParams struct {
	Client struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"client"`
	ProtocolVersions []string `json:"protocolVersions"`
}

type handshakeResult struct {
	EngineVersion     string `json:"engineVersion"`
	SpecVersion       string `json:"specVersion"`
	IdentityAlgorithm string `json:"identityAlgorithm"`
	ProtocolVersion   string `json:"protocolVersion"`
}

func (s *Server) handshake(raw json.RawMessage) (any, error) {
	var p handshakeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rtgerr.New(rtgerr.ProtoInvalidParams, "handshake: %v", err)
	}
	ok := false
	for _, v := range p.ProtocolVersions {
		if major, valid := majorOf(v); valid && major == majorOf1(spec.ProtocolVersion) {
			ok = true
		}
	}
	if !ok {
		return nil, rtgerr.New(rtgerr.ProtoUnsupportedMajor, "client speaks %v; this engine speaks %s", p.ProtocolVersions, spec.ProtocolVersion)
	}
	s.mu.Lock()
	s.handshaken = true
	s.mu.Unlock()
	return handshakeResult{
		EngineVersion: rtogtor.EngineVersion, SpecVersion: rtogtor.SpecVersion,
		IdentityAlgorithm: rtogtor.IdentityAlgorithm, ProtocolVersion: spec.ProtocolVersion,
	}, nil
}

func (s *Server) cancel(raw json.RawMessage) (any, error) {
	var p struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, rtgerr.New(rtgerr.ProtoInvalidParams, "cancel: %v", err)
	}
	s.mu.Lock()
	if c, ok := s.inflight[p.RequestID]; ok {
		c()
	}
	s.mu.Unlock()
	return struct{}{}, nil
}
