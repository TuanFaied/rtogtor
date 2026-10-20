# RtoGtoR — Multi-Language Project Specification

Oct 20, 2026 · @faied

## 1. Role, vision and principles

You are a senior software architect and open-source engineer. Design and implement **RtoGtoR**, an open-source ecosystem for bidirectional transformation between relational and graph databases, with first-class SDKs for **Go, Python, TypeScript and C#**.

The project must be production-minded, modular, testable, extensible and suitable for long-term community development across four language ecosystems.

### Long-term capabilities

- Relational → graph and graph → relational transformation
- One-time data migration, resumable at large scale
- Schema inspection and mapping suggestions
- Data validation and migration reports
- A shared, versioned, language-neutral mapping specification
- SDKs in Go, Python, TypeScript and C# with identical semantics
- Runtime access abstractions (later)
- Continuous synchronization (later)

The first release targets **PostgreSQL and Neo4j**. Do not attempt every long-term feature at once; build a working foundation first.

### Core design principles

1. **Bidirectional by design.** Architecture and mapping definitions accommodate both directions.
2. **Fidelity first.** Never silently discard information or claim lossless reversibility when the models are incompatible.
3. **Explicit mappings.** Users define how tables, columns, keys, nodes, labels, properties and relationships map to one another.
4. **Plan before execution.** Users can inspect and validate a plan before any write.
5. **Safe recovery.** Checkpoints, idempotent writes, retries and resumption wherever feasible.
6. **Bounded memory.** Large datasets are processed in batches or streams.
7. **Database independence.** Database-specific behavior lives only inside adapters.
8. **The specification is the product.** Mapping, IR, value encoding, identity, reports and error codes are defined once, language-neutrally. No SDK defines semantics on its own.
9. **One engine, many SDKs.** Migration logic is implemented exactly once. SDKs are thin, typed clients of that engine.
10. **Conformance over convention.** Every SDK must pass the same shared conformance suite; identical inputs produce byte-identical canonical outputs.
11. **Honest capabilities.** Clearly distinguish implemented, partial and planned features, per language.
12. **No unnecessary AI dependency.** Core migration and validation are deterministic. AI-assisted suggestions are optional and future.

## 2. Multi-language architecture

The migration engine is written **once, in Go**, and shipped as a single static binary. Python, TypeScript and C# SDKs are thin typed clients of that engine; the Go SDK is the engine's own public library.

&#91;embedded content: RtoGtoR architecture · 4 SDKs, 1 engine, 2 adapters\]

Types for every SDK are generated from `specs/`; Python, TypeScript and C# reach the engine through the protocol, while Go calls it in-process.

### Why one engine

Planning, batching, checkpoints, resume, retries, identity resolution and validation are the hard parts. Four independent implementations would produce four sets of bugs that disagree on edge cases such as resume after a mid-batch failure. Migration is a batch job, so it does not need to run inside the caller's process.

### Approaches considered

| Approach | Decision | Reason |
| --- | --- | --- |
| One Go engine + thin SDKs | **Chosen** | One implementation of the hard logic; single binary distributes through every package manager |
| Shared native core + FFI bindings | Rejected | Go `c-shared` libraries each carry their own runtime; cgo complicates builds and debugging |
| Four native engines + conformance suite | Rejected for migration | Four times the adapter, driver and recovery code; reserved for runtime interop (section 8) |

Record this decision as ADR-0001 in `docs/adr/`.

### Engine responsibilities (Go)

- Database adapters, IR, mapping engine, planner, executor, validator, reporter
- The `rtogtor` CLI
- A stable machine interface for SDKs (below)
- A public Go package (`github.com/TuanFaied/rtogtor/engine`) that Go users call directly, with no subprocess

### SDK responsibilities (Python, TypeScript, C#)

- Typed mapping builders and loaders (JSON/YAML), generated from the spec's JSON Schemas
- Local mapping validation against the schema
- Locating, launching and talking to the engine
- Typed models for plans, reports, run state and errors
- Idiomatic resource management (context managers, `using`/`IAsyncDisposable`, `AsyncDisposable`)
- No migration, planning or identity logic of their own

