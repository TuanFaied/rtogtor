// Package canonjson serializes JSON with the JSON Canonicalization Scheme,
// RFC 8785: sorted object keys (by UTF-16 code units), no insignificant
// whitespace, ECMAScript number formatting and minimal string escaping.
//
// Canonical bytes are what RtoGtoR hashes (mapping hashes) and compares
// (plans, reports, conformance outputs).
package canonjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/TuanFaied/rtogtor/engine/internal/esnum"
)

// Marshal encodes v with encoding/json and canonicalizes the result.
func Marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Canonicalize(raw)
}

// Canonicalize rewrites a JSON document in canonical form.
func Canonicalize(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("canonjson: unexpected data after the top-level value")
	}
	var buf bytes.Buffer
	if err := write(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func write(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		f, err := strconv.ParseFloat(string(x), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return fmt.Errorf("canonjson: number %s is not representable as an IEEE 754 double", x)
		}
		out := esnum.Format(f)
		if !sameDecimal(string(x), out) {
			return fmt.Errorf("%w: %s would become %s", ErrInexactNumber, x, out)
		}
		buf.WriteString(out)
	case string:
		writeString(buf, x)
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := write(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			if err := write(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonjson: unexpected value of type %T", v)
	}
	return nil
}

// ErrInexactNumber reports a number that an IEEE 754 double cannot represent
// exactly. RFC 8785 serializes every number as a double; RtoGtoR refuses to
// change a value silently instead.
var ErrInexactNumber = errors.New("canonjson: number is not exactly representable as a double")

// sameDecimal reports whether two decimal number texts denote the same value.
func sameDecimal(a, b string) bool {
	ra, ok1 := new(big.Rat).SetString(a)
	rb, ok2 := new(big.Rat).SetString(b)
	return ok1 && ok2 && ra.Cmp(rb) == 0
}

// lessUTF16 orders strings by their UTF-16 code units (RFC 8785 §3.2.3).
func lessUTF16(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

const hexDigits = "0123456789abcdef"

// writeString escapes only what RFC 8785 requires: '"', '\\' and control
// characters. Everything else, including non-ASCII, is written as UTF-8.
func writeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			buf.WriteString(`\"`)
		case r == '\\':
			buf.WriteString(`\\`)
		case r == '\b':
			buf.WriteString(`\b`)
		case r == '\f':
			buf.WriteString(`\f`)
		case r == '\n':
			buf.WriteString(`\n`)
		case r == '\r':
			buf.WriteString(`\r`)
		case r == '\t':
			buf.WriteString(`\t`)
		case r < 0x20:
			buf.WriteString(`\u00`)
			buf.WriteByte(hexDigits[r>>4])
			buf.WriteByte(hexDigits[r&0xf])
		default:
			buf.WriteString(s[i : i+size])
		}
		i += size
	}
	buf.WriteByte('"')
}
