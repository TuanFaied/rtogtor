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

## Milestone 3 — Engine protocol + C# SDK *(implemented on branch `milestone-3/protocol-csharp`)*

| Item | Status |
|---|---|
| Go public API (`Migration`, `Connection`, `Plan`, `Run`, `Verify`) shared by the CLI and the protocol server | ✅ |
| Protocol 1.0: `specs/protocol.md`, `protocol.schema.json`, `rtogtor serve --stdio`, error codes RTG-PROTO-002…008 | ✅ |
| Codegen pipeline with CI drift check ([ADR-0005](adr/0005-own-schema-code-generator.md)) | ✅ (C#; Python/TS emitters in M4) |
| C# SDK: `Migration`, `Engine`, `Mapping`, `Connection`, `RtoGtoRException`, generated model | ✅ |
| C# conformance: identity and value vectors, e-commerce plan case | ✅ all pass |
| NuGet package with engines for 5 platforms, smoke-tested as an installed package | ✅ (not yet published) |
| Conformance runner protocol (`conformance/runner-protocol.md`) | ✅ |

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
