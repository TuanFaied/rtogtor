package protocol

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/url"

	"github.com/santhosh-tekuri/jsonschema/v6"

	rtogtor "github.com/TuanFaied/rtogtor/engine"
	"github.com/TuanFaied/rtogtor/engine/internal/jsonschemas"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/mapping"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

func defaultHandlers() map[string]handler {
	// handshake, cancel and shutdown are handled by the server itself.
	return map[string]handler{
		"handshake": nil, "cancel": nil, "shutdown": nil,
		"validateMapping": validateMapping,
		"inspect":         inspect,
		"plan":            plan,
		"migrate":         migrate,
		"verify":          verify,
		"report":          report,
		"runState":        runState,
		"failures":        failures,
		"identity":        identity,
		"canonicalize":    canonicalize,
	}
}

func decode(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return rtgerr.New(rtgerr.ProtoInvalidParams, "%v", err)
	}
	return nil
}

// connection names environment variables, never credentials.
type connection struct {
	Product     string `json:"product"`
	URLEnv      string `json:"urlEnv"`
	URI         string `json:"uri"`
	UserEnv     string `json:"userEnv"`
	PasswordEnv string `json:"passwordEnv"`
	Database    string `json:"database"`
}

func (s *Server) env(name string) (string, error) {
	v, ok := s.lookup(name)
	if !ok || v == "" {
		return "", rtgerr.New(rtgerr.ConnConfig, "environment variable %s is not set or is empty in the engine process", name)
	}
	return v, nil
}

// resolve builds a connection from environment variables. The resulting
// URL lives only in memory.
func (s *Server) resolve(c *connection) (rtogtor.Connection, error) {
	if c == nil {
		return rtogtor.Connection{}, nil
	}
	raw := ""
	if c.URLEnv != "" {
		v, err := s.env(c.URLEnv)
		if err != nil {
			return rtogtor.Connection{}, err
		}
		raw = v
	} else {
		u, err := url.Parse(c.URI)
		if err != nil {
			return rtogtor.Connection{}, rtgerr.New(rtgerr.ProtoInvalidParams, "invalid uri")
		}
		if u.User != nil {
			return rtogtor.Connection{}, rtgerr.New(rtgerr.ProtoInvalidParams,
				"uri must not contain credentials; pass them through userEnv and passwordEnv")
		}
		if c.UserEnv != "" || c.PasswordEnv != "" {
			var user, pw string
			if c.UserEnv != "" {
				if user, err = s.env(c.UserEnv); err != nil {
					return rtogtor.Connection{}, err
				}
			}
			if c.PasswordEnv != "" {
				if pw, err = s.env(c.PasswordEnv); err != nil {
					return rtogtor.Connection{}, err
				}
			}
			u.User = url.UserPassword(user, pw)
		}
		raw = u.String()
	}
	var conn rtogtor.Connection
	switch c.Product {
	case rtogtor.ProductPostgreSQL:
		conn = rtogtor.PostgreSQL(raw)
	case rtogtor.ProductNeo4j:
		conn = rtogtor.Neo4j(raw, rtogtor.NoAuth())
	}
	if c.Database != "" {
		conn = conn.WithDatabase(c.Database)
	}
	return conn, nil
}

func decodeMapping(raw json.RawMessage) (*mapping.Mapping, error) {
	m, issues := mapping.Decode(raw, mapping.FormatJSON)
	if issues.HasErrors() {
		return nil, issues
	}
	return m, nil
}

// ---- methods -----------------------------------------------------------------

type diagnosticOut struct {
	Code      rtgerr.Code     `json:"code"`
	Severity  rtgerr.Severity `json:"severity"`
	Retryable bool            `json:"retryable"`
	Message   string          `json:"message"`
	Path      string          `json:"path,omitempty"`
}

