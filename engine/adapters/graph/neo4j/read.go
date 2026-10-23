package neo4j

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
	"github.com/TuanFaied/rtogtor/engine/internal/canonjson"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

// Reading nodes for graph → relational migrations (ADR-0006 §7).

// cypherKeyTypes names the Cypher type of each key property type, for type
// predicates (IS :: <type> NOT NULL).
var cypherKeyTypes = map[string]string{
	"Boolean":       "BOOLEAN",
	"Long":          "INTEGER",
	"String":        "STRING",
	"Date":          "DATE",
	"LocalDateTime": "LOCAL DATETIME",
	"DateTime":      "ZONED DATETIME",
}

func labelExpr(v string, labels []string) string {
	var sb strings.Builder
	sb.WriteString(v)
	for _, l := range labels {
		sb.WriteString(":" + quote(l))
	}
	return sb.String()
}

// keyed returns the predicate a node needs to be read: every key property
// present with its key type.
func keyed(v string, key []graph.NodeField) (string, error) {
	parts := make([]string, len(key))
	for i, k := range key {
		stored, ok := keyTypes[k.Type]
		if !ok {
			return "", rtgerr.New(rtgerr.PlanUnsupported, "%s key properties cannot be read in key order", k.Type)
		}
		parts[i] = v + "." + quote(k.Property) + " IS :: " + cypherKeyTypes[stored] + " NOT NULL"
	}
	return strings.Join(parts, " AND "), nil
}

func (a *Adapter) nodeQuery(req graph.NodeReadRequest) (string, map[string]any, error) {
	if len(req.Labels) == 0 || len(req.Key) == 0 || req.Limit <= 0 {
		return "", nil, rtgerr.New(rtgerr.Internal, "ReadNodes needs labels, key properties and a positive limit")
	}
	where, err := keyed("n", req.Key)
	if err != nil {
		return "", nil, err
	}
	params := map[string]any{"limit": int64(req.Limit)}
	if c := req.After; c != nil {
		if len(c.Key) != len(req.Key) {
			return "", nil, rtgerr.New(rtgerr.Internal, "cursor has %d values for %d key properties", len(c.Key), len(req.Key))
		}
		// (k1, …, kn, elementId) > (c1, …, cn, eid), expanded.
		var alts []string
		var equal []string
		for i, k := range req.Key {
			v, err := encodeValue(k.Type, "", c.Key[i])
			if err != nil {
				return "", nil, rtgerr.New(rtgerr.Internal, "cursor value for %s: %v", k.Property, err)
			}
			p := fmt.Sprintf("c%d", i)
			params[p] = v
			prop := "n." + quote(k.Property)
			alts = append(alts, "("+strings.Join(append(append([]string{}, equal...), prop+" > $"+p), " AND ")+")")
			equal = append(equal, prop+" = $"+p)
		}
		params["eid"] = c.ElementID
		alts = append(alts, "("+strings.Join(append(equal, "elementId(n) > $eid"), " AND ")+")")
		where += " AND (" + strings.Join(alts, " OR ") + ")"
	}

	order := make([]string, 0, len(req.Key)+1)
	keys := make([]string, len(req.Key))
	for i, k := range req.Key {
		keys[i] = "n." + quote(k.Property)
		order = append(order, keys[i])
	}
	order = append(order, "elementId(n)")
	props := make([]string, len(req.Properties))
	for i, p := range req.Properties {
		props[i] = "n." + quote(p.Property)
	}
	related := make([]string, len(req.Related))
	for i, r := range req.Related {
		pattern := "(n)-[:" + quote(r.Type) + "]->(m)"
		if !r.Outgoing {
			pattern = "(n)<-[:" + quote(r.Type) + "]-(m)"
		}
		match, err := keyed("m", r.Key)
		if err != nil {
			return "", nil, err
		}
		if len(r.Labels) > 0 {
			match = labelExpr("m", r.Labels) + " AND " + match
		}
		rk := make([]string, len(r.Key))
		for j, k := range r.Key {
			rk[j] = "m." + quote(k.Property)
		}
		// A non-matching node yields null; two entries are enough to detect
		// more than one related node.
		related[i] = "[" + pattern + " | CASE WHEN " + match + " THEN [" + strings.Join(rk, ", ") + "] END][..2]"
	}
	cypher := "MATCH " + labelExpr("(n", req.Labels) + ") WHERE " + where +
		" WITH n ORDER BY " + strings.Join(order, ", ") + " LIMIT $limit" +
		" RETURN elementId(n) AS id, [" + strings.Join(keys, ", ") + "] AS key, [" + strings.Join(props, ", ") +
		"] AS props, [" + strings.Join(related, ", ") + "] AS related"
	return cypher, params, nil
}

