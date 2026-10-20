# RtoGtoR

**Relational ↔ Graph transformation, done explicitly and safely.**

RtoGtoR is an open-source engine for moving data between relational and graph
databases — starting with **PostgreSQL** and **Neo4j** — with SDKs planned for
**Go, Python, TypeScript and C#**.

> **Status: pre-alpha (Milestone 1 — Foundation).** Nothing here is ready for
> production data yet. See [Capabilities](#capabilities) for exactly what works.

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
| Specification v0.1 (values, identity, error codes, mapping/metadata/IR schemas) | 🟡 Draft |
| Canonical value encoding (Go engine) | ✅ Implemented, tested against shared vectors |
| `rtg_id` identity derivation (Go engine) | ✅ Implemented, tested against shared vectors |
| Mapping loading + validation (JSON/YAML) | ✅ Implemented (schema + semantic checks) |
| CLI: `rtogtor version`, `rtogtor validate` | ✅ Implemented |
| Adapter interfaces (relational, graph) | 🟡 Defined, unstable |
| PostgreSQL schema inspection (`rtogtor inspect`) | ⏳ Planned (Milestone 1) |
| PostgreSQL → Neo4j migration | ⏳ Planned (Milestone 2) |
| Neo4j → PostgreSQL migration | ⏳ Planned (Milestone 5) |
| Python / TypeScript / C# SDKs | ⏳ Planned (Milestones 3–4) |

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

## Repository layout

| Path | Contents |
|---|---|
| [`specs/`](specs/) | The language-neutral specification — the source of truth |
| [`engine/`](engine/) | Go engine: library (`github.com/TuanFaied/rtogtor/engine`) and CLI |
| [`conformance/`](conformance/) | Shared test vectors and cases every SDK must pass |
| [`examples/`](examples/) | Example schemas and mappings |
| [`docs/`](docs/) | Architecture decisions, guides, full project specification |

## Documentation

- [Project specification](docs/specification.md)
- [Architecture decision records](docs/adr/)
- [Roadmap](docs/roadmap.md)
- [Specification index](specs/README.md)

## Contributing

Contributions are welcome — please read [CONTRIBUTING.md](CONTRIBUTING.md) and
our [Code of Conduct](CODE_OF_CONDUCT.md). To report a security issue, see
[SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE)
