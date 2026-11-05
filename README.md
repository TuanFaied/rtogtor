<div align="center">

# RtoGtoR

**Relational ↔ Graph data migration — explicit, verifiable, and safe to re-run.**

Move data between **PostgreSQL** and **Neo4j**, in both directions, from the CLI
or from **Go, Python, TypeScript and C#**.

[![engine](https://github.com/TuanFaied/rtogtor/actions/workflows/engine.yml/badge.svg)](https://github.com/TuanFaied/rtogtor/actions/workflows/engine.yml)
[![python](https://github.com/TuanFaied/rtogtor/actions/workflows/python.yml/badge.svg)](https://github.com/TuanFaied/rtogtor/actions/workflows/python.yml)
[![typescript](https://github.com/TuanFaied/rtogtor/actions/workflows/typescript.yml/badge.svg)](https://github.com/TuanFaied/rtogtor/actions/workflows/typescript.yml)
[![dotnet](https://github.com/TuanFaied/rtogtor/actions/workflows/dotnet.yml/badge.svg)](https://github.com/TuanFaied/rtogtor/actions/workflows/dotnet.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

[Quick start](#quick-start) ·
[How it works](#how-it-works) ·
[SDKs](#sdks) ·
[Documentation](#documentation) ·
[Contributing](#contributing)

</div>

---

> [!NOTE]
> **Status: 1.0 release candidate.** Every capability listed below is
> implemented and tested. The SDK packages are built and release-automated but
> **not yet published** to PyPI, npm or NuGet — see [releasing](docs/releasing.md)
> and the [known limitations](docs/limitations.md).

## Table of contents

- [Why RtoGtoR](#why-rtogtor)
- [Features](#features)
- [How it works](#how-it-works)
- [Quick start](#quick-start)
- [Mappings](#mappings)
- [CLI workflow](#cli-workflow)
- [SDKs](#sdks)
- [Supported platforms](#supported-platforms)
- [Repository layout](#repository-layout)
- [Documentation](#documentation)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [Security](#security)
- [License](#license)

## Why RtoGtoR

Moving data between a relational schema and a property graph is easy to do
*almost* right. Implicit conventions guess wrong, lossy type conversions slip
through, and a run that dies halfway leaves duplicates behind. RtoGtoR is built
around the opposite defaults:

- **Explicit mappings.** You decide how tables, keys and foreign keys become
  labels, nodes and relationships — and how they come back.
- **Plan before you write.** Every migration produces an inspectable plan,
  checked against the live schema, before anything touches the destination.
- **Fidelity first.** Lossy conversions are rejected unless a policy allows
  them with a stated reason, and every allowed loss is reported. Nothing is
  silently discarded.
- **Safe to re-run.** Stable, content-derived identities (`rtg_id`) make writes
  idempotent, so interrupted runs resume without duplicates.
- **Verifiable.** After a run, compare counts — or every single value — on both
  sides.
- **One engine, many languages.** Migration logic lives once, in a Go engine.
  The SDKs are thin, typed clients that all pass the same conformance suite, so
  they behave identically.

## Features

| Capability | Status |
|---|---|
| PostgreSQL → Neo4j: `plan`, `migrate` (`--dry-run`, `--resume`), `verify`, `report`, `failures` | ✅ End-to-end tested |
| Neo4j → PostgreSQL (`direction: graph-to-relational`), same commands | ✅ Round-trip tested |
| Schema inspection (`rtogtor inspect`) for PostgreSQL and Neo4j | ✅ Integration-tested |
| Draft mapping generation (`rtogtor scaffold`, `scaffold --reverse`) | ✅ |
| Mapping loading and validation, JSON or YAML (schema + semantic checks) | ✅ |
| Canonical value encoding and `rtg_id` identity derivation | ✅ Tested against shared vectors |
| Resumable runs, explicit retry policies, value-level verification | ✅ Fault-injection tested in every SDK |
| Engine ↔ SDK protocol 1.0 (`rtogtor serve --stdio`) | ✅ Schema-validated and fuzzed |
| Specification 1.0 (0.1 documents still accepted — [compatibility](specs/compatibility.md)) | ✅ |
| Go, Python, TypeScript and C# SDKs | ✅ Pass the conformance suite; registry packages not yet published |
| Adapter interfaces for additional databases | 🟡 Defined, unstable |

✅ implemented · 🟡 partial/draft · ⏳ planned. A language SDK is listed as
supported only after it passes the full conformance suite.

## How it works

```text
            ┌──────────────┐     inspect      ┌──────────────┐
            │  PostgreSQL  │ ◄──────────────► │    Neo4j     │
            └──────┬───────┘                  └──────▲───────┘
                   │ read                            │ write (idempotent, batched)
                   ▼                                 │
  mapping.yaml ─► validate ─► plan ─► migrate ─► verify ─► report
                                         │
                                  run state (resume)
```

1. **Inspect** the source schema: tables, columns, logical types, keys and
   foreign keys — unsupported types are flagged up front.
2. **Map** it explicitly, starting from a generated draft if you like.
3. **Plan** — the mapping is checked against the live schema and every step and
   every lossy transformation is listed, with what allowed it.
4. **Migrate** in batches, with a dry-run mode that reads and converts every row
   but writes nothing.
5. **Verify** and **report** — counts or full value comparison, plus a record of
   any failed rows.

The SDKs drive the same engine over a versioned JSON protocol, so a migration
written in Python behaves exactly like one run from the CLI.

## Quick start

### Prerequisites

- [Go](https://go.dev/dl/) 1.25 or newer (to build the engine and CLI)
- [Docker](https://docs.docker.com/get-docker/) with Compose v2 (for the local
  example databases)

### 1. Build the CLI

```bash
git clone https://github.com/TuanFaied/rtogtor.git
cd rtogtor/engine
go build -o bin/rtogtor ./cmd/rtogtor
./bin/rtogtor version
```

### 2. Start the example databases

From the repository root, start disposable PostgreSQL 17 and Neo4j 5
containers. PostgreSQL loads the [e-commerce example](examples/ecommerce/schema.sql)
on first start.

```bash
cp .env.example .env    # then choose local-only passwords
docker compose up -d --wait
```

### 3. Migrate PostgreSQL → Neo4j

Connection URLs are read **only from environment variables you name** — never
from command-line flags — so they never appear in process lists or shell
history.

```bash
set -a; source .env; set +a
export SOURCE_URL="postgres://rtogtor:${POSTGRES_PASSWORD}@127.0.0.1:5432/ecommerce?sslmode=disable"
export NEO4J_URL="neo4j://neo4j:${NEO4J_PASSWORD}@127.0.0.1:7687"

cd engine
./bin/rtogtor validate --mapping ../examples/ecommerce/mapping.yaml
./bin/rtogtor plan     --mapping ../examples/ecommerce/mapping.yaml --source-env SOURCE_URL
./bin/rtogtor migrate  --mapping ../examples/ecommerce/mapping.yaml --source-env SOURCE_URL --dest-env NEO4J_URL
```

Next, follow the full [migration guide](docs/migration-guide.md) — including
[the reverse direction](docs/migration-guide.md#the-other-way-neo4j--postgresql)
— or run the complete e-commerce round trip in your language
([how](examples/ecommerce/README.md)).

## Mappings

A mapping is a JSON or YAML document that states exactly how the two models
correspond. An excerpt from [`examples/ecommerce/mapping.yaml`](examples/ecommerce/mapping.yaml):

```yaml
specVersion: "1.0.0"
name: ecommerce
direction: relational-to-graph

source:      { kind: postgresql, schema: public }
destination: { kind: neo4j }

identity:
  strategy: hash
  property: rtg_id

policies:
  loss: strict
  allow:
    - kind: representation
      type: decimal
      reason: Neo4j has no exact decimal type; prices are stored as canonical decimal strings.

entities:
  - name: Customer
    table: customers
    labels: [Customer]
    key: [id]
    properties:
      - { column: id,        property: id,       type: int64 }
      - { column: email,     property: email,    type: string }
      - { column: full_name, property: fullName, type: string }
```

- **entities** map a table to nodes: labels, the identity key and properties.
- **relationships** map a foreign key to a relationship type and direction.
- **policies** decide what happens to lossy conversions. Under the default
  `strict` policy, each loss needs an explicit `allow` rule with a reason.

Don't want to write one from scratch? `rtogtor scaffold --source-env SOURCE_URL`
drafts a mapping from the live schema, with a comment on every choice it made.
The full format is defined in the [specification](specs/README.md).

## CLI workflow

| Command | What it does |
|---|---|
| `rtogtor inspect` | Lists the source schema and flags anything unsupported |
| `rtogtor scaffold` | Drafts a mapping from an inspected schema (`--reverse` for the way back) |
| `rtogtor validate` | Checks a mapping against the schema and semantic rules |
| `rtogtor plan` | Builds the migration plan against the live schema; writes nothing |
| `rtogtor migrate` | Runs the migration (`--dry-run`, `--resume`) |
| `rtogtor verify` | Compares source and destination, down to individual values |
| `rtogtor report`, `rtogtor failures` | Show a run's report and its failed records |
| `rtogtor serve --stdio` | Runs the engine as a protocol server for the SDKs |

`migrate` exit codes: `0` everything migrated · `3` finished with failed
records · `1` failed or interrupted (resumable) · `2` usage error.

## SDKs

All SDKs expose the same model — `Connection`, `Mapping`, `Migration` with
`plan`, `run` and `verify` — with stable error codes, retry policies,
cancellation and resume support. Credentials are always read from environment
variables and never logged or sent in protocol messages.

| Language | Package | Requirements | Docs |
|---|---|---|---|
| Go | `github.com/TuanFaied/rtogtor/engine` (in-process) | Go 1.25+ | [docs/sdk/go.md](docs/sdk/go.md) |
| Python | `rtogtor` (PyPI, platform wheels with the engine) | — | [docs/sdk/python.md](docs/sdk/python.md) |
| TypeScript | `@rtogtor/sdk` (npm, per-platform engine packages) | Node.js 22+, ESM | [docs/sdk/typescript.md](docs/sdk/typescript.md) |
| C# / .NET | `RtoGtoR` (NuGet, engine bundled) | .NET 8+ | [docs/sdk/dotnet.md](docs/sdk/dotnet.md) |

<details open>
<summary><b>Python</b></summary>

```python
from rtogtor import Connection, Mapping, Migration

source = Connection.postgresql(url_env="SOURCE_URL")
destination = Connection.neo4j(uri="bolt://localhost:7687", auth_env="NEO4J")
mapping = Mapping.from_file("mapping.yaml")

with Migration(source=source, destination=destination, mapping=mapping) as m:
    plan = m.plan()                                       # nothing is written
    report = m.run(dry_run=False)
    verification = m.verify(report.run_id, values=True)  # every value, both sides
```

</details>

<details>
<summary><b>TypeScript</b></summary>

```ts
import { Connection, Mapping, Migration } from "@rtogtor/sdk";

const mapping = await Mapping.fromFile("mapping.yaml");
await using m = new Migration({
  source: Connection.postgresql({ urlEnv: "SOURCE_URL" }),
  destination: Connection.neo4j({ uri: "bolt://localhost:7687", authEnv: "NEO4J" }),
  mapping,
});

const plan = await m.plan();                                // nothing is written
const report = await m.run({ dryRun: false });
const verification = await m.verify(report.runId, { values: true });
```

</details>

<details>
<summary><b>C#</b></summary>

```csharp
using RtoGtoR;

var mapping = await Mapping.FromFileAsync("mapping.yaml");
await using var m = new Migration(
    source: Connection.PostgreSql(urlEnv: "SOURCE_URL"),
    destination: Connection.Neo4j("neo4j://localhost:7687", authEnv: "NEO4J"),
    mapping: mapping);

var plan = await m.PlanAsync(ct);                       // nothing is written
var report = await m.RunAsync(new RunOptions { DryRun = false }, ct);
var verification = await m.VerifyAsync(report.RunId, new VerifyOptions { Values = true }, ct);
```

</details>

<details>
<summary><b>Go</b></summary>

```go
import "github.com/TuanFaied/rtogtor/engine"

src := rtogtor.PostgreSQLFromEnv("SOURCE_URL")
dst := rtogtor.Neo4j("neo4j://localhost:7687", rtogtor.BasicAuthFromEnv("NEO4J"))

m, err := rtogtor.LoadMapping("mapping.yaml")
if err != nil {
    // err is an rtgerr.List: every problem, each with a stable RTG-* code
}
mig := rtogtor.NewMigration(src, dst, m)
defer mig.Close()

plan, err := mig.Plan(ctx)
report, err := mig.Run(ctx, rtogtor.RunOptions{BatchSize: 1000})
verification, err := mig.Verify(ctx, report.RunID, rtogtor.DefaultStateDir)
```

</details>

Complete programs that migrate the example both ways:
[Go](examples/ecommerce/go/main.go) ·
[Python](examples/ecommerce/python/main.py) ·
[TypeScript](examples/ecommerce/typescript/main.ts) ·
[C#](examples/ecommerce/dotnet/Program.cs).

## Supported platforms

| | |
|---|---|
| **Databases** | PostgreSQL (source and destination), Neo4j 5 (source and destination) — see the [compatibility matrix](specs/compatibility.md) |
| **Bundled engine binaries** | Linux x86_64 / arm64, macOS x86_64 / arm64, Windows x64 |

Type support and current gaps (for example junction tables, `time`/`interval`
values, delete propagation) are documented in
[known limitations](docs/limitations.md). Every unsupported case fails with a
clear error code — none is silently ignored.

## Repository layout

| Path | Contents |
|---|---|
| [`specs/`](specs/) | The language-neutral specification — the source of truth |
| [`engine/`](engine/) | Go engine: library (`github.com/TuanFaied/rtogtor/engine`) and CLI |
| [`sdks/`](sdks/) | [.NET](sdks/dotnet/), [Python](sdks/python/) and [TypeScript](sdks/typescript/) SDKs |
| [`tools/codegen/`](tools/codegen/) | Generates SDK types from `specs/` |
| [`conformance/`](conformance/) | Shared test vectors and cases every SDK must pass |
| [`examples/`](examples/) | Example schema, mappings and the round trip in all four languages |
| [`docs/`](docs/) | Guides, architecture decisions and the full project specification |

## Documentation

**Using RtoGtoR**

- [Migration guide](docs/migration-guide.md) — PostgreSQL → Neo4j and back, step by step
- [Recovery and guarantees](docs/recovery.md)
- [Known limitations](docs/limitations.md)
- [Performance and memory](docs/performance.md)
- [Security review](docs/security.md)

**Reference**

- [Specification index](specs/README.md) and [compatibility matrix](specs/compatibility.md)
- [Error codes](specs/error-codes.md)
- [Engine ↔ SDK protocol](specs/protocol.md)
- [Project specification](docs/specification.md)

**Project**

- [Development guide](docs/development.md) — local databases, tests, CI
- [Architecture decision records](docs/adr/)
- [Releasing](docs/releasing.md)
- [Changelog](CHANGELOG.md)

## Roadmap

Planned work — junction-table relationships, natural-key identities, more
database adapters and more — is tracked in the [roadmap](docs/roadmap.md).
Ideas and feedback are welcome in [issues](https://github.com/TuanFaied/rtogtor/issues).

## Contributing

Contributions of all kinds are welcome: bug reports, documentation, new type
support, adapters and SDK improvements. Please read
[CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request, and see
[GOVERNANCE.md](GOVERNANCE.md) for how decisions are made. Everyone
participating is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

Changes to the specification affect every SDK; please open a
[spec change proposal](https://github.com/TuanFaied/rtogtor/issues/new/choose)
first.

## Security

Please **do not** report security vulnerabilities in public issues. See
[SECURITY.md](SECURITY.md) for how to report them privately.

## License

RtoGtoR is licensed under the [Apache License 2.0](LICENSE). See [NOTICE](NOTICE)
for attributions.