### Engine ↔ SDK interface

- **Phase 1:** subprocess with a JSON-lines protocol over stdin/stdout. Each request and response is a versioned JSON message validated against `specs/protocol.schema.json`. Progress events stream as separate messages.
- **Phase 2 (optional):** a gRPC server mode (`rtogtor serve`) defined in `specs/proto/`, for long-running services.
- Every message carries `protocolVersion`. The engine rejects unknown major versions with a stable error code.
- Credentials pass through environment variables or a credentials file reference, never as command-line arguments (visible in process lists).

### Distribution

| Ecosystem | Package | How the engine ships |
| --- | --- | --- |
| Go | `github.com/TuanFaied/rtogtor/engine` | Native library + `go install` for the CLI |
| Python | `rtogtor` on PyPI | Platform-specific wheels bundling the binary |
| TypeScript | `@rtogtor/sdk` on npm | Per-platform packages via `optionalDependencies` (esbuild pattern) |
| C# | `RtoGtoR` on NuGet | Runtime-specific assets under `runtimes/<rid>/native` |
| CLI only | GitHub Releases, Homebrew, Docker image | Signed archives with checksums |

Target platforms: linux-x64, linux-arm64, darwin-x64, darwin-arm64, windows-x64. Each SDK verifies at startup that the bundled binary's version is within its supported engine range.

## 3. Portable specification

The `specs/` directory is the single source of truth for every language. It is versioned independently of any SDK (`specVersion`, semver). No SDK may add semantics that are not in the spec.

### Spec documents

| File | Defines |
| --- | --- |
| `mapping.schema.json` | Mapping definitions (both directions) |
| `ir.schema.json` | Canonical intermediate representation |
| `values.md` | Canonical value encoding (below) |
| `identity.md` | Byte-exact identity derivation (below) |
| `schema-metadata.schema.json` | Inspected schema output (tables, keys, labels, types) |
| `plan.schema.json` | Execution plan output |
| `report.schema.json` | Migration and verification reports |
| `run-state.schema.json` | Run model, checkpoints, failed records |
| `protocol.schema.json` | Engine ↔ SDK messages |
| `error-codes.md` | Stable error codes |
| `compatibility.md` | Spec × engine × SDK version matrix |

### Generated types

SDK types are **generated** from the JSON Schemas, never hand-written. CI fails if generated code is out of date.

| Language | Generator |
| --- | --- |
| Go | `go-jsonschema` (or `oapi-codegen` types) |
| Python | `datamodel-code-generator` → Pydantic v2 models |
| TypeScript | `json-schema-to-typescript` |
| C# | NJsonSchema → records with `System.Text.Json` |

Hand-written code may wrap generated types with builders and convenience methods only.

### Canonical value encoding

Languages disagree on numeric and temporal precision; this is the most likely source of silent data loss. The IR, plans, reports and the SDK protocol use these encodings:

| Logical type | Encoding | Reason |
| --- | --- | --- |
| `int64` | JSON string of decimal digits | JS `number` loses precision above 2^53 |
| `decimal` | String, with declared precision/scale in type metadata | Postgres `numeric` is arbitrary; C# `decimal` ≈ 28 digits; Go has no native decimal |
| `float64` | JSON number; `NaN`/`±Infinity` as strings | JSON has no NaN |
| `timestamp` / `timestamptz` | ISO 8601, explicit precision (up to nanoseconds), explicit offset or `Z` for tz-aware | Postgres µs, JS ms, Neo4j ns |
| `date`, `time`, `duration` | ISO 8601 forms | Neo4j temporal types |
| `uuid` | Lowercase hyphenated string |  |
| `bytes` | Base64 (RFC 4648, padded) |  |
| `json` | Embedded JSON value + flag | Avoids double encoding |
| `array<T>` | JSON array of encoded `T` | Neo4j lists are homogeneous |
| null vs absent | Distinct: `null` value vs property omitted | Neo4j cannot store null properties |

