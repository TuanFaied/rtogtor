package mapping

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/TuanFaied/rtogtor/engine/internal/jsonschemas"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// Format of a mapping document.
type Format string

// Formats.
const (
	FormatJSON Format = "json"
	FormatYAML Format = "yaml"
)

// MaxFileSize bounds the size of a mapping document.
const MaxFileSize = 16 << 20

// FormatFromPath infers the format from a file extension.
func FormatFromPath(path string) (Format, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return FormatJSON, true
	case ".yaml", ".yml":
		return FormatYAML, true
	}
	return "", false
}

// LoadFile reads, parses and validates a mapping file.
//
// The returned list holds every error and warning found. The mapping is nil
// if the list contains errors.
func LoadFile(path string) (*Mapping, rtgerr.List) {
	format, ok := FormatFromPath(path)
	if !ok {
		return nil, rtgerr.List{rtgerr.New(rtgerr.MapParse, "cannot infer the format of %q: use a .json, .yaml or .yml extension", filepath.Base(path))}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, rtgerr.List{rtgerr.New(rtgerr.MapParse, "cannot open mapping file: %v", err)}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, rtgerr.List{rtgerr.New(rtgerr.MapParse, "cannot read mapping file: %v", err)}
	}
	if len(data) > MaxFileSize {
		return nil, rtgerr.List{rtgerr.New(rtgerr.MapParse, "mapping file is larger than %d bytes", MaxFileSize)}
	}
	return Decode(data, format)
}

// Decode parses and validates a mapping document.
//
// The returned list holds every error and warning found. The mapping is nil
// if the list contains errors.
func Decode(data []byte, format Format) (*Mapping, rtgerr.List) {
	jsonData, perr := toJSON(data, format)
	if perr != nil {
		return nil, rtgerr.List{perr}
	}

	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonData))
	if err != nil {
		return nil, rtgerr.List{rtgerr.New(rtgerr.MapParse, "invalid JSON: %v", err)}
	}

	// Check the version before the schema: other versions have other schemas.
	if obj, ok := instance.(map[string]any); ok {
		if v, ok := obj["specVersion"].(string); ok && !spec.Supports(v) {
			return nil, rtgerr.List{rtgerr.New(rtgerr.SpecUnsupportedVersion,
				"mapping declares specVersion %s; this engine supports %s", v, supportedRange()).At("/specVersion")}
		}
	}

	violations, err := jsonschemas.Validate(spec.MappingSchemaFile, instance)
	if err != nil {
		return nil, rtgerr.List{rtgerr.New(rtgerr.Internal, "schema validation failed: %v", err)}
	}
	if len(violations) > 0 {
		list := make(rtgerr.List, len(violations))
		for i, v := range violations {
			list[i] = rtgerr.New(rtgerr.MapSchema, "%s", v.Message).At(v.Path)
		}
		return nil, list
	}

	var m Mapping
	dec := json.NewDecoder(bytes.NewReader(jsonData))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		// The schema forbids unknown fields, so this indicates the Go types
		// and the schema disagree.
		return nil, rtgerr.List{rtgerr.New(rtgerr.Internal, "mapping passed schema validation but does not decode: %v", err)}
	}
	m.applyDefaults()

	issues := validateSemantics(&m)
	if issues.HasErrors() {
		return nil, issues
	}
	return &m, issues
}

func supportedRange() string {
	parts := strings.SplitN(spec.Version, ".", 3)
	return parts[0] + "." + parts[1] + ".x"
}

// toJSON converts the document to JSON, rejecting duplicate keys.
func toJSON(data []byte, format Format) ([]byte, *rtgerr.Error) {
	switch format {
	case FormatJSON:
		if err := checkJSON(data); err != nil {
			return nil, rtgerr.New(rtgerr.MapParse, "invalid JSON: %v", err)
		}
		return data, nil
	case FormatYAML:
		dec := yaml.NewDecoder(bytes.NewReader(data))
		var doc any
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, rtgerr.New(rtgerr.MapParse, "mapping document is empty")
			}
			return nil, rtgerr.New(rtgerr.MapParse, "invalid YAML: %v", err)
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			return nil, rtgerr.New(rtgerr.MapParse, "a mapping file must contain exactly one YAML document")
		}
		out, err := json.Marshal(doc)
		if err != nil {
			return nil, rtgerr.New(rtgerr.MapParse, "YAML cannot be represented as JSON (are all keys strings?): %v", err)
		}
		return out, nil
	default:
		return nil, rtgerr.New(rtgerr.MapParse, "unknown mapping format %q", string(format))
	}
}

// checkJSON verifies that data is a single JSON value without duplicate
// object keys. encoding/json silently keeps the last duplicate, which would
// let a mapping mean something other than what a reader sees.
func checkJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkJSON(dec, ""); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the top-level value")
	}
	return nil
}

func walkJSON(dec *json.Decoder, path string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key := keyTok.(string)
			child := path + "/" + escapePointer(key)
			if seen[key] {
				return fmt.Errorf("duplicate key %s at %s", strconv.Quote(key), child)
			}
			seen[key] = true
			if err := walkJSON(dec, child); err != nil {
				return err
			}
		}
	case '[':
		for i := 0; dec.More(); i++ {
			if err := walkJSON(dec, path+"/"+strconv.Itoa(i)); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token() // closing delimiter
	return err
}

func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
