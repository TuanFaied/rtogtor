# E-commerce example

A small shop database (customers, products, orders, order lines) that
exercises single and composite keys, foreign keys and two self-references.

| File | What it is |
|---|---|
| [`schema.sql`](schema.sql) | PostgreSQL tables and sample data |
| [`mapping.yaml`](mapping.yaml) | PostgreSQL → Neo4j mapping |
| [`mapping-reverse.yaml`](mapping-reverse.yaml) | Neo4j → PostgreSQL mapping, the reverse of `mapping.yaml` (`rtogtor scaffold --reverse mapping.yaml` drafts the same mapping) |
| [`go/`](go/), [`dotnet/`](dotnet/), [`python/`](python/), [`typescript/`](typescript/) | The same program in each language: migrate PostgreSQL → Neo4j and back, verifying every value both ways |

## Run it

Start disposable databases (`docker compose up -d --wait` from the
repository root, see [development.md](../../docs/development.md)), load the
schema, and point the examples at them:

```bash
export POSTGRES_URL='postgres://rtogtor:<password>@127.0.0.1:15432/ecommerce?sslmode=disable'
export NEO4J_URL='neo4j://neo4j:<password>@127.0.0.1:7687'
psql "$POSTGRES_URL" -v ON_ERROR_STOP=1 -f examples/ecommerce/schema.sql
```

Each program prints the plan, every finished step, and the verification
result, and exits non-zero if anything does not round-trip:

| Language | From this directory |
|---|---|
| Go | `cd go && go run .` |
| C# | `cd dotnet && dotnet run` |
| Python | `cd python && python main.py` (with `pip install rtogtor`, or `pip install -e ../../../sdks/python`) |
| TypeScript (Node.js 22.6+) | `cd typescript && npm install && npm start` |

Inside this repository the SDKs are built from source, so the C#, Python and
TypeScript programs need an engine: build one with `go build -o rtogtor
./cmd/rtogtor` in `engine/` and set `RTOGTOR_ENGINE_PATH` to it. Installed
packages bundle their engine.

Every run is safe to repeat: writes are idempotent, and the reverse
migration writes the same rows back into the original tables. CI runs all
four programs against fresh databases on every change
([`.github/workflows/examples.yml`](../../.github/workflows/examples.yml)).