Every conversion that narrows precision or changes representation is a **lossy transformation** and follows the loss policy (section 5).

TS SDK maps `int64` to `bigint`; C# to `long`/`decimal`; Python to `int`/`Decimal`; Go to `int64`/a decimal library (e.g. `shopspring/decimal` or `cockroachdb/apd`). Document the choice per SDK.

### Byte-exact identity derivation

Stable entity identities, including composite keys, must be identical across languages and runs.

- Identity = `(entityType, ordered key fields)`.
- Canonical serialization: entity type, then key fields in mapping-declared order, each as `type-tag : length-prefixed UTF-8 of the canonical encoding`.
- The stored graph identity property is `rtg_id` = lowercase hex SHA-256 of that serialization (configurable to store the natural key instead).
- Never use language built-ins (`hash()`, `GetHashCode()`, `String()`).
- `identity.md` includes worked test vectors; every SDK and the engine test against them.

### Error codes

- Stable string codes: `RTG-<AREA>-<NNN>` (e.g. `RTG-MAP-012`, `RTG-EXEC-004`).
- Each code has: meaning, severity, whether retryable, remediation hint.
- SDKs expose the code on their exception or error types; they never invent codes.
- Codes are never reused or renumbered.

## 4. Engine layers (Go)

All layers live in the Go engine. SDKs never reimplement them.

### Engine technology stack

- Go 1.23+ (use the current stable release at implementation time)
- `pgx` v5 for PostgreSQL
- Official `neo4j-go-driver` v5
- `cobra` for the CLI
- `santhosh-tekuri/jsonschema` (or equivalent) for schema validation; `gopkg.in/yaml.v3` for YAML
- A decimal library for exact numerics
- `log/slog` for structured logging with secret redaction
- `testing` + `testify`, `rapid` for property-based tests, `testcontainers-go` for integration tests
- `golangci-lint`, `go vet`, `govulncheck`

Keep dependencies minimal; justify each one in `docs/dependencies.md`.

### Layer A: Database adapters

Define stable Go interfaces for relational and graph adapters. Keep all PostgreSQL- and Neo4j-specific behavior inside adapters.

**Relational adapter:** connection management; schema and table inspection; primary- and foreign-key discovery; column type discovery; paginated or keyset-based reading; batched inserts and upserts; transaction handling; capability reporting.

**Graph adapter:** connection management; label and relationship-type inspection where supported; node and relationship reading; batched node creation and updates (`UNWIND` + `MERGE`); relationship creation; stable identity resolution; constraint/index management for identity properties; transaction handling; capability reporting.

Schema inspection uses explicit `information_schema` and `pg_catalog` queries, not ORM reflection, and emits a document conforming to `schema-metadata.schema.json`. Every SDK therefore sees identical metadata.

### Layer B: Canonical intermediate representation

A versioned IR, independent of either database model, conforming to `ir.schema.json`. It represents:

- Entities, attributes and canonically encoded values
- Identifiers, including composite identifiers
- Relationships, direction and relationship properties
- Entity types and labels
- Nullability and type metadata
- Source provenance
- Transformation warnings, unsupported and lossy transformations

The IR must evolve without unnecessarily breaking compatibility (additive changes in minor versions).

### Layer C: Mapping engine

Mappings describe: source and destination; tables ↔ labels; primary-key and identity strategies; column ↔ property mappings; foreign key ↔ relationship mappings; relationship direction and names; type conversions; null handling; composite keys; junction-table handling; relationship properties; naming conventions; conflict policies; loss and unsupported-feature policies.

Load and validate mappings from JSON and YAML. Provide automatic mapping suggestions from schema metadata (`rtogtor infer`), which users review and override before use.

### Layer D: Planning engine

A deterministic planner that:

1. Inspects the source schema.
2. Inspects destination capabilities and relevant schema.
3. Loads and validates the mapping.
4. Identifies required transformations.
5. Detects unsupported or lossy operations.
6. Determines dependency order (nodes before relationships; parents before children for relational writes).
7. Identifies cycles and self-references.
8. Estimates work where reliable.
9. Produces a human-readable plan.
10. Produces a machine-readable plan conforming to `plan.schema.json`.

