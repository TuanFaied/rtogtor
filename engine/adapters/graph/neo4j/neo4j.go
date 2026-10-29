// Package neo4j implements the graph adapter for Neo4j 5.
//
// Writes are idempotent: nodes are merged on a uniqueness-constrained
// identity property and relationships are merged between nodes found by
// identity, so replaying a batch after a failure never creates duplicates.
package neo4j

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"

	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/TuanFaied/rtogtor/engine/adapters/graph"
	"github.com/TuanFaied/rtogtor/engine/internal/redact"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

// Adapter is a Neo4j connection.
type Adapter struct {
	TypeSupport
	drv      driver.Driver
	database string
	secrets  []string
}

var _ graph.Adapter = (*Adapter)(nil)

// Open connects to Neo4j. connURL is a neo4j://, neo4j+s://, bolt:// or
// bolt+s:// URL; credentials, if any, are given as user:password in the URL.
// database selects the database; empty means the server default.
//
// Errors never contain the URL or password.
func Open(ctx context.Context, connURL, database string) (*Adapter, error) {
	secrets := redact.URLSecrets(connURL)
	u, err := url.Parse(connURL)
	if err != nil {
		return nil, rtgerr.New(rtgerr.ConnConfig, "invalid Neo4j URL: %s", redact.Secrets(err.Error(), secrets...))
	}
	switch u.Scheme {
	case "neo4j", "neo4j+s", "neo4j+ssc", "bolt", "bolt+s", "bolt+ssc":
	default:
		return nil, rtgerr.New(rtgerr.ConnConfig, "invalid Neo4j URL: scheme must be neo4j://, neo4j+s://, bolt:// or bolt+s://")
	}
	auth := driver.NoAuth()
	if u.User != nil {
		pw, _ := u.User.Password()
		auth = driver.BasicAuth(u.User.Username(), pw, "")
		u.User = nil
	}
	drv, err := driver.NewDriver(u.String(), auth)
	if err != nil {
		return nil, rtgerr.New(rtgerr.ConnConfig, "invalid Neo4j settings: %s", redact.Secrets(err.Error(), secrets...))
	}
	if err := drv.VerifyConnectivity(ctx); err != nil {
		_ = drv.Close(context.Background())
		return nil, rtgerr.New(rtgerr.ConnFailed, "cannot connect to Neo4j: %s", redact.Secrets(err.Error(), secrets...))
	}
	return &Adapter{drv: drv, database: database, secrets: secrets}, nil
}

// Close implements graph.Adapter.
func (a *Adapter) Close() error { return a.drv.Close(context.Background()) }

// Capabilities implements graph.Adapter.
func (a *Adapter) Capabilities() graph.Capabilities {
	return graph.Capabilities{UniquenessConstraints: true, TransactionalBatches: true, SchemaInspection: true}
}

// quote returns a backtick-quoted Cypher identifier.
func quote(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// wrap converts a driver error into an RtoGtoR error without secrets.
// Transient errors get the retryable RTG-EXEC-003 code.
func (a *Adapter) wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	msg := redact.Secrets(err.Error(), a.secrets...)
	if driver.IsRetryable(err) {
		return rtgerr.New(rtgerr.ExecBatchFailed, "%s failed with a transient error: %s", what, msg).Wrap(err)
	}
	return rtgerr.New(rtgerr.NeoQuery, "%s failed: %s", what, msg).Wrap(err)
}

func (a *Adapter) session(ctx context.Context, mode driver.AccessMode) driver.Session {
	return a.drv.NewSession(ctx, driver.SessionConfig{DatabaseName: a.database, AccessMode: mode})
}

// query runs a read query and returns all records.
func (a *Adapter) query(ctx context.Context, what, cypher string, params map[string]any) ([]*driver.Record, error) {
	s := a.session(ctx, driver.AccessModeRead)
	defer func() { _ = s.Close(context.Background()) }()
	recs, err := driver.ExecuteRead(ctx, s, func(tx driver.ManagedTransaction) ([]*driver.Record, error) {
		res, err := tx.Run(ctx, cypher, params)
		if err != nil {
			return nil, err
		}
		return res.Collect(ctx)
	})
	return recs, a.wrap(what, err)
}

