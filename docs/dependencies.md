# Dependencies

RtoGtoR keeps dependencies minimal. Every direct dependency is listed here with
the reason it is needed. Add an entry in the same pull request that adds a
dependency.

## Engine (Go module `github.com/TuanFaied/rtogtor/engine`)

| Module | Used for | Why not the standard library |
|---|---|---|
| `github.com/spf13/cobra` | CLI commands, flags, help | Subcommands with consistent help and flag handling; the de facto standard for Go CLIs |
| `gopkg.in/yaml.v3` | Reading YAML mappings | No YAML in the standard library. v3 rejects duplicate keys and follows YAML 1.2 (no `no` → `false` surprises) |
| `github.com/santhosh-tekuri/jsonschema/v6` | Validating documents against `specs/*.schema.json` | No JSON Schema in the standard library. Supports draft 2020-12, pure Go, no cgo |
| `golang.org/x/text` | English messages from the JSON Schema validator | Already required by `jsonschema/v6`; pinned to v0.41.0, which fixes GO-2026-5970 and GO-2026-6629 |
| `github.com/jackc/pgx/v5` | PostgreSQL driver | No PostgreSQL protocol in the standard library. Pure Go (no cgo), actively maintained, exposes typed catalog values such as `oid` and arrays |
| `github.com/neo4j/neo4j-go-driver/v6` | Neo4j driver | The official driver. v6 is the current major version; the project specification named v5, which is in maintenance |
| `pgregory.net/rapid` (tests only) | Property-based tests | Shrinking generators for identity and value-encoding invariants |

## .NET SDK (`sdks/dotnet`)

| Package | Used for | Why |
|---|---|---|
| `JsonSchema.Net` | Validating mappings locally against `mapping.schema.json` | Full JSON Schema draft 2020-12 support, including cross-file `$ref` |
| `YamlDotNet` | Reading YAML mappings | The SDK applies YAML 1.2 core-schema typing itself (see `Mapping.cs`) so results match the engine |
| xUnit, Microsoft.NET.Test.Sdk, coverlet (tests only) | Tests | Standard .NET test stack |

## Code generator (`tools/codegen`)

No dependencies (standard library only). See ADR-0005.

## Planned (engine)

| Module | Milestone | Used for |
|---|---|---|
| A decimal library (`cockroachdb/apd` or `shopspring/decimal`) | M5 | Exact numeric conversion when writing to PostgreSQL. Not needed for PostgreSQL → Neo4j: decimals are canonicalized as text, without arithmetic |

Considered and not adopted:

| Module | Reason |
|---|---|
| `github.com/testcontainers/testcontainers-go` | Integration tests use the Compose services locally and a service container in CI, configured by `RTOGTOR_TEST_POSTGRES_URL`. This avoids a large dependency tree for the same isolation (each test uses its own schema). Revisit if tests need per-test servers. |

## Go version

The module declares `go 1.25.0`. Build release binaries with the latest
patch release of a supported Go version so standard-library security fixes
are included.
