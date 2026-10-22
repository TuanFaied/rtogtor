package ir

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"unicode/utf8"

	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

// identityDomain is the rtg-id/v1 domain separator (specs/identity.md §3).
const identityDomain = "rtg-id/v1\x00"

// KeyComponent is one field of an identity key.
type KeyComponent struct {
	Type  LogicalType
	Value string // text form; ignored when Null is true
	Null  bool
}

// Key is a validated identity key whose components are in canonical form.
type Key struct {
	EntityType string
	Components []KeyComponent
}

// NewKey validates the inputs and canonicalizes every component, following
// specs/identity.md §1.
func NewKey(entityType string, components []KeyComponent) (Key, error) {
	if entityType == "" || !utf8.ValidString(entityType) {
		return Key{}, rtgerr.New(rtgerr.IDEmptyInput, "entity type must be a non-empty UTF-8 string")
	}
	if len(components) == 0 {
		return Key{}, rtgerr.New(rtgerr.IDEmptyInput, "identity key for %q has no components", entityType)
	}
	canonical := make([]KeyComponent, len(components))
	for i, c := range components {
		if !c.Type.Known() {
			return Key{}, rtgerr.New(rtgerr.ValUnsupportedType, "key component %d of %q has unknown logical type %q", i, entityType, string(c.Type))
		}
		if !c.Type.IdentityPermitted() {
			return Key{}, rtgerr.New(rtgerr.IDTypeNotPermitted, "key component %d of %q has type %s, which is not permitted in %s", i, entityType, c.Type, "rtg-id/v1")
		}
		if c.Null {
			return Key{}, rtgerr.New(rtgerr.IDNullComponent, "key component %d of %q is null", i, entityType)
		}
		v, err := Canonicalize(c.Type, c.Value)
		if err != nil {
			return Key{}, err
		}
		if c.Type == TypeDecimal && (v == "NaN" || v == "Infinity" || v == "-Infinity") {
			return Key{}, rtgerr.New(rtgerr.ValInvalid, "key component %d of %q: decimal %s cannot be an identity component", i, entityType, v)
		}
		canonical[i] = KeyComponent{Type: c.Type, Value: v}
	}
	return Key{EntityType: entityType, Components: canonical}, nil
}

// Serialize returns the rtg-id/v1 serialization of k (specs/identity.md §3).
func (k Key) Serialize() []byte {
	size := len(identityDomain) + 4 + len(k.EntityType) + 4
	for _, c := range k.Components {
		size += len(identityTags[c.Type]) + 1 + 4 + len(c.Value)
	}
	buf := make([]byte, 0, size)
	buf = append(buf, identityDomain...)
	buf = appendLengthPrefixed(buf, k.EntityType)
	buf = appendLength(buf, len(k.Components))
	for _, c := range k.Components {
		buf = append(buf, identityTags[c.Type]...)
		buf = append(buf, ':')
		buf = appendLengthPrefixed(buf, c.Value)
	}
	return buf
}

func appendLengthPrefixed(buf []byte, s string) []byte {
	return append(appendLength(buf, len(s)), s...)
}

// appendLength appends n as u32be. Inputs anywhere near 4 GiB are impossible
// for real keys, so exceeding the range is a programming error.
func appendLength(buf []byte, n int) []byte {
	if n < 0 || uint64(n) > math.MaxUint32 {
		panic("ir: identity length out of the u32 range")
	}
	return binary.BigEndian.AppendUint32(buf, uint32(n)) //nolint:gosec // range checked above
}

// ID returns the rtg_id of k: lowercase hex SHA-256 of its serialization.
func (k Key) ID() string {
	sum := sha256.Sum256(k.Serialize())
	return hex.EncodeToString(sum[:])
}

// Identity is a convenience wrapper: NewKey followed by ID.
func Identity(entityType string, components ...KeyComponent) (string, error) {
	k, err := NewKey(entityType, components)
	if err != nil {
		return "", err
	}
	return k.ID(), nil
}
