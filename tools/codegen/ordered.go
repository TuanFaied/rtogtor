package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// object is a JSON object that remembers key order, so generated code lists
// properties in the order the schema declares them.
type object struct {
	keys []string
	vals map[string]any
}

func (o *object) get(k string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.vals[k]
	return v, ok
}

func (o *object) obj(k string) *object {
	v, _ := o.get(k)
	x, _ := v.(*object)
	return x
}

func (o *object) str(k string) string {
	v, _ := o.get(k)
	s, _ := v.(string)
	return s
}

func (o *object) list(k string) []any {
	v, _ := o.get(k)
	l, _ := v.([]any)
	return l
}

func (o *object) has(k string) bool {
	_, ok := o.get(k)
	return ok
}

// parse decodes JSON keeping object key order.
func parse(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("unexpected data after the top-level value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch d {
	case '{':
		o := &object{vals: map[string]any{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k := kt.(string)
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			o.keys = append(o.keys, k)
			o.vals[k] = v
		}
		_, err := dec.Token()
		return o, err
	case '[':
		var l []any
		for dec.More() {
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			l = append(l, v)
		}
		_, err := dec.Token()
		if l == nil {
			l = []any{}
		}
		return l, err
	}
	return nil, fmt.Errorf("unexpected delimiter %v", d)
}
