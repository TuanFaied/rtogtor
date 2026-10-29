# RtoGtoR

**Relational ↔ Graph transformation, done explicitly and safely.**

RtoGtoR is an open-source engine for moving data between relational and graph
databases — **PostgreSQL** and **Neo4j**, in both directions — with SDKs for
**Go, Python, TypeScript and C#**.

> **Status: 1.0 release candidate.** Every capability below is implemented and
> tested; packages are not published to the registries yet
> ([releasing](docs/releasing.md)). See [Capabilities](#capabilities) and the
> [known limitations](docs/limitations.md).

## Why RtoGtoR

- **Explicit mappings.** You decide how tables, keys and foreign keys become
  labels, nodes and relationships — and back.
- **Plan before you write.** Inspect and validate a plan before anything touches
  the destination.
- **Fidelity first.** Lossy conversions are rejected unless you allow them, and
  every allowed loss is reported. Nothing is silently discarded.
- **Safe to re-run.** Stable, content-derived identities (`rtg_id`) make writes
  idempotent, so interrupted runs can resume.
- **One engine, many languages.** Migration logic lives once, in a Go engine.
  The SDKs are thin, typed clients that all pass the same conformance suite.

## Capabilities

| Capability | Status |
|---|---|
| Specification 1.0 (values, identity, error codes, mapping, metadata, plan, report, run state, protocol); 0.1 documents still accepted ([compatibility](specs/compatibility.md)) | ✅ |
| Canonical value encoding (Go engine) | ✅ Implemented, tested against shared vectors |
| `rtg_id` identity derivation (Go engine) | ✅ Implemented, tested against shared vectors |
| Mapping loading + validation (JSON/YAML) | ✅ Implemented (schema + semantic checks) |
| CLI: `rtogtor version`, `validate`, `inspect` (PostgreSQL and Neo4j), `scaffold` (draft mappings) | ✅ Implemented |
| Adapter interfaces (relational, graph) | 🟡 Defined, unstable |
| PostgreSQL schema inspection (`rtogtor inspect`) | ✅ Implemented, integration-tested |
| PostgreSQL → Neo4j migration: `plan`, `migrate` (`--dry-run`, `--resume`), `verify`, `report`, `failures` | ✅ Implemented, end-to-end tested |
| Neo4j → PostgreSQL migration: same commands, `direction: graph-to-relational` | ✅ Implemented, round-trip tested ([guide](docs/migration-guide.md#the-other-way-neo4j--postgresql)) |
| Go SDK (the engine library, `github.com/TuanFaied/rtogtor/engine`) | ✅ Implemented ([docs](docs/sdk/go.md)) |
| Engine ↔ SDK protocol 1.0 (`rtogtor serve --stdio`) | ✅ Implemented, schema-validated and fuzzed in tests |
| Recovery: resumable runs, explicit retry policies, fault-injection tests in every SDK, value-level verification | ✅ Implemented ([recovery](docs/recovery.md), [security](docs/security.md), [performance](docs/performance.md)) |
| C# SDK (`RtoGtoR` on NuGet, bundles the engine for 5 platforms) | ✅ Implemented, passes the conformance suite; not yet published ([docs](docs/sdk/dotnet.md)) |
| Python SDK (`rtogtor` on PyPI, platform wheels with the engine) | ✅ Implemented, passes the conformance suite; not yet published ([docs](docs/sdk/python.md)) |
| TypeScript SDK (`@rtogtor/sdk` on npm, per-platform engine packages) | ✅ Implemented, passes the conformance suite; not yet published ([docs](docs/sdk/typescript.md)) |

✅ implemented · 🟡 partial/draft · ⏳ planned. A language SDK is listed as
supported only after it passes the full conformance suite.

## Quick start (engine, from source)

Requires Go 1.25 or newer.

```bash
cd engine
go build -o bin/rtogtor ./cmd/rtogtor
./bin/rtogtor version
./bin/rtogtor validate --mapping ../examples/ecommerce/mapping.yaml
```

Then follow the [migration guide](docs/migration-guide.md), or run the
e-commerce round trip in your language:
[Go](examples/ecommerce/go/main.go), [C#](examples/ecommerce/dotnet/Program.cs),
[Python](examples/ecommerce/python/main.py),
[TypeScript](examples/ecommerce/typescript/main.ts)
([how](examples/ecommerce/README.md)).

## Repository layout

| Path | Contents |
|---|---|
| [`specs/`](specs/) | The language-neutral specification — the source of truth |
| [`engine/`](engine/) | Go engine: library (`github.com/TuanFaied/rtogtor/engine`) and CLI |
| [`sdks/dotnet/`](sdks/dotnet/), [`sdks/python/`](sdks/python/), [`sdks/typescript/`](sdks/typescript/) | SDKs |
| [`tools/codegen/`](tools/codegen/) | Generates SDK types from `specs/` |
| [`conformance/`](conformance/) | Shared test vectors and cases every SDK must pass |
| [`examples/`](examples/) | Example schema, mappings, and the round trip in all four languages |
| [`docs/`](docs/) | Architecture decisions, guides, full project specification |

## Documentation

- [Project specification](docs/specification.md)
- [Migration guide](docs/migration-guide.md) — PostgreSQL → Neo4j and back, step by step
- [Recovery and guarantees](docs/recovery.md)
- [Known limitations](docs/limitations.md)
- [Performance and memory](docs/performance.md)
- [Security review](docs/security.md)
- [Releasing](docs/releasing.md) and the [compatibility matrix](specs/compatibility.md)
- [Development guide](docs/development.md) — local databases, tests, CI
- [Architecture decision records](docs/adr/)
- [Roadmap](docs/roadmap.md)
- [Specification index](specs/README.md)

## Contributing

Contributions are welcome — please read [CONTRIBUTING.md](CONTRIBUTING.md) and
our [Code of Conduct](CODE_OF_CONDUCT.md). To report a security issue, see
[SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE)
