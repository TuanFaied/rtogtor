package ir

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

type vectorComponent struct {
	Type  LogicalType `json:"type"`
	Value *string     `json:"value"`
}

func (c vectorComponent) toKey() KeyComponent {
	if c.Value == nil {
		return KeyComponent{Type: c.Type, Null: true}
	}
	return KeyComponent{Type: c.Type, Value: *c.Value}
}

func loadJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// TestIdentityVectors runs conformance/vectors/identity.json.
func TestIdentityVectors(t *testing.T) {
	var doc struct {
		SpecVersion string `json:"specVersion"`
		Algorithm   string `json:"algorithm"`
		Vectors     []struct {
			Name             string            `json:"name"`
			EntityType       string            `json:"entityType"`
			Key              []vectorComponent `json:"key"`
			CanonicalKey     []vectorComponent `json:"canonicalKey"`
			SerializationHex string            `json:"serializationHex"`
			RtgID            string            `json:"rtgId"`
		} `json:"vectors"`
		Invalid []struct {
			Name       string            `json:"name"`
			EntityType string            `json:"entityType"`
			Key        []vectorComponent `json:"key"`
			ErrorCode  rtgerr.Code       `json:"errorCode"`
		} `json:"invalid"`
	}
	loadJSON(t, testutil.RepoPath(t, "conformance", "vectors", "identity.json"), &doc)

	if doc.Algorithm != spec.IdentityAlgorithm {
		t.Fatalf("vectors are for %s, engine implements %s", doc.Algorithm, spec.IdentityAlgorithm)
	}
	if len(doc.Vectors) == 0 || len(doc.Invalid) == 0 {
		t.Fatal("vector file has no cases")
	}

	for _, v := range doc.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			comps := make([]KeyComponent, len(v.Key))
			for i, c := range v.Key {
				comps[i] = c.toKey()
			}
			k, err := NewKey(v.EntityType, comps)
			if err != nil {
				t.Fatalf("NewKey: %v", err)
			}
			if len(k.Components) != len(v.CanonicalKey) {
				t.Fatalf("got %d canonical components, want %d", len(k.Components), len(v.CanonicalKey))
			}
			for i, want := range v.CanonicalKey {
				if got := k.Components[i]; got.Type != want.Type || got.Value != *want.Value {
					t.Errorf("component %d = (%s, %q), want (%s, %q)", i, got.Type, got.Value, want.Type, *want.Value)
				}
			}
			if got := hex.EncodeToString(k.Serialize()); got != v.SerializationHex {
				t.Errorf("serialization\n got %s\nwant %s", got, v.SerializationHex)
			}
			if got := k.ID(); got != v.RtgID {
				t.Errorf("rtg_id = %s, want %s", got, v.RtgID)
			}
		})
	}

	for _, v := range doc.Invalid {
		t.Run("invalid/"+v.Name, func(t *testing.T) {
			comps := make([]KeyComponent, len(v.Key))
			for i, c := range v.Key {
				comps[i] = c.toKey()
			}
			_, err := NewKey(v.EntityType, comps)
			if !rtgerr.IsCode(err, v.ErrorCode) {
				t.Errorf("got error %v, want code %s", err, v.ErrorCode)
			}
		})
	}
}

// TestValueVectors runs conformance/vectors/values.json.
func TestValueVectors(t *testing.T) {
	var doc struct {
		Valid []struct {
			Type      LogicalType `json:"type"`
			Input     string      `json:"input"`
			Canonical string      `json:"canonical"`
		} `json:"valid"`
		Invalid []struct {
			Type      LogicalType `json:"type"`
			Input     string      `json:"input"`
			ErrorCode rtgerr.Code `json:"errorCode"`
		} `json:"invalid"`
	}
	loadJSON(t, testutil.RepoPath(t, "conformance", "vectors", "values.json"), &doc)
	if len(doc.Valid) == 0 || len(doc.Invalid) == 0 {
		t.Fatal("vector file has no cases")
	}

	for _, v := range doc.Valid {
		got, err := Canonicalize(v.Type, v.Input)
		if err != nil {
			t.Errorf("Canonicalize(%s, %q): unexpected error %v", v.Type, v.Input, err)
			continue
		}
		if got != v.Canonical {
			t.Errorf("Canonicalize(%s, %q) = %q, want %q", v.Type, v.Input, got, v.Canonical)
		}
		// Canonical forms are fixed points.
		if again, err := Canonicalize(v.Type, got); err != nil || again != got {
			t.Errorf("canonical form %q of %s is not stable: %q, %v", got, v.Type, again, err)
		}
	}
	for _, v := range doc.Invalid {
		_, err := Canonicalize(v.Type, v.Input)
		if !rtgerr.IsCode(err, v.ErrorCode) {
			t.Errorf("Canonicalize(%s, %q): got %v, want code %s", v.Type, v.Input, err, v.ErrorCode)
		}
	}
}