// ServerInfo implements graph.Adapter.
func (a *Adapter) ServerInfo(ctx context.Context) (graph.ServerInfo, error) {
	info := graph.ServerInfo{Product: Product, Database: a.database}
	recs, err := a.query(ctx, "reading server information",
		"CALL dbms.components() YIELD name, versions, edition WHERE name = 'Neo4j Kernel' RETURN versions[0] AS version, edition", nil)
	if err != nil {
		return info, err
	}
	if len(recs) == 1 {
		v, _ := recs[0].Get("version")
		e, _ := recs[0].Get("edition")
		info.Version, _ = v.(string)
		info.Edition, _ = e.(string)
	}
	if info.Database == "" {
		recs, err := a.query(ctx, "reading the database name", "CALL db.info() YIELD name RETURN name", nil)
		if err == nil && len(recs) == 1 {
			n, _ := recs[0].Get("name")
			info.Database, _ = n.(string)
		}
	}
	return info, nil
}

// EnsureIdentityConstraint implements graph.Adapter.
func (a *Adapter) EnsureIdentityConstraint(ctx context.Context, label, property string) error {
	name := quote("rtg_" + label + "_" + property)
	cypher := "CREATE CONSTRAINT " + name + " IF NOT EXISTS FOR (n:" + quote(label) + ") REQUIRE n." + quote(property) + " IS UNIQUE"
	_, err := driver.ExecuteQuery(ctx, a.drv, cypher, nil, driver.EagerResultTransformer,
		driver.ExecuteQueryWithDatabase(a.database), driver.ExecuteQueryWithWritersRouting())
	return a.wrap("creating the identity constraint on :"+label+"("+property+")", err)
}

// MergeNodes implements graph.Adapter.
func (a *Adapter) MergeNodes(ctx context.Context, b graph.NodeBatch) error {
	if len(b.Labels) == 0 || len(b.Rows) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("UNWIND $rows AS row MERGE (n:" + quote(b.Labels[0]) + " {" + quote(b.IDProperty) + ": row.id}) SET n += row.props")
	if len(b.Labels) > 1 {
		sb.WriteString(" SET n")
		for _, l := range b.Labels[1:] {
			sb.WriteString(":" + quote(l))
		}
	}
	rows := make([]any, len(b.Rows))
	for i, r := range b.Rows {
		rows[i] = map[string]any{"id": r.ID, "props": map[string]any(r.Props)}
	}
	s := a.session(ctx, driver.AccessModeWrite)
	defer func() { _ = s.Close(context.Background()) }()
	_, err := s.ExecuteWrite(ctx, func(tx driver.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, sb.String(), map[string]any{"rows": rows})
		if err != nil {
			return nil, err
		}
		return res.Consume(ctx)
	})
	return a.wrap("merging :"+b.Labels[0]+" nodes", err)
}

// MergeRelationships implements graph.Adapter.
func (a *Adapter) MergeRelationships(ctx context.Context, b graph.RelationshipBatch) ([]int, error) {
	if len(b.Rows) == 0 {
		return nil, nil
	}
	id := quote(b.IDProperty)
	cypher := "UNWIND $rows AS row" +
		" MATCH (a:" + quote(b.FromLabel) + " {" + id + ": row.from})" +
		" MATCH (b:" + quote(b.ToLabel) + " {" + id + ": row.to})" +
		" MERGE (a)-[r:" + quote(b.Type) + "]->(b) SET r += row.props" +
		" RETURN row.i AS i"
	rows := make([]any, len(b.Rows))
	for i, r := range b.Rows {
		rows[i] = map[string]any{"i": int64(i), "from": r.From, "to": r.To, "props": map[string]any(r.Props)}
	}
	s := a.session(ctx, driver.AccessModeWrite)
	defer func() { _ = s.Close(context.Background()) }()
	written, err := driver.ExecuteWrite(ctx, s, func(tx driver.ManagedTransaction) (map[int64]bool, error) {
		res, err := tx.Run(ctx, cypher, map[string]any{"rows": rows})
		if err != nil {
			return nil, err
		}
		recs, err := res.Collect(ctx)
		if err != nil {
			return nil, err
		}
		seen := make(map[int64]bool, len(recs))
		for _, rec := range recs {
			v, _ := rec.Get("i")
			if n, ok := v.(int64); ok {
				seen[n] = true
			}
		}
		return seen, nil
	})
	if err != nil {
		return nil, a.wrap("merging :"+b.Type+" relationships", err)
	}
	var missing []int
	for i := range b.Rows {
		if !written[int64(i)] {
			missing = append(missing, i)
		}
	}
	return missing, nil
}

