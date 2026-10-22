# Development guide

## Prerequisites

| Tool | Version | Used for |
|---|---|---|
| Go | 1.25+ (latest patch release) | Engine |
| Docker | Docker Desktop or Engine with Compose v2 | Local databases, integration tests |

## Local databases

The repository's `docker-compose.yml` starts disposable PostgreSQL 17 and
Neo4j 5 containers. PostgreSQL loads the e-commerce example
(`examples/ecommerce/schema.sql`) on first start.

```bash
cp .env.example .env
```

Edit `.env` and choose local-only passwords. `.env` is git-ignored; never
commit it. If port 5432 is already used by a local PostgreSQL install, set
`POSTGRES_PORT` (for example `55432`).

```bash
docker compose up -d --wait
```

Stop and **delete all data**:

```bash
docker compose down -v
```

Ports are bound to `127.0.0.1` only.

## Running the engine against the example

The CLI reads connection URLs from an environment variable you name, never
from flags. In bash:

```bash
set -a; source .env; set +a
export SOURCE_URL="postgres://rtogtor:${POSTGRES_PASSWORD}@127.0.0.1:${POSTGRES_PORT:-5432}/ecommerce?sslmode=disable"
cd engine
go run ./cmd/rtogtor inspect --source-env SOURCE_URL
```

In PowerShell:

```powershell
Get-Content .env | Where-Object { $_ -match '^\s*[^#].*=' } | ForEach-Object { $k, $v = $_ -split '=', 2; Set-Item "env:$k" $v }
$env:SOURCE_URL = "postgres://rtogtor:$($env:POSTGRES_PASSWORD)@127.0.0.1:$(if ($env:POSTGRES_PORT) { $env:POSTGRES_PORT } else { 5432 })/ecommerce?sslmode=disable"
cd engine
go run ./cmd/rtogtor inspect --source-env SOURCE_URL
```

## Tests

Unit tests need no database:

```bash
cd engine
go test ./...
```

Integration tests run only with the `integration` build tag and require
`RTOGTOR_TEST_POSTGRES_URL`. They **fail** (rather than skip) when it is
missing. Each test creates and drops its own `rtg_it_*` schema; point it only
at a disposable database such as the Compose container.

```bash
export RTOGTOR_TEST_POSTGRES_URL="$SOURCE_URL"
go test -tags integration ./...
```

Golden files (for example
`conformance/cases/ecommerce/expected-schema-metadata.json`) are refreshed
with `-update`. Review the diff by hand before committing it:

```bash
go test -tags integration ./adapters/relational/postgresql/ -update
```

## Lint and security checks

```bash
go vet ./...
gofmt -l .
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run --build-tags integration ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

## After changing `specs/*.json`

```bash
cd engine
go generate ./...
```

CI fails if `engine/spec/data` differs from `specs/`.

## Continuous integration

`.github/workflows/engine.yml` runs on changes to the engine, specs,
conformance data or examples:

| Job | What it checks |
|---|---|
| test | gofmt, vet, embedded-spec drift, unit tests on Linux/Windows/macOS with Go 1.25.x and stable (race detector on Linux) |
| lint | golangci-lint (`engine/.golangci.yml`) |
| vulnerabilities | govulncheck |
| integration | PostgreSQL 17 service + `-tags integration` tests |
| build | Cross-compiles the five target platforms on pushes to `main` |
