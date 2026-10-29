// Package runstate persists migration runs (specs/run-state.schema.json) in
// a local state directory, so any run can be inspected and resumed.
//
// Layout:
//
//	<dir>/runs/<run-id>/state.json      run state and per-step checkpoints
//	<dir>/runs/<run-id>/mapping.json    the mapping, as canonical JSON
//	<dir>/runs/<run-id>/plan.json       the plan, as canonical JSON
//	<dir>/runs/<run-id>/failures.jsonl  failed records, one JSON object per line
//	<dir>/runs/<run-id>/report.json     the final report
//
// None of these files ever contain credentials.
package runstate

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/TuanFaied/rtogtor/engine/internal/jsonschemas"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// Status of a run.
type Status string

// Statuses.
const (
	StatusRunning               Status = "running"
	StatusSucceeded             Status = "succeeded"
	StatusSucceededWithFailures Status = "succeeded-with-failures"
	StatusInterrupted           Status = "interrupted"
	StatusFailed                Status = "failed"
)

// Endpoint describes one database of a run. It never holds credentials or
// host names.
type Endpoint struct {
	Product  string `json:"product"`
	Version  string `json:"version,omitempty"`
	Database string `json:"database,omitempty"`
}

// MappingRef identifies the mapping of a run.
type MappingRef struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// ErrorInfo is a recorded error.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Stream is the progress of one load step.
type Stream struct {
	StepID string `json:"stepId"`
	Kind   string `json:"kind"`
	Done   bool   `json:"done"`
	// Cursor holds the canonical text of the last committed row's key, in
	// key order. Nil means the step has not committed a batch yet.
	Cursor []string `json:"cursor"`
	// Batches counts committed batches; it is also the number of the next
	// batch.
	Batches int64 `json:"batches"`
	Read    int64 `json:"read"`
	Written int64 `json:"written"`
	Failed  int64 `json:"failed"`
	// Skipped counts rows that produce no write by design, such as a NULL
	// foreign key.
	Skipped int64 `json:"skipped"`
	// NullKeys counts rows whose key contains NULL; they cannot be read in
	// key order or given an identity.
	NullKeys int64 `json:"nullKeys"`
}

// State is a run's persisted state.
type State struct {
	SpecVersion string     `json:"specVersion"`
	RunID       string     `json:"runId"`
	Mapping     MappingRef `json:"mapping"`
	DryRun      bool       `json:"dryRun"`
	Status      Status     `json:"status"`
	BatchSize   int        `json:"batchSize"`
	Source      Endpoint   `json:"source"`
	Destination Endpoint   `json:"destination"`
	Streams     []Stream   `json:"streams"`
	// Attempts counts how many times the run was started or resumed.
	Attempts    int        `json:"attempts"`
	StartedAt   string     `json:"startedAt"`
	UpdatedAt   string     `json:"updatedAt"`
	CompletedAt string     `json:"completedAt,omitempty"`
	LastError   *ErrorInfo `json:"lastError,omitempty"`
}

// Stream returns the stream for a step.
func (s *State) Stream(stepID string) *Stream {
	for i := range s.Streams {
		if s.Streams[i].StepID == stepID {
			return &s.Streams[i]
		}
	}
	return nil
}

// KeyValue is one key column of a failed record.
type KeyValue struct {
	Column string `json:"column"`
	Type   string `json:"type"`
	Value  string `json:"value"`
}

// Failure is one failed record.
type Failure struct {
	StepID string `json:"stepId"`
	// Batch is the number of the batch the record belonged to.
	Batch int64 `json:"batch"`
	// Key identifies the source row. It is empty for aggregate failures
	// such as rows with NULL keys.
	Key     []KeyValue `json:"key"`
	Code    string     `json:"code"`
	Message string     `json:"message"`
	At      string     `json:"at"`
}

// Now returns the current time in the format used by run state.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// NewRunID returns a UUIDv7: time-ordered, so runs sort by start time.
func NewRunID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(time.Now().UnixMilli())) //nolint:gosec // Unix time is positive
	copy(b[:6], ts[2:])                                               // 48-bit millisecond timestamp
	b[6] = 0x70 | (b[6] & 0x0f)                                       // version 7
	b[8] = 0x80 | (b[8] & 0x3f)                                       // RFC 9562 variant
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

var runIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Store is a state directory.
type Store struct {
	Dir string
}

// DefaultDir is the default state directory, relative to the working
// directory.
const DefaultDir = ".rtogtor"

func (s *Store) runDir(runID string) (string, error) {
	// Validating the ID also prevents path traversal.
	if !runIDPattern.MatchString(runID) {
		return "", rtgerr.New(rtgerr.RunNotFound, "%q is not a valid run ID", runID)
	}
	return filepath.Join(s.Dir, "runs", runID), nil
}

// Create creates a run directory and writes its initial files.
func (s *Store) Create(st *State, mappingJSON, planJSON []byte) error {
	dir, err := s.runDir(st.RunID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return rtgerr.New(rtgerr.Internal, "creating the run directory: %v", err)
	}
	if err := writeAtomic(filepath.Join(dir, "mapping.json"), mappingJSON); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "plan.json"), planJSON); err != nil {
		return err
	}
	return s.Save(st)
}

