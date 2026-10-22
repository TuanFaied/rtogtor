//go:build integration

// Integration tests against a real, disposable PostgreSQL server.
//
//	docker compose up -d --wait
//	RTOGTOR_TEST_POSTGRES_URL=postgres://rtogtor:<password>@127.0.0.1:55432/ecommerce?sslmode=disable \
//	  go test -tags integration ./...
//
// Each test works in its own freshly created schema and drops it afterwards.

package postgresql

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/internal/jsonschemas"
	"github.com/TuanFaied/rtogtor/engine/internal/testutil"
	"github.com/TuanFaied/rtogtor/engine/ir"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
	"github.com/TuanFaied/rtogtor/engine/spec"
)

const urlEnv = "RTOGTOR_TEST_POSTGRES_URL"

var update = flag.Bool("update", false, "rewrite golden files")

func testURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv(urlEnv)
	if u == "" {
		t.Fatalf("%s must point to a disposable PostgreSQL database for integration tests", urlEnv)
	}
	return u
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

// freshSchema creates a uniquely named schema, runs ddl inside it, and drops
// it when the test ends.
func freshSchema(t *testing.T, ddl string) string {
	t.Helper()
	conn, err := pgx.Connect(ctx(t), testURL(t))
	if err != nil {
		t.Fatalf("connect: %v", rtgerr.New(rtgerr.ConnFailed, "test database unavailable"))
	}
	t.Cleanup(func() { conn.Close(context.Background()) })

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "rtg_it_" + hex.EncodeToString(b)
	if _, err := conn.Exec(ctx(t), "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE")
	})
	script := "SET search_path TO " + name + ";\n" + ddl
	if _, err := conn.PgConn().Exec(ctx(t), script).ReadAll(); err != nil {
		t.Fatalf("loading fixture: %v", err)
	}
	return name
}