// ReadNodes implements graph.Adapter.
func (a *Adapter) ReadNodes(ctx context.Context, req graph.NodeReadRequest) ([]graph.NodeRecord, error) {
	cypher, params, err := a.nodeQuery(req)
	if err != nil {
		return nil, err
	}
	what := "reading " + labelExpr("", req.Labels) + " nodes"
	recs, err := a.query(ctx, what, cypher, params)
	if err != nil {
		return nil, err
	}
	out := make([]graph.NodeRecord, 0, len(recs))
	for _, rec := range recs {
		id, _ := rec.Get("id")
		rawKey, _ := rec.Get("key")
		rawProps, _ := rec.Get("props")
		rawRel, _ := rec.Get("related")
		n := graph.NodeRecord{ElementID: fmt.Sprint(id)}
		if n.Key, err = decodeKey(req.Key, rawKey); err != nil {
			// The query only returns keys of the key types; failing here
			// is a defect.
			return nil, rtgerr.New(rtgerr.Internal, "%s: %v", what, err)
		}
		vals := asList(rawProps)
		values := make([]ir.Value, len(req.Properties))
		for i, p := range req.Properties {
			v, err := decodeValue(p.Type, p.Element, vals[i])
			if err != nil {
				n.Err = rtgerr.New(rtgerr.ExecConversion, "property %s: %v", p.Property, err)
				break
			}
			values[i] = v
		}
		if n.Err == nil {
			n.Values = values
		}
		rels := asList(rawRel)
		n.Related = make([][]graph.RelatedNode, len(req.Related))
		for i, r := range req.Related {
			for _, m := range asList(rels[i]) {
				rn := graph.RelatedNode{}
				if m != nil {
					if rn.Key, err = decodeKey(r.Key, m); err != nil {
						return nil, rtgerr.New(rtgerr.Internal, "%s: related :%s node: %v", what, r.Type, err)
					}
					rn.Matches = true
				}
				n.Related[i] = append(n.Related[i], rn)
			}
		}
		out = append(out, n)
	}
	return out, nil
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func decodeKey(fields []graph.NodeField, raw any) ([]ir.Value, error) {
	vals := asList(raw)
	if len(vals) != len(fields) {
		return nil, fmt.Errorf("expected %d key values, got %d", len(fields), len(vals))
	}
	out := make([]ir.Value, len(fields))
	for i, f := range fields {
		v, err := decodeValue(f.Type, "", vals[i])
		if err != nil {
			return nil, fmt.Errorf("key property %s: %w", f.Property, err)
		}
		out[i] = v
	}
	return out, nil
}

// decodeValue converts a Neo4j value to a canonical value of logical type t,
// accepting the property types ReadableFrom lists.
func decodeValue(t, elem ir.LogicalType, v any) (ir.Value, error) {
	if v == nil {
		return ir.NullValue(), nil
	}
	wrong := func() (ir.Value, error) {
		return ir.Value{}, fmt.Errorf("a %s value cannot be read as %s", neoTypeName(v), t)
	}
	canon := func(text string) (ir.Value, error) {
		c, err := ir.Canonicalize(t, text)
		if err != nil {
			return ir.Value{}, err
		}
		return ir.TextValue(c), nil
	}
	switch t {
	case ir.TypeArray:
		items, ok := v.([]any)
		if !ok {
			return wrong()
		}
		out := ir.Value{Elements: make([]ir.Value, len(items))}
		for i, item := range items {
			e, err := decodeValue(elem, "", item)
			if err != nil {
				return ir.Value{}, fmt.Errorf("element %d: %w", i, err)
			}
			out.Elements[i] = e
		}
		return out, nil
	case ir.TypeBool:
		if b, ok := v.(bool); ok {
			return ir.TextValue(strconv.FormatBool(b)), nil
		}
	case ir.TypeInt64:
		if n, ok := v.(int64); ok {
			return ir.TextValue(strconv.FormatInt(n, 10)), nil
		}
	case ir.TypeDecimal:
		switch x := v.(type) {
		case string:
			return canon(x)
		case int64:
			return ir.TextValue(strconv.FormatInt(x, 10)), nil
		}
	case ir.TypeFloat64:
		switch x := v.(type) {
		case float64:
			return ir.TextValue(ir.FormatFloat64(x)), nil
		case int64:
			f := float64(x)
			if f >= math.MaxInt64 || int64(f) != x {
				return ir.Value{}, fmt.Errorf("integer %d has no exact float64 value", x)
			}
			return ir.TextValue(ir.FormatFloat64(f)), nil
		}
	case ir.TypeString:
		if s, ok := v.(string); ok {
			return canon(s)
		}
	case ir.TypeUUID:
		if s, ok := v.(string); ok {
			return canon(s)
		}
	case ir.TypeJSON:
		if s, ok := v.(string); ok {
			c, err := canonjson.Canonicalize([]byte(s))
			if err != nil {
				return ir.Value{}, fmt.Errorf("not a JSON document: %w", err)
			}
			return ir.Value{JSON: c}, nil
		}
	case ir.TypeBytes:
		if b, ok := v.([]byte); ok {
			return ir.TextValue(base64.StdEncoding.EncodeToString(b)), nil
		}
	case ir.TypeDate:
		if d, ok := v.(driver.Date); ok {
			return canon(d.Time().Format("2006-01-02"))
		}
	case ir.TypeTimestamp:
		if d, ok := v.(driver.LocalDateTime); ok {
			return canon(d.Time().Format("2006-01-02T15:04:05.999999999"))
		}
	case ir.TypeTimestampTZ:
		if d, ok := v.(time.Time); ok {
			return canon(d.Format(time.RFC3339Nano))
		}
	default:
		return ir.Value{}, fmt.Errorf("%s values cannot be read from Neo4j by this engine version", t)
	}
	return wrong()
}

// neoTypeName names a driver value's Neo4j type for messages.
func neoTypeName(v any) string {
	switch v.(type) {
	case bool:
		return "Boolean"
	case int64:
		return "Long"
	case float64:
		return "Double"
	case string:
		return "String"
	case []byte:
		return "ByteArray"
	case []any:
		return "List"
	case driver.Date:
		return "Date"
	case driver.LocalDateTime:
		return "LocalDateTime"
	case time.Time:
		return "DateTime"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// CountKeyedNodes implements graph.Adapter.
func (a *Adapter) CountKeyedNodes(ctx context.Context, labels []string, key []graph.NodeField) (int64, error) {
	where, err := keyed("n", key)
	if err != nil {
		return 0, err
	}
	return a.count(ctx, "counting "+labelExpr("", labels)+" nodes",
		"MATCH "+labelExpr("(n", labels)+") WHERE "+where+" RETURN count(n) AS n", nil)
}

// CountNodesWithoutKey implements graph.Adapter.
func (a *Adapter) CountNodesWithoutKey(ctx context.Context, labels []string, key []graph.NodeField) (int64, error) {
	where, err := keyed("n", key)
	if err != nil {
		return 0, err
	}
	return a.count(ctx, "counting "+labelExpr("", labels)+" nodes without a key",
		"MATCH "+labelExpr("(n", labels)+") WHERE NOT ("+where+") RETURN count(n) AS n", nil)
}

// CountRelated implements graph.Adapter.
func (a *Adapter) CountRelated(ctx context.Context, labels []string, key []graph.NodeField, rel graph.RelatedRead) (int64, error) {
	where, err := keyed("n", key)
	if err != nil {
		return 0, err
	}
	match, err := keyed("m", rel.Key)
	if err != nil {
		return 0, err
	}
	if len(rel.Labels) > 0 {
		match = labelExpr("m", rel.Labels) + " AND " + match
	}
	pattern := "(n)-[:" + quote(rel.Type) + "]->(m)"
	if !rel.Outgoing {
		pattern = "(n)<-[:" + quote(rel.Type) + "]-(m)"
	}
	return a.count(ctx, "counting :"+rel.Type+" relationships",
		"MATCH "+labelExpr("(n", labels)+") WHERE "+where+
			" WITH n, ["+pattern+" | CASE WHEN "+match+" THEN 1 ELSE 0 END][..2] AS r"+
			" WHERE size(r) = 1 AND r[0] = 1 RETURN count(n) AS n", nil)
}
