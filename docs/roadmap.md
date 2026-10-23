# Roadmap

Each milestone leaves the project runnable. A language SDK is announced as
supported only after it passes the full conformance suite.

## Milestone 1 — Foundation *(complete)*

| Step | Status |
|---|---|
| 1. Repository, license, community files | ✅ Done |
| 2. Specification v0.1 drafts: values, identity + vectors, error codes, mapping / schema-metadata / IR schemas; ADRs 0001–0003 | ✅ Done (draft) |
| 3. Go engine skeleton: canonical values, `rtg_id`, mapping validation, adapter interfaces, CLI `version` / `validate` | ✅ Done |
| 4. `docker-compose.yml` with PostgreSQL and Neo4j; e-commerce fixture loading | ✅ Done |
| 5. PostgreSQL schema inspection (`rtogtor inspect`) + integration tests | ✅ Done |
| 6. CI workflows (test, lint, vulnerabilities, integration, cross-compile) | ✅ Done |
| 6b. Codegen pipeline | ➡️ Moved to Milestone 3 ([ADR-0004](adr/0004-defer-codegen-to-first-sdk.md)) |

**Done when:** `rtogtor inspect` prints valid metadata for the e-commerce
database, the identity and value vectors pass, and CI is green.

## Milestone 2 — PostgreSQL → Neo4j vertical slice *(implemented)*

| Item | Status |
|---|---|
| `plan`, `report`, `run-state` schemas; error codes for planning, execution, run state, verification | ✅ |
| Deterministic planner with loss policy, type and foreign-key checks (`rtogtor plan`) | ✅ |
| Neo4j adapter: identity constraints, `UNWIND`/`MERGE` batches, missing-endpoint detection | ✅ |
| Batched execution with checkpoints, failed-records store, retries, resume (`rtogtor migrate`) | ✅ |
| Dry run that writes nothing (tested) | ✅ |
| Verification (`rtogtor verify`), reports, `rtogtor failures` | ✅ |
| Resume after injected failures at every crash point (tested) | ✅ |
| Bounded-memory test (150 000 × 2 KiB rows, heap < 96 MiB) | ✅ |
| Junction-table relationships, natural identity, `time`/`interval` types | ⏳ See [limitations](limitations.md) |

## Milestone 3 — Engine protocol + C# SDK *(implemented)*

| Item | Status |
|---|---|
| Go public API (`Migration`, `Connection`, `Plan`, `Run`, `Verify`) shared by the CLI and the protocol server | ✅ |
| Protocol 1.0: `specs/protocol.md`, `protocol.schema.json`, `rtogtor serve --stdio`, error codes RTG-PROTO-002…008 | ✅ |
| Codegen pipeline with CI drift check ([ADR-0005](adr/0005-own-schema-code-generator.md)) | ✅ (C#; Python/TS emitters in M4) |
| C# SDK: `Migration`, `Engine`, `Mapping`, `Connection`, `RtoGtoRException`, generated model | ✅ |
| C# conformance: identity and value vectors, e-commerce plan case | ✅ all pass |
| NuGet package with engines for 5 platforms, smoke-tested as an installed package | ✅ (not yet published) |
| Conformance runner protocol (`conformance/runner-protocol.md`) | ✅ |

## Milestone 4 — Python and TypeScript SDKs *(implemented on branch `milestone-4/python-typescript`)*

| Item | Status |
|---|---|
| Codegen emitters: Pydantic v2 models (Python) and interfaces (TypeScript); schemas shipped with the generated code | ✅ |
| Python SDK: `Migration`, `AsyncMigration`, `Engine`, `Mapping`, `Connection`, `RtoGtoRError`; Ctrl-C cancels | ✅ |
| TypeScript SDK: `Migration`, `Engine`, `Mapping`, `Connection`, `RtoGtoRError`; `AbortSignal`, `await using` | ✅ |
| Conformance (identity and value vectors, e-commerce plan case) through both SDKs | ✅ all pass |
| Property tests: engine identities match independent implementations (Hypothesis, fast-check) | ✅ |
| Five platform wheels; npm main package plus five platform packages; both smoke-tested as installed packages | ✅ (not yet published) |
| CI for both SDKs (3 OSes, lint, types, tests, integration, packaging) | ✅ |

## Milestone 5 — Neo4j → PostgreSQL *(implemented on branch `milestone-5/neo4j-to-postgres`)*

| Item | Status |
|---|---|
| Semantics decided and recorded ([ADR-0006](adr/0006-graph-to-relational-semantics.md)) | ✅ |
| Graph inspection in the CLI and every SDK (`inspect` on a Neo4j URL) | ✅ |
| Reverse planning: graph and destination checks, unique keys, topological order, deferred self-references and cycles, optional `CREATE TABLE` DDL | ✅ |
| Reverse execution: key-ordered resumable reads, idempotent upserts, per-row isolation of rejected rows | ✅ |
| Per-value precision checks (`RTG-EXEC-007`) and loss reporting | ✅ |
| Reverse verification | ✅ |
| Round-trip tests PostgreSQL → Neo4j → PostgreSQL (existing and created tables), resume, graph shapes that do not fit tables | ✅ |
| Reverse plan conformance case through Go, C#, Python and TypeScript | ✅ all pass |

## Milestone 6 — Production hardening

Resumption across SDKs, retry policies, fault injection, performance and
memory tests, security review.

## Milestone 7 — Public release 1.0

CLI polish, docs and examples in all four languages, release automation,
compatibility matrix.

## Later

Additional adapters (MySQL, SQL Server, other graph databases), runtime
interoperability, continuous synchronization (CDC), gRPC server mode,
optional AI-assisted mapping suggestions.