func open(t *testing.T) *Adapter {
	t.Helper()
	a, err := Open(ctx(t), testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func validateDoc(t *testing.T, doc *schemameta.Document) {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonschemas.ValidateJSON(spec.SchemaMetadataSchemaFile, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range v {
		t.Errorf("schema violation at %s: %s", x.Path, x.Message)
	}
}

// normalize removes values that legitimately vary between servers and runs.
func normalize(doc *schemameta.Document, schema string) {
	doc.Database.Version = ""
	doc.Database.Name = ""
	for i := range doc.Schemas {
		s := &doc.Schemas[i]
		if s.Name == schema {
			s.Name = "public"
		}
		for j := range s.Tables {
			t := &s.Tables[j]
			t.EstimatedRows = nil
			for k := range t.ForeignKeys {
				if t.ForeignKeys[k].ReferencedSchema == schema {
					t.ForeignKeys[k].ReferencedSchema = "public"
				}
			}
		}
	}
	for i := range doc.Warnings {
		doc.Warnings[i].Path = strings.Replace(doc.Warnings[i].Path, schema+".", "public.", 1)
	}
}

func TestInspectEcommerceMatchesGolden(t *testing.T) {
	fixture, err := os.ReadFile(testutil.RepoPath(t, "examples", "ecommerce", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	schema := freshSchema(t, string(fixture))
	a := open(t)

	doc, err := a.Inspect(ctx(t), relational.InspectOptions{Schemas: []string{schema}})
	if err != nil {
		t.Fatal(err)
	}
	validateDoc(t, doc)
	if doc.Database.Product != "postgresql" || doc.Database.Version == "" {
		t.Errorf("database info: %+v", doc.Database)
	}

	normalize(doc, schema)
	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	golden := testutil.RepoPath(t, "conformance", "cases", "ecommerce", "expected-schema-metadata.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(got) != string(want) {
		t.Errorf("inspection output differs from %s; rerun with -update if the change is intended.\ngot:\n%s", golden, got)
	}

	// Deterministic: a second inspection gives identical output.
	doc2, err := a.Inspect(ctx(t), relational.InspectOptions{Schemas: []string{schema}})
	if err != nil {
		t.Fatal(err)
	}
	normalize(doc2, schema)
	again, _ := json.MarshalIndent(doc2, "", "  ")
	if string(again)+"\n" != string(got) {
		t.Error("two inspections of the same schema differ")
	}
}

func TestInspectTypeCoverage(t *testing.T) {
	schema := freshSchema(t, `
		CREATE TYPE mood AS ENUM ('sad', 'ok', 'happy');
		CREATE DOMAIN price AS numeric(10,2);
		CREATE TABLE everything (
			id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			small       smallint NOT NULL,
			amount      numeric(10,3),
			anynum      numeric,
			ratio       real,
			code        char(3),
			label       varchar(20),
			body        text,
			flag        boolean DEFAULT false,
			ref         uuid,
			born        date,
			at_time     time(0),
			at_timetz   timetz,
			local_ts    timestamp(3),
			instant     timestamptz,
			span        interval,
			blob        bytea,
			doc         jsonb,
			tags        text[],
			feeling     mood,
			cost        price,
			location    point
		);
		CREATE TABLE no_key (a int);
	`)
	a := open(t)
	doc, err := a.Inspect(ctx(t), relational.InspectOptions{Schemas: []string{schema}})
	if err != nil {
		t.Fatal(err)
	}
	validateDoc(t, doc)

	cols := map[string]schemameta.Column{}
	for _, tbl := range doc.Schemas[0].Tables {
		if tbl.Name == "everything" {
			for _, c := range tbl.Columns {
				cols[c.Name] = c
			}
		}
	}
	typeOf := func(name string) string {
		c, ok := cols[name]
		if !ok {
			t.Fatalf("column %s missing", name)
		}
		if c.TypeInfo.Type == nil {
			return "<unsupported>"
		}
		return string(*c.TypeInfo.Type)
	}
	want := map[string]string{
		"id": "int64", "small": "int64", "amount": "decimal", "anynum": "decimal",
		"ratio": "float64", "code": "string", "label": "string", "body": "string",
		"flag": "bool", "ref": "uuid", "born": "date", "at_time": "time",
		"at_timetz": "timetz", "local_ts": "timestamp", "instant": "timestamptz",
		"span": "duration", "blob": "bytes", "doc": "json", "tags": "array",
		"feeling": "string", "cost": "decimal", "location": "<unsupported>",
	}
	for col, typ := range want {
		if got := typeOf(col); got != typ {
			t.Errorf("%s: got %s, want %s", col, got, typ)
		}
	}

	if c := cols["amount"].TypeInfo; *c.Precision != 10 || *c.Scale != 3 {
		t.Errorf("amount precision/scale: %+v", c)
	}
	if c := cols["anynum"].TypeInfo; c.Precision != nil || c.Scale != nil {
		t.Errorf("unconstrained numeric must not declare precision: %+v", c)
	}
	if c := cols["cost"].TypeInfo; *c.Precision != 10 || *c.Scale != 2 || !strings.HasSuffix(c.NativeType, ".price") {
		t.Errorf("domain: %+v", c)
	}
	if c := cols["label"].TypeInfo; *c.MaxLength != 20 {
		t.Errorf("varchar length: %+v", c)
	}
	if c := cols["local_ts"].TypeInfo; *c.FractionalDigits != 3 {
		t.Errorf("timestamp(3) precision: %+v", c)
	}
	if c := cols["at_time"].TypeInfo; *c.FractionalDigits != 0 {
		t.Errorf("time(0) precision: %+v", c)
	}
	if c := cols["instant"].TypeInfo; *c.FractionalDigits != 6 {
		t.Errorf("timestamptz default precision: %+v", c)
	}
	if c := cols["tags"].TypeInfo; c.Element == nil || *c.Element.Type != ir.TypeString {
		t.Errorf("text[] element: %+v", c)
	}
	if c := cols["feeling"].TypeInfo; strings.Join(c.EnumValues, ",") != "sad,ok,happy" {
		t.Errorf("enum values: %+v", c)
	}
	if c := cols["id"]; !c.Generated || c.Nullable {
		t.Errorf("identity column: %+v", c)
	}
	if c := cols["flag"]; !c.HasDefault || !c.Nullable {
		t.Errorf("flag column: %+v", c)
	}

	wantWarnings := map[string]bool{
		"RTG-SCHEMA-001 " + schema + ".everything.location": true,
		"RTG-SCHEMA-002 " + schema + ".no_key":              true,
	}
	for _, w := range doc.Warnings {
		key := w.Code + " " + w.Path
		if !wantWarnings[key] {
			t.Errorf("unexpected warning %s: %s", key, w.Message)
		}
		delete(wantWarnings, key)
	}
	for k := range wantWarnings {
		t.Errorf("missing warning %s", k)
	}
}

func TestInspectMissingSchema(t *testing.T) {
	a := open(t)
	_, err := a.Inspect(ctx(t), relational.InspectOptions{Schemas: []string{"rtg_does_not_exist"}})
	if !rtgerr.IsCode(err, rtgerr.SchemaNotFound) {
		t.Errorf("got %v, want %s", err, rtgerr.SchemaNotFound)
	}
}

func TestInspectionIsReadOnly(t *testing.T) {
	schema := freshSchema(t, "CREATE TABLE t (id int PRIMARY KEY);")
	a := open(t)
	if _, err := a.Inspect(ctx(t), relational.InspectOptions{Schemas: []string{schema}}); err != nil {
		t.Fatal(err)
	}
	// The adapter's transaction was read-only and rolled back; the
	// connection must be usable and in no transaction afterwards.
	if status := a.conn.PgConn().TxStatus(); status != 'I' {
		t.Errorf("connection left in transaction status %q", status)
	}
}

func TestConnectionErrorsNeverLeakSecrets(t *testing.T) {
	good, err := url.Parse(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	const secret = "Wr0ng-S3cret-Value"
	bad := *good
	bad.User = url.UserPassword(good.User.Username(), secret)

	_, err = Open(ctx(t), bad.String())
	if !rtgerr.IsCode(err, rtgerr.ConnFailed) {
		t.Fatalf("got %v, want %s", err, rtgerr.ConnFailed)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks the password: %v", err)
	}

	_, err = Open(ctx(t), "postgres://user:"+secret+"@127.0.0.1:notaport/db")
	if !rtgerr.IsCode(err, rtgerr.ConnConfig) {
		t.Fatalf("got %v, want %s", err, rtgerr.ConnConfig)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks the password: %v", err)
	}
}