func validateMapping(_ context.Context, _ *Server, _ string, raw json.RawMessage) (any, error) {
	var p struct {
		Mapping json.RawMessage `json:"mapping"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	m, issues := mapping.Decode(p.Mapping, mapping.FormatJSON)
	out := struct {
		Valid       bool            `json:"valid"`
		MappingHash string          `json:"mappingHash,omitempty"`
		Diagnostics []diagnosticOut `json:"diagnostics"`
	}{Valid: !issues.HasErrors(), Diagnostics: []diagnosticOut{}}
	for _, e := range issues {
		out.Diagnostics = append(out.Diagnostics, diagnosticOut{e.Code, e.Severity(), e.Retryable(), e.Message, e.Path})
	}
	if m != nil {
		h, err := m.Hash()
		if err != nil {
			return nil, rtgerr.New(rtgerr.Internal, "hashing the mapping: %v", err)
		}
		out.MappingHash = h
	}
	return out, nil
}

func inspect(ctx context.Context, s *Server, _ string, raw json.RawMessage) (any, error) {
	var p struct {
		Source  *connection `json:"source"`
		Schemas []string    `json:"schemas"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	src, err := s.resolve(p.Source)
	if err != nil {
		return nil, err
	}
	return rtogtor.Inspect(ctx, src, p.Schemas...)
}

func plan(ctx context.Context, s *Server, _ string, raw json.RawMessage) (any, error) {
	var p struct {
		Mapping             json.RawMessage `json:"mapping"`
		Source              *connection     `json:"source"`
		SourceMetadata      json.RawMessage `json:"sourceMetadata"`
		Destination         *connection     `json:"destination"`
		DestinationMetadata json.RawMessage `json:"destinationMetadata"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	m, err := decodeMapping(p.Mapping)
	if err != nil {
		return nil, err
	}
	reverse := m.Direction == mapping.GraphToRelational
	if p.SourceMetadata != nil {
		meta, err := decodeMetadata("sourceMetadata", p.SourceMetadata)
		if err != nil {
			return nil, err
		}
		var dst *rtogtor.SchemaMetadata
		if reverse {
			if p.DestinationMetadata == nil {
				return nil, rtgerr.New(rtgerr.ProtoInvalidParams, "a graph-to-relational plan from sourceMetadata also needs destinationMetadata")
			}
			if dst, err = decodeMetadata("destinationMetadata", p.DestinationMetadata); err != nil {
				return nil, err
			}
		}
		return rtogtor.PlanFromMetadata(m, meta, dst)
	}
	src, err := s.resolve(p.Source)
	if err != nil {
		return nil, err
	}
	var dst rtogtor.Connection
	if reverse {
		if p.Destination == nil {
			return nil, rtgerr.New(rtgerr.ProtoInvalidParams, "a graph-to-relational plan needs destination, to inspect its tables")
		}
		if dst, err = s.resolve(p.Destination); err != nil {
			return nil, err
		}
	}
	mig := rtogtor.NewMigration(src, dst, m)
	defer func() { _ = mig.Close() }()
	return mig.Plan(ctx)
}

// decodeMetadata validates and decodes a schema-metadata parameter.
func decodeMetadata(name string, raw json.RawMessage) (*rtogtor.SchemaMetadata, error) {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, rtgerr.New(rtgerr.ProtoInvalidParams, "%s: %v", name, err)
	}
	if v, err := jsonschemas.Validate(spec.SchemaMetadataSchemaFile, inst); err != nil || len(v) > 0 {
		msg := "does not match schema-metadata.schema.json"
		if len(v) > 0 {
			msg = v[0].Path + ": " + v[0].Message
		}
		return nil, rtgerr.New(rtgerr.ProtoInvalidParams, "%s %s", name, msg)
	}
	var meta rtogtor.SchemaMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, rtgerr.New(rtgerr.ProtoInvalidParams, "%s: %v", name, err)
	}
	return &meta, nil
}

func migrate(ctx context.Context, s *Server, id string, raw json.RawMessage) (any, error) {
	var p struct {
		Mapping     json.RawMessage `json:"mapping"`
		Source      *connection     `json:"source"`
		Destination *connection     `json:"destination"`
		DryRun      bool            `json:"dryRun"`
		BatchSize   int             `json:"batchSize"`
		ResumeRunID string          `json:"resumeRunId"`
		StateDir    string          `json:"stateDir"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	m, err := decodeMapping(p.Mapping)
	if err != nil {
		return nil, err
	}
	src, err := s.resolve(p.Source)
	if err != nil {
		return nil, err
	}
	dst, err := s.resolve(p.Destination)
	if err != nil {
		return nil, err
	}
	mig := rtogtor.NewMigration(src, dst, m)
	defer func() { _ = mig.Close() }()
	rep, err := mig.Run(ctx, rtogtor.RunOptions{
		DryRun: p.DryRun, BatchSize: p.BatchSize, ResumeRunID: p.ResumeRunID, StateDir: p.StateDir,
		Progress: func(pr rtogtor.Progress) {
			s.emit(id, "progress", struct {
				StepID  string `json:"stepId"`
				Batch   int64  `json:"batch"`
				Read    int64  `json:"read"`
				Written int64  `json:"written"`
				Failed  int64  `json:"failed"`
				Done    bool   `json:"done"`
			}{pr.StepID, pr.Batch, pr.Read, pr.Written, pr.Failed, pr.Done})
		},
	})
	if err != nil {
		if rep != nil {
			return nil, &reportError{err: err, report: rep}
		}
		return nil, err
	}
	return rep, nil
}

type runParams struct {
	RunID    string `json:"runId"`
	StateDir string `json:"stateDir"`
}

func (p runParams) dir() string {
	if p.StateDir == "" {
		return rtogtor.DefaultStateDir
	}
	return p.StateDir
}

func verify(ctx context.Context, s *Server, _ string, raw json.RawMessage) (any, error) {
	var p struct {
		runParams
		Source      *connection `json:"source"`
		Destination *connection `json:"destination"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	src, err := s.resolve(p.Source)
	if err != nil {
		return nil, err
	}
	dst, err := s.resolve(p.Destination)
	if err != nil {
		return nil, err
	}
	mig := rtogtor.NewMigration(src, dst, nil)
	defer func() { _ = mig.Close() }()
	return mig.Verify(ctx, p.RunID, p.dir())
}

func report(_ context.Context, _ *Server, _ string, raw json.RawMessage) (any, error) {
	var p runParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	return rtogtor.LoadReport(p.dir(), p.RunID)
}

func runState(_ context.Context, _ *Server, _ string, raw json.RawMessage) (any, error) {
	var p runParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	return rtogtor.LoadRunState(p.dir(), p.RunID)
}

func failures(_ context.Context, _ *Server, _ string, raw json.RawMessage) (any, error) {
	var p runParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	f, err := rtogtor.LoadFailures(p.dir(), p.RunID)
	if err != nil {
		return nil, err
	}
	return struct {
		Failures []rtogtor.Failure `json:"failures"`
	}{f}, nil
}

type keyComponent struct {
	Type  ir.LogicalType `json:"type"`
	Value *string        `json:"value"`
}

func identity(_ context.Context, _ *Server, _ string, raw json.RawMessage) (any, error) {
	var p struct {
		EntityType string         `json:"entityType"`
		Key        []keyComponent `json:"key"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	comps := make([]ir.KeyComponent, len(p.Key))
	for i, k := range p.Key {
		comps[i] = ir.KeyComponent{Type: k.Type, Null: k.Value == nil}
		if k.Value != nil {
			comps[i].Value = *k.Value
		}
	}
	key, err := ir.NewKey(p.EntityType, comps)
	if err != nil {
		return nil, err
	}
	canonical := make([]keyComponent, len(key.Components))
	for i, c := range key.Components {
		v := c.Value
		canonical[i] = keyComponent{Type: c.Type, Value: &v}
	}
	return struct {
		RtgID            string         `json:"rtgId"`
		CanonicalKey     []keyComponent `json:"canonicalKey"`
		SerializationHex string         `json:"serializationHex"`
	}{key.ID(), canonical, hex.EncodeToString(key.Serialize())}, nil
}

func canonicalize(_ context.Context, _ *Server, _ string, raw json.RawMessage) (any, error) {
	var p struct {
		Type  ir.LogicalType `json:"type"`
		Value string         `json:"value"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	c, err := ir.Canonicalize(p.Type, p.Value)
	if err != nil {
		return nil, err
	}
	return struct {
		Canonical string `json:"canonical"`
	}{c}, nil
}
