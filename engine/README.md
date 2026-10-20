# RtoGtoR engine (Go)

The single implementation of RtoGtoR's migration logic, and the `rtogtor`
CLI. See [ADR-0001](../docs/adr/0001-single-go-engine-thin-sdks.md).

- Module: `github.com/TuanFaied/rtogtor/engine` (package `rtogtor`,
  see [ADR-0002](../docs/adr/0002-go-module-path-and-tagging.md))
- Requires Go 1.25 or newer (use the latest patch release)

## Build, test, run

```bash
go build -o bin/rtogtor ./cmd/rtogtor
go test ./...
go vet ./...
gofmt -l .
./bin/rtogtor validate --mapping ../examples/ecommerce/mapping.yaml
```

After editing any `specs/*.json` file:

```bash
go generate ./...
```

## Packages

| Package | Purpose |
|---|---|
| `.` (`rtogtor`) | Public API: versions, `LoadMapping` |
| `spec` | Spec version, embedded copies of `specs/*.json` |
| `rtgerr` | Stable error codes from `specs/error-codes.json` |
| `ir` | Logical types, canonical values (`values.md`), `rtg_id` (`identity.md`) |
| `mapping` | Mapping loading (JSON/YAML) and validation |
| `schemameta` | Inspected schema metadata document |
| `adapters/relational`, `adapters/graph` | Adapter interfaces (unstable) |
| `cmd/rtogtor`, `internal/cli` | The CLI |

## Using it from Go

```go
import "github.com/TuanFaied/rtogtor/engine"

m, err := rtogtor.LoadMapping("mapping.yaml")
if err != nil {
    // err lists every problem, each with a stable RTG-* code
}
```
