# Changelog

Each component (spec, engine, each SDK) is versioned independently. Entries are
grouped by component.

## Unreleased

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
