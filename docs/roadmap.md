# Roadmap

Each milestone leaves the project runnable. A language SDK is announced as
supported only after it passes the full conformance suite.

## Milestone 1 — Foundation *(in progress)*

| Step | Status |
|---|---|
| 1. Repository, license, community files | ✅ Done |
| 2. Specification v0.1 drafts: values, identity + vectors, error codes, mapping / schema-metadata / IR schemas; ADRs 0001–0003 | ✅ Done (draft) |
| 3. Go engine skeleton: canonical values, `rtg_id`, mapping validation, adapter interfaces, CLI `version` / `validate` | ✅ Done |
| 4. `docker-compose.yml` with PostgreSQL and Neo4j; e-commerce fixture loading | ⏳ Next |
| 5. PostgreSQL schema inspection (`rtogtor inspect`) + integration tests | ⏳ |
| 6. Codegen pipeline (schemas → Go/Python/TS/C# types) and CI workflows | ⏳ |

**Done when:** `rtogtor inspect` prints valid metadata for the e-commerce
database, the identity and value vectors pass, and CI is green.

## Milestone 2 — PostgreSQL → Neo4j vertical slice

Planner (`rtogtor plan`, dry run), Neo4j adapter with identity constraints,
batched execution with checkpoints, resume after injected failure, `verify`,
reports, bounded-memory test. Adds `plan`, `report` and `run-state` schemas.

## Milestone 3 — Engine protocol + C# SDK

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