func (a *Adapter) count(ctx context.Context, what, cypher string, params map[string]any) (int64, error) {
	recs, err := a.query(ctx, what, cypher, params)
	if err != nil {
		return 0, err
	}
	if len(recs) != 1 {
		return 0, rtgerr.New(rtgerr.Internal, "%s returned %d records", what, len(recs))
	}
	v, _ := recs[0].Get("n")
	n, ok := v.(int64)
	if !ok {
		return 0, rtgerr.New(rtgerr.Internal, "%s returned %T", what, v)
	}
	return n, nil
}

// CountNodes implements graph.Adapter.
func (a *Adapter) CountNodes(ctx context.Context, label string) (int64, error) {
	return a.count(ctx, "counting :"+label+" nodes", "MATCH (n:"+quote(label)+") RETURN count(n) AS n", nil)
}

// CountRelationships implements graph.Adapter.
func (a *Adapter) CountRelationships(ctx context.Context, relType, fromLabel, toLabel string) (int64, error) {
	return a.count(ctx, "counting :"+relType+" relationships",
		"MATCH (:"+quote(fromLabel)+")-[r:"+quote(relType)+"]->(:"+quote(toLabel)+") RETURN count(r) AS n", nil)
}

// MissingIDs implements graph.Adapter.
func (a *Adapter) MissingIDs(ctx context.Context, label, property string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	recs, err := a.query(ctx, "checking :"+label+" identities",
		"UNWIND $ids AS id OPTIONAL MATCH (n:"+quote(label)+" {"+quote(property)+": id}) WITH id, n WHERE n IS NULL RETURN id",
		map[string]any{"ids": ids})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		v, _ := r.Get("id")
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// Inspect implements graph.Adapter using db.schema.nodeTypeProperties and
// db.schema.relTypeProperties, which sample the stored data.
func (a *Adapter) Inspect(ctx context.Context, _ graph.InspectOptions) (*schemameta.Document, error) {
	info, err := a.ServerInfo(ctx)
	if err != nil {
		return nil, err
	}
	doc := &schemameta.Document{
		SpecVersion:       spec.Version,
		Kind:              schemameta.KindGraph,
		Database:          schemameta.Database{Product: Product, Version: info.Version, Name: info.Database},
		Labels:            []schemameta.Label{},
		RelationshipTypes: []schemameta.RelationshipType{},
	}

	nodeRecs, err := a.query(ctx, "inspecting node properties",
		"CALL db.schema.nodeTypeProperties() YIELD nodeLabels, propertyName, propertyTypes, mandatory RETURN nodeLabels, propertyName, propertyTypes, mandatory", nil)
	if err != nil {
		return nil, err
	}
	labels := map[string]map[string]*schemameta.GraphProperty{}
	for _, r := range nodeRecs {
		ls, _ := r.Get("nodeLabels")
		name, _ := r.Get("propertyName")
		types, _ := r.Get("propertyTypes")
		mandatory, _ := r.Get("mandatory")
		for _, l := range asStrings(ls) {
			props := labels[l]
			if props == nil {
				props = map[string]*schemameta.GraphProperty{}
				labels[l] = props
			}
			addProperty(props, name, types, mandatory)
		}
	}
	for name, props := range labels {
		doc.Labels = append(doc.Labels, schemameta.Label{Name: name, Properties: sortedProps(props)})
	}
	sort.Slice(doc.Labels, func(i, j int) bool { return doc.Labels[i].Name < doc.Labels[j].Name })

	relRecs, err := a.query(ctx, "inspecting relationship properties",
		"CALL db.schema.relTypeProperties() YIELD relType, propertyName, propertyTypes, mandatory RETURN relType, propertyName, propertyTypes, mandatory", nil)
	if err != nil {
		return nil, err
	}
	rels := map[string]map[string]*schemameta.GraphProperty{}
	for _, r := range relRecs {
		rt, _ := r.Get("relType")
		name, _ := r.Get("propertyName")
		types, _ := r.Get("propertyTypes")
		mandatory, _ := r.Get("mandatory")
		t := strings.Trim(strings.TrimPrefix(fmt.Sprint(rt), ":"), "`")
		props := rels[t]
		if props == nil {
			props = map[string]*schemameta.GraphProperty{}
			rels[t] = props
		}
		addProperty(props, name, types, mandatory)
	}
	for name, props := range rels {
		doc.RelationshipTypes = append(doc.RelationshipTypes, schemameta.RelationshipType{Name: name, Properties: sortedProps(props)})
	}
	sort.Slice(doc.RelationshipTypes, func(i, j int) bool { return doc.RelationshipTypes[i].Name < doc.RelationshipTypes[j].Name })
	return doc, nil
}

func asStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// neo4jTypes maps db.schema property type names to logical types.
var neo4jTypes = map[string]ir.LogicalType{
	"Boolean": ir.TypeBool, "Long": ir.TypeInt64, "Double": ir.TypeFloat64,
	"String": ir.TypeString, "Date": ir.TypeDate, "LocalDateTime": ir.TypeTimestamp,
	"DateTime": ir.TypeTimestampTZ, "LocalTime": ir.TypeTime, "Time": ir.TypeTimeTZ,
	"Duration": ir.TypeDuration, "ByteArray": ir.TypeBytes,
}

func addProperty(props map[string]*schemameta.GraphProperty, name, types, mandatory any) {
	n, ok := name.(string)
	if !ok || n == "" {
		return // a label or type without properties
	}
	p := props[n]
	if p == nil {
		p = &schemameta.GraphProperty{Name: n, Types: []schemameta.TypeInfo{}, Mandatory: true}
		props[n] = p
	}
	if m, ok := mandatory.(bool); ok && !m {
		p.Mandatory = false
	}
	for _, t := range asStrings(types) {
		info := schemameta.TypeInfo{NativeType: t}
		if strings.HasSuffix(t, "Array") {
			if lt, ok := neo4jTypes[strings.TrimSuffix(t, "Array")]; ok {
				arr := ir.TypeArray
				info.Type = &arr
				elem := lt
				info.Element = &schemameta.TypeInfo{Type: &elem, NativeType: strings.TrimSuffix(t, "Array")}
			}
		} else if lt, ok := neo4jTypes[t]; ok {
			info.Type = &lt
		}
		dup := false
		for _, existing := range p.Types {
			if existing.NativeType == info.NativeType {
				dup = true
			}
		}
		if !dup {
			p.Types = append(p.Types, info)
		}
	}
	sort.Slice(p.Types, func(i, j int) bool { return p.Types[i].NativeType < p.Types[j].NativeType })
}

func sortedProps(props map[string]*schemameta.GraphProperty) []schemameta.GraphProperty {
	out := make([]schemameta.GraphProperty, 0, len(props))
	for _, p := range props {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func parseInt64(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

func parseFloat64(s string) (float64, error) {
	switch s {
	case "NaN":
		return math.NaN(), nil
	case "Infinity":
		return math.Inf(1), nil
	case "-Infinity":
		return math.Inf(-1), nil
	case "-0":
		return math.Copysign(0, -1), nil
	}
	return strconv.ParseFloat(s, 64)
}
