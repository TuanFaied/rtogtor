# Changelog

Each component (spec, engine, each SDK) is versioned independently. Entries are
grouped by component.

## Unreleased

### Milestone 6 (branch `milestone-6/hardening`)

- Retry policy per run: `migrate --retry-attempts/--retry-initial-backoff/
  --retry-max-backoff`, `RunOptions.Retry` (Go), protocol `migrate.retry`,
  and a `retry` option in every SDK. Run state and reports record the policy
  (`retry`) and the number of retried operations (`retries`)
- Fault injection: a `write` fault point inside retried writes, and the
  test-only `RTOGTOR_TEST_FAULT` variable for real engines in any language
- `verify --values` (protocol `verify.values`, SDK `values` option):
  compares every migrated value in both directions; new code
  `RTG-VERIFY-005`; report `entities[].valueMismatches`
- Neo4j inspection lists range indexes (`labels[].indexes`); graph →
  relational plans warn when the key has no index
- Bounded-memory tests in both directions; throughput measurement with
  `RTOGTOR_PERF_ROWS`; value encoding benchmarks
- Fuzz targets for mappings, protocol lines, values, JSON and the failure
  log, run in CI
- Security review (`docs/security.md`); Python build requires
  `setuptools>=83`
- Go API: `Migration.VerifyWith(ctx, runID, VerifyOptions)`;
  `rtogtor.RetryPolicy`, `rtogtor.DefaultRetry`

### Milestone 5 (branch `milestone-5/neo4j-to-postgres`)

- Engine: Neo4j → PostgreSQL migrations (`direction: graph-to-relational`),
  with the semantics in ADR-0006: key-ordered resumable reads from Neo4j,
  `INSERT … ON CONFLICT` upserts, foreign keys written with the row or, for
  self-references and cycles, filled afterwards; reverse verification
- Spec: plan step kinds `ensure-table`, `load-rows`, `update-foreign-keys`;
  `destination.createTables`; plan fields `scale` and `fractionalDigits`;
  protocol `plan` accepts `destination` and `destinationMetadata`
- Spec (breaking, pre-1.0): report `entities[].sourceRows` and
  `destinationNodes` renamed `sourceCount` and `destinationCount`, since
  either side can be rows or nodes
- Error codes: RTG-PLAN-008…011, RTG-EXEC-005…007, RTG-PG-002
- CLI: `inspect`, `plan`, `migrate` and `verify` take PostgreSQL or Neo4j
  URLs; `plan --dest-env` for graph-to-relational mappings
- Go API (breaking, pre-1.0): `PlanFromMetadata(mapping, source,
  destination)`; `Inspect` accepts Neo4j connections
- SDKs: `Engine.plan` takes optional destination metadata; `Migration.plan`
  sends the destination; the reverse plan conformance case passes in C#,
  Python and TypeScript
- Example: `examples/ecommerce/mapping-reverse.yaml`

### Milestone 4 (branch `milestone-4/python-typescript`)

- `tools/codegen`: Python (Pydantic v2) and TypeScript emitters; schemas ship
  with generated code; `Warning` renamed `InspectionWarning` (shadowed
  Python's built-in)
- Python SDK `rtogtor`: sync and asyncio APIs, YAML 1.2 core-schema typing,
  platform wheels bundling the engine, `python -m rtogtor`
- TypeScript SDK `@rtogtor/sdk`: AbortSignal cancellation, `await using`,
  per-platform engine packages via optionalDependencies, `npx rtogtor`
- Both SDKs pass the conformance suite and property tests against independent
  identity implementations
- CI for both SDKs; fixed the .NET workflow (`build-engine.sh` lacked its
  execute bit on Linux and macOS)

### Milestone 3 (branch `milestone-3/protocol-csharp`)

- Spec: engine ↔ SDK protocol 1.0 (`protocol.md`, `protocol.schema.json`),
  RTG-PROTO-002…008; named `$defs` for report and run-state shapes
- Engine: public Go API (`NewMigration`, `Plan`, `Run`, `Verify`, …) used by
  the CLI and by `rtogtor serve --stdio`; cancellation during connect is
  reported as cancellation, not as a connection failure
- `tools/codegen`: SDK types and error codes generated from the schemas
- .NET SDK `RtoGtoR`: Migration/Engine/Mapping/Connection API, local schema
  validation, YAML 1.2 typing matching the engine, NuGet package bundling the
  engine for five platforms
- Conformance runner protocol; CI for codegen drift and the .NET SDK
- Fixed: Go end-to-end tests now delete their Neo4j test data (cleanup had
  been failing silently)

### Milestone 2 (branch `milestone-2/pg-to-neo4j`)

- Spec: `plan`, `report` and `run-state` schemas; 20 new error codes
  (PLAN, EXEC, RUN, NEO, VERIFY); RFC 8785 canonical JSON, stricter on
  inexact numbers
- Engine: planner, Neo4j adapter, PostgreSQL keyset reads, executor with
  checkpoints, failed-records store, retries and resume, verification,
  reports
- CLI: `plan`, `migrate` (`--dry-run`, `--resume`), `verify`, `report`,
  `failures`
- Docs: migration guide, recovery guarantees, known limitations

### Specification 0.1.0 (draft)

- Canonical value encoding (`specs/values.md`)
- Identity derivation `rtg-id/v1` with test vectors (`specs/identity.md`)
- Stable error codes (`specs/error-codes.json`, `specs/error-codes.md`)
- JSON Schemas: mapping, schema metadata, IR (draft)

### Engine 0.1.0-dev

- Canonical value parsing and formatting
- `rtg_id` derivation
- Mapping loading (JSON/YAML) with schema and semantic validation
- CLI commands `version` and `validate`
- Relational and graph adapter interfaces (unstable)
- PostgreSQL adapter: schema inspection from pg_catalog in a read-only
  snapshot (types, precision, enums, domains, arrays, keys, foreign keys)
- CLI command `inspect` (connection URL from an environment variable only;
  credentials redacted from every error)

### Specification 0.1.0 (draft), continued

- Error codes RTG-SCHEMA-001…003, RTG-CONN-001…002, RTG-PG-001

### Repository

- `docker-compose.yml` with PostgreSQL 17 and Neo4j 5 (localhost-only ports)
- CI workflow for the engine; Dependabot for Go modules and GitHub Actions
- Development guide