// Save writes the state atomically: a crash leaves either the old or the new
// state, never a partial file.
func (s *Store) Save(st *State) error {
	dir, err := s.runDir(st.RunID)
	if err != nil {
		return err
	}
	st.UpdatedAt = Now()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return rtgerr.New(rtgerr.Internal, "encoding run state: %v", err)
	}
	return writeAtomic(filepath.Join(dir, "state.json"), append(b, '\n'))
}

// Load reads a run's state.
func (s *Store) Load(runID string) (*State, error) {
	b, err := s.ReadFile(runID, "state.json")
	if err != nil {
		return nil, err
	}
	if v, err := jsonschemas.ValidateJSON(spec.RunStateSchemaFile, b); err != nil || len(v) > 0 {
		return nil, rtgerr.New(rtgerr.RunStateCorrupt, "state of run %s does not match the run-state schema", runID)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, rtgerr.New(rtgerr.RunStateCorrupt, "state of run %s is unreadable: %v", runID, err)
	}
	if !spec.Supports(st.SpecVersion) {
		return nil, rtgerr.New(rtgerr.RunStateCorrupt, "run %s was written for specVersion %s", runID, st.SpecVersion)
	}
	return &st, nil
}

// ReadFile reads one file of a run.
func (s *Store) ReadFile(runID, name string) ([]byte, error) {
	dir, err := s.runDir(runID)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		if _, statErr := os.Stat(dir); errors.Is(statErr, fs.ErrNotExist) {
			return nil, rtgerr.New(rtgerr.RunNotFound, "run %s not found in %s", runID, s.Dir)
		}
		return nil, rtgerr.New(rtgerr.RunStateCorrupt, "run %s has no %s", runID, name)
	}
	if err != nil {
		return nil, rtgerr.New(rtgerr.RunStateCorrupt, "reading %s of run %s: %v", name, runID, err)
	}
	return b, nil
}

// WriteFile writes one file of a run atomically.
func (s *Store) WriteFile(runID, name string, data []byte) error {
	dir, err := s.runDir(runID)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, name), data)
}

// AppendFailures appends failed records and syncs them to disk.
func (s *Store) AppendFailures(runID string, failures []Failure) error {
	if len(failures) == 0 {
		return nil
	}
	dir, err := s.runDir(runID)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, f := range failures {
		b, err := json.Marshal(f)
		if err != nil {
			return rtgerr.New(rtgerr.Internal, "encoding a failure: %v", err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(filepath.Join(dir, "failures.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return rtgerr.New(rtgerr.Internal, "opening the failures file: %v", err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		_ = f.Close()
		return rtgerr.New(rtgerr.Internal, "writing failures: %v", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return rtgerr.New(rtgerr.Internal, "syncing failures: %v", err)
	}
	return f.Close()
}

// Failures returns the failed records of committed batches. Records from a
// batch that was replayed after a crash appear once; records written for a
// batch that never committed are ignored.
func (s *Store) Failures(st *State) ([]Failure, error) {
	dir, err := s.runDir(st.RunID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, "failures.jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return []Failure{}, nil
	}
	if err != nil {
		return nil, rtgerr.New(rtgerr.RunStateCorrupt, "reading failures: %v", err)
	}
	defer func() { _ = f.Close() }()

	committed := map[string]int64{}
	for _, str := range st.Streams {
		committed[str.StepID] = str.Batches
	}
	seen := map[string]bool{}
	out := []Failure{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var fl Failure
		if err := json.Unmarshal(line, &fl); err != nil {
			// A torn final line from a crash mid-write is ignored: its
			// batch was not checkpointed, so it will be replayed.
			continue
		}
		if fl.Batch >= committed[fl.StepID] && fl.Batch >= 0 {
			continue
		}
		keyJSON, _ := json.Marshal(fl.Key)
		id := fmt.Sprintf("%s|%d|%s|%s", fl.StepID, fl.Batch, fl.Code, keyJSON)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, fl)
	}
	if err := sc.Err(); err != nil {
		return nil, rtgerr.New(rtgerr.RunStateCorrupt, "reading failures: %v", err)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StepID != out[j].StepID {
			return stepIndex(st, out[i].StepID) < stepIndex(st, out[j].StepID)
		}
		return out[i].Batch < out[j].Batch
	})
	return out, nil
}

func stepIndex(st *State, id string) int {
	for i, s := range st.Streams {
		if s.StepID == id {
			return i
		}
	}
	return len(st.Streams)
}

// AggregateBatch marks failures that are not tied to a batch (such as a count
// of rows with NULL keys). They are recorded before a step's first batch.
const AggregateBatch int64 = -1

// writeAtomic writes data to a temporary file and renames it over path.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return rtgerr.New(rtgerr.Internal, "writing %s: %v", filepath.Base(path), err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return rtgerr.New(rtgerr.Internal, "writing %s: %v", filepath.Base(path), err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return rtgerr.New(rtgerr.Internal, "syncing %s: %v", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return rtgerr.New(rtgerr.Internal, "writing %s: %v", filepath.Base(path), err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return rtgerr.New(rtgerr.Internal, "replacing %s: %v", filepath.Base(path), err)
	}
	return nil
}