Plans are deterministic: the same inputs yield byte-identical canonical JSON. A dry run never modifies destination data.

### Layer E: Execution engine

A pipeline that reads source data incrementally, converts to IR, applies mapping rules and conversions, writes in batches, resolves identities, creates relationships, records checkpoints, retries transient failures per explicit policy, supports resumption, and generates a final report. Use bounded channels and worker pools; never load a full table or graph into memory.

### Layer F: Validation and reporting

Validate: record, entity and relationship counts; required properties; identity consistency; type conversion correctness; referential integrity; missing or duplicate entities; unsupported mappings; transformation losses; failed records; source-to-destination discrepancies.

Strictness levels are configurable. Reports are emitted as JSON (`report.schema.json`) and human-readable text.

## 5. Fidelity, safety and recovery

### Data fidelity and reversibility

Relational and graph models are not identical; RtoGtoR makes the differences explicit. Known cases:

- A table may become a node label.
- A foreign key may become a directed relationship.
- A junction table may become a relationship, or stay an entity when it carries business attributes.
- A node with multiple labels may not map cleanly to one table.
- Relationship properties may need a dedicated relational table.
- Composite identifiers need the stable representation in `identity.md`.
- Nulls, arrays, nested structures and database-specific types need explicit conversion rules (`values.md`).

Two loss policies, at minimum:

- **Strict (default):** rejects unsupported or lossy mappings unless each is explicitly allowed in the mapping.
- **Flexible:** permits configured lossy transformations and reports each effect.

Never silently discard data.

### Migration run model

Each run records, per `run-state.schema.json`:

- Unique run ID (ULID or UUIDv7)
- Source and destination metadata (no credentials)
- Mapping hash and `specVersion`
- Current stage
- Batch checkpoints (keyset cursor per source stream)
- Succeeded and failed record counts
- Error details with error codes
- Start and completion timestamps
- Resume information

Run state is stored in a local state directory by default (SQLite or JSON files), configurable to a table in the source or destination database. The same run can be resumed from any SDK because the state format is in the spec.

### Idempotency, retries and resume

- Graph writes use `MERGE` on the `rtg_id` identity property with a uniqueness constraint created before loading.
- Relational writes use `INSERT … ON CONFLICT` on mapped keys.
- Retry policies (max attempts, backoff, jitter, retryable error codes) are explicit and part of the mapping or run config.
- Failed records are written to an inspectable dead-letter store with the source key, error code and message.
- Resume restarts from the last committed checkpoint; replaying a batch must be safe.

### Guarantees

Do not claim distributed atomic transactions across two databases. Document actual guarantees per adapter and execution mode in `docs/recovery.md`: each batch is atomic within the destination; a run as a whole is at-least-once with idempotent writes, giving effectively-once results for supported mappings.

### Security

- Never write credentials to logs, reports, run state, plans or process arguments.
- Redact connection strings in all output.
- Do not run destructive operations against a non-disposable database without an explicit `--allow-destructive` flag and a confirmation.
- Sign release binaries and publish checksums and an SBOM.

## 6. SDK APIs and CLI

Each SDK exposes the same concepts — `Connection`, `Mapping`, `Migration`, `Plan`, `Report`, `RunState`, `RtoGtoRError` — in idiomatic form. The shapes below are design targets; implement coherent, typed APIs and document actual behavior.

### Go

```go
src := rtogtor.PostgreSQL(os.Getenv("SOURCE_URL"))
dst := rtogtor.Neo4j("bolt://localhost:7687", rtogtor.BasicAuthFromEnv("NEO4J"))
mapping, err := rtogtor.LoadMapping("mapping.yaml")
m := rtogtor.NewMigration(src, dst, mapping)
defer m.Close()
plan, err := m.Plan(ctx)
report, err := m.Run(ctx, rtogtor.RunOptions{DryRun: false})
```

### Python

