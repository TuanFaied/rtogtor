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
| `pgregory.net/rapid` (tests only) | Property-based tests | Shrinking generators for identity and value-encoding invariants |

Planned:

| Module | Milestone | Used for |
|---|---|---|
| `github.com/jackc/pgx/v5` | M1 step 5 | PostgreSQL driver |
| `github.com/testcontainers/testcontainers-go` (tests only) | M1 step 5 | Disposable PostgreSQL/Neo4j for integration tests |
| `github.com/neo4j/neo4j-go-driver/v5` | M2 | Official Neo4j driver |
| A decimal library (`cockroachdb/apd` or `shopspring/decimal`) | M2 | Exact numeric conversion. Not needed yet: v0.1 canonicalizes decimals as text without arithmetic |

## Go version

The module declares `go 1.25.0`. Build release binaries with the latest
patch release of a supported Go version so standard-library security fixes
are included.
