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

## Milestone 2 — PostgreSQL → Neo4j vertical slice *(implemented on branch `milestone-2/pg-to-neo4j`)*

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

## Milestone 3 — Engine protocol + C# SDK

Codegen pipeline (schemas → types, with a CI drift check; see ADR-0004),
JSON-lines protocol v1 (`protocol.schema.json`), NuGet packaging with bundled
binaries, C# SDK passing all conformance cases.

## Milestone 4 — Python and TypeScript SDKs

PyPI wheels and npm per-platform packages, both passing conformance.

## Milestone 5 — Neo4j → PostgreSQL

Graph inspection, reverse mappings, relational schema planning, reverse
execution, round-trip tests and loss reporting.

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