```python
from rtogtor import Connection, Mapping, Migration

source = Connection.postgresql(url_env="SOURCE_URL")
destination = Connection.neo4j(uri="bolt://localhost:7687", auth_env="NEO4J")
mapping = Mapping.from_file("mapping.yaml")

with Migration(source=source, destination=destination, mapping=mapping) as m:
    plan = m.plan()
    report = m.run(dry_run=False)
```

### TypeScript

```typescript
import { Connection, Mapping, Migration } from "@rtogtor/sdk";

const mapping = await Mapping.fromFile("mapping.yaml");
await using m = new Migration({
  source: Connection.postgresql({ urlEnv: "SOURCE_URL" }),
  destination: Connection.neo4j({ uri: "bolt://localhost:7687", authEnv: "NEO4J" }),
  mapping,
});
const plan = await m.plan();
const report = await m.run({ dryRun: false });
```

### C#

```csharp
using RtoGtoR;

var mapping = await Mapping.FromFileAsync("mapping.yaml");
await using var m = new Migration(
    source: Connection.PostgreSql(urlEnv: "SOURCE_URL"),
    destination: Connection.Neo4j("bolt://localhost:7687", authEnv: "NEO4J"),
    mapping: mapping);
var plan = await m.PlanAsync(ct);
var report = await m.RunAsync(new RunOptions { DryRun = false }, ct);
```

### Requirements for every SDK

- Full type annotations / static types; nullable reference types enabled in C#; `strict` in TypeScript
- Errors carry the spec error code, severity and retryable flag
- Predictable resource cleanup; the engine process is terminated on dispose
- Cancellation support (`context.Context`, `CancellationToken`, `AbortSignal`, Python cancellation via `KeyboardInterrupt`/async tasks)
- Progress events exposed as callbacks, async iterators or events
- No connections or engine processes started at import time
- No secrets in logs, exceptions or reports
- Async-first in TypeScript and C#; sync API in Python with an optional async variant

### CLI

The Go engine provides the `rtogtor` executable. Every SDK package exposes the same CLI through its bundled binary (e.g. `python -m rtogtor`, `npx rtogtor`, `dotnet rtogtor` tool).

```bash
rtogtor inspect --source-env SOURCE_URL
rtogtor infer --source-config source.yaml --output mapping.yaml
rtogtor validate --mapping mapping.yaml
rtogtor plan --mapping mapping.yaml [--format json]
rtogtor migrate --mapping mapping.yaml --dry-run
rtogtor migrate --mapping mapping.yaml
rtogtor migrate --mapping mapping.yaml --resume RUN_ID
rtogtor verify --run-id RUN_ID
rtogtor report --run-id RUN_ID --format json
rtogtor failures --run-id RUN_ID
rtogtor version   # engine, spec and protocol versions
```

Consistent options, actionable errors with codes, safe defaults, useful help, and a machine-readable `--format json` on every command. Never print credentials.

## 7. Repository, testing and releases

### Monorepo structure

```text
RtoGtoR/
├── README.md  LICENSE  CONTRIBUTING.md  CODE_OF_CONDUCT.md
├── SECURITY.md  CHANGELOG.md  GOVERNANCE.md
├── docker-compose.yml
├── .github/workflows/          # path-filtered per component
├── specs/                      # source of truth (section 3)
│   ├── *.schema.json
│   ├── values.md  identity.md  error-codes.md  compatibility.md
│   └── proto/                  # gRPC, phase 2
├── engine/                     # Go module: library + CLI
│   ├── cmd/rtogtor/
│   ├── adapters/relational/postgresql/
│   ├── adapters/graph/neo4j/
│   ├── ir/  mapping/  planning/  execution/
│   ├── validation/  reporting/  runstate/  protocol/
│   └── internal/
├── sdks/
│   ├── python/                 # src/rtogtor, tests, pyproject.toml
│   ├── typescript/             # src, tests, package.json
│   └── dotnet/                 # src/RtoGtoR, tests, RtoGtoR.sln
├── conformance/
│   ├── cases/<case-name>/
│   │   ├── source.sql | source.cypher
│   │   ├── mapping.yaml
│   │   ├── expected-plan.json
│   │   ├── expected-destination.json
│   │   └── expected-report.json
│   ├── vectors/                # identity and value-encoding vectors
│   └── runner-protocol.md
├── examples/ecommerce/         # same example in all four languages
├── tools/codegen/              # schema → types for each SDK
└── docs/
    ├── architecture.md  adr/  mapping-guide.md  migration-guide.md
    ├── recovery.md  limitations.md  roadmap.md
    └── sdk/go.md  python.md  typescript.md  dotnet.md
```

Go import path `github.com/TuanFaied/rtogtor/engine`; Python package `rtogtor`; npm scope `@rtogtor`; NuGet ID `RtoGtoR`. Display name remains **RtoGtoR**.

Because the Go module lives in `engine/`, engine releases are tagged `engine/vX.Y.Z`; the public package is declared `package rtogtor` so callers write `rtogtor.NewMigration(...)` (see ADR-0002).

### Test layers

| Layer | Where | What |
| --- | --- | --- |
| Unit | engine + each SDK | Mapping validation, schema interpretation, IR, type conversion, planning, reporting, errors |
| Integration | engine | PostgreSQL and Neo4j via Docker Compose / testcontainers |
| Round-trip | engine | Relational → graph → relational; compare normalized data |
| Fault injection | engine | Interrupted runs, transient errors, duplicates, invalid mappings, failures between batches |
| Property-based | engine (`rapid`), Python (Hypothesis), TS (`fast-check`), C# (FsCheck) | Identity, value encoding, mapping invariants, round trips |
| Conformance | every SDK | Shared `conformance/cases` and `vectors`, run through each SDK's public API |
| Protocol | each SDK | Engine version handshake, error mapping, cancellation, process cleanup |

For mappings that cannot round-trip losslessly, tests verify that the planner or report names each limitation.

### Conformance suite

- Cases are language-neutral data: input fixture, mapping, expected plan, expected destination snapshot, expected report and warnings.
- Outputs are compared as **canonical JSON** (sorted keys, stable array ordering defined per document type, canonical value encoding).
- Each SDK ships a small harness that runs every case through its public API and emits results per `runner-protocol.md`.
- A feature is "supported in language X" only when X passes all its cases.

### Acceptance criteria

- PostgreSQL → Neo4j and Neo4j → PostgreSQL integration tests pass.
- Composite keys, self-references and supported relationship patterns are tested.
- Unsupported transformations fail clearly with error codes or follow an explicit user policy.
- Dry-run makes no destination changes (verified by test).
- Large datasets run in bounded memory (verified by a memory-ceiling test).
- Interrupted runs resume safely.
- Validation reports explain discrepancies.
- Every released SDK passes the full conformance suite.
- Documentation examples compile and run in CI for all four languages.

### CI

- Path-filtered workflows: `specs/**` changes run everything; `sdks/typescript/**` runs only TS plus conformance.
- Per component: format, lint, type check, unit tests. Go: `gofmt`, `golangci-lint`, `govulncheck`. Python: Ruff, mypy, pytest. TS: ESLint, Prettier, `tsc --noEmit`, Vitest. C#: `dotnet format`, analyzers as errors, xUnit.
- A codegen check fails when generated types drift from schemas.
- One shared integration job with PostgreSQL and Neo4j services; conformance runs against the freshly built engine binary.
- Cross-compile the engine for all target platforms on every main-branch build.

### Versioning and releases

- `specVersion`, `protocolVersion`, engine and each SDK are versioned independently with semver.
- `specs/compatibility.md` lists which engine and SDK versions support which spec and protocol versions.
- Release automation with release-please (or equivalent) per component; Renovate or Dependabot for all four ecosystems.
- Publish to PyPI, npm and NuGet with trusted publishing / OIDC where available; sign artifacts; publish SBOMs.
- Reserve package names on all registries before the first public commit.
- License: Apache-2.0 unless a documented reason requires another permissive license.

## 8. Milestones, future work and agent instructions

Work in milestones and keep the project runnable after each one. A language is announced as supported only after it passes the conformance suite.

### Milestones

1. **Foundation** — monorepo, licensing and community files; `specs/` v0.1 (mapping, IR, values, identity, error codes, schema metadata); codegen pipeline; Go engine skeleton with adapter interfaces; PostgreSQL schema inspection; first conformance cases and vectors; CI.
2. **PostgreSQL → Neo4j vertical slice (Go engine + CLI)** — explicit mappings, planning and dry run, batch execution, identity resolution, relationships, verification and reports, using the e-commerce example.
3. **Engine protocol + first SDK (C#)** — JSON-lines protocol v1, binary bundling for NuGet, C# SDK passing all conformance cases.
4. **Python and TypeScript SDKs** — wheels and npm per-platform packages, both passing conformance.
5. **Neo4j → PostgreSQL** — graph inspection, reverse mapping, relational schema planning, reverse execution, round-trip tests and loss reporting.
6. **Production hardening** — checkpoints and resumption across SDKs, retry policies, fault injection, validation improvements, performance and memory tests, security review.
7. **Public release 1.0** — CLI polish, docs and examples in all four languages, release automation, compatibility matrix.

### First vertical slice: e-commerce

A small PostgreSQL database with customers, products, orders and order items, foreign keys between them, and at least one composite key (e.g. `order_items (order_id, line_no)`) plus a self-reference (e.g. `products.parent_product_id` or `customers.referred_by`).

The slice must: inspect the schema; validate a user-provided mapping; show a dry-run plan; migrate in batches; create relationships correctly; resume safely after an injected failure; verify the resulting graph; produce a report. Then implement the reverse direction with explicit mappings, without claiming universal losslessness.

### Later milestones

- **Additional adapters** (MySQL, SQL Server, other graph databases) behind the same interfaces.
- **Runtime interoperability:** fetch by stable identity, create/update, delete per explicit policy, bounded relationship traversal, native SQL/Cypher escape hatches, a documented subset of translated operations. This runs inside application processes, so it is the one area where native per-language implementations are justified; they must still follow the spec and pass a runtime conformance suite. Never claim SQL and Cypher are interchangeable.
- **Continuous synchronization:** change data capture (evaluate Debezium and PostgreSQL logical replication), inserts/updates/deletes, tombstones, ordering, duplicate events, conflict resolution, dead letters, reconciliation, metrics. Do not advertise bidirectional real-time sync until conflict semantics, delivery guarantees and recovery are implemented and tested.
- **gRPC server mode** for the engine.
- **Optional AI-assisted mapping suggestions**, never required for core operation.

### Instructions for the coding agent

Before implementation:

1. Inspect the existing repository and preserve useful code.
2. Write ADR-0001 (single Go engine + thin SDKs) and any other significant decisions in `docs/adr/`.
3. Produce a concise implementation plan with concrete acceptance criteria for the current milestone.
4. List any ambiguities that would materially affect data integrity, and ask before resolving them by assumption.

During implementation:

- Work in small, verifiable increments; keep everything runnable.
- Change the spec first, then regenerate types, then implement. Never let an SDK define behavior the spec lacks.
- Implement real functionality, not placeholder success responses.
- Do not skip or silence failing tests.
- Do not claim support for features or languages that have not passed their tests.
- Avoid unnecessary abstractions, dependencies and premature optimization.
- Ship tests and documentation with every meaningful feature.
- Never hardcode credentials.
- Never run destructive migrations against a real database without explicit authorization; use disposable local containers.

At the end of each milestone, report:

- Files created or modified
- Features implemented, per component and language
- Tests executed and their actual results (including conformance pass/fail per SDK)
- Known limitations
- Security and data-integrity considerations
- Exact commands to build, test and run each component
- Recommended next milestone

**Start with Milestone 1, then implement the PostgreSQL → Neo4j vertical slice in the Go engine. Prioritize correctness, recoverability, data fidelity and a clean, language-neutral specification over the number of features or languages.**
