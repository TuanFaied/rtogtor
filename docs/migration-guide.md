# Migration guide: PostgreSQL → Neo4j

A walkthrough using the e-commerce example. It assumes the local databases
from [development.md](development.md) are running.

## 1. Set the connection variables

Connection URLs are read only from environment variables you name, never from
command-line flags, so they never appear in process lists or shell history.

```bash
export SOURCE_URL="postgres://rtogtor:<password>@127.0.0.1:5432/ecommerce?sslmode=disable"
export NEO4J_URL="neo4j://neo4j:<password>@127.0.0.1:7687"
```

## 2. Inspect the source

```bash
rtogtor inspect --source-env SOURCE_URL
```

This lists tables, columns, logical types, keys and foreign keys. Columns
with unsupported types and tables without a primary key are flagged.

## 3. Write and validate a mapping

See [`examples/ecommerce/mapping.yaml`](../examples/ecommerce/mapping.yaml):

- **entities** map a table to nodes: `labels`, `key` (the identity, in order)
  and `properties` (column → property, with the logical type);
- **relationships** map a foreign key to a relationship type and direction:
  - `owner-to-referenced`: (row with the FK) → (referenced row);
  - `referenced-to-owner`: the reverse;
- **policies** decide what happens with lossy conversions. Under the default
  `strict` policy, each loss needs an `allow` rule with a reason.

```bash
rtogtor validate --mapping mapping.yaml
```

## 4. Plan

```bash
rtogtor plan --mapping mapping.yaml --source-env SOURCE_URL
```

The plan checks the mapping against the live schema (tables, columns, types,
foreign-key types) and lists every step and every lossy transformation with
what allowed it. `--format json` prints the plan as canonical JSON.

## 5. Dry run

```bash
rtogtor migrate --mapping mapping.yaml --source-env SOURCE_URL --dry-run
```

A dry run reads and converts every row and reports what would be written. It
writes nothing to Neo4j (not even constraints) and does not need
`--dest-env`.

## 6. Migrate

```bash
rtogtor migrate --mapping mapping.yaml --source-env SOURCE_URL --dest-env NEO4J_URL
```

| Exit status | Meaning |
|---|---|
| 0 | Every record migrated |
| 3 | Finished, but some records failed (`rtogtor failures`) |
| 1 | Failed or interrupted; resumable |
| 2 | Usage error |

If the run stops, resume it. Nothing is duplicated
([recovery.md](recovery.md)):

```bash
rtogtor migrate --mapping mapping.yaml --source-env SOURCE_URL --dest-env NEO4J_URL --resume <run-id>
```

## 7. Verify and report

```bash
rtogtor verify   --run-id <run-id> --source-env SOURCE_URL --dest-env NEO4J_URL
rtogtor report   --run-id <run-id>
rtogtor failures --run-id <run-id>
```

Every command accepts `--format json`. Run state lives in `.rtogtor/` by
default (`--state-dir` to change it).

## The other way: Neo4j → PostgreSQL

A mapping with `direction: graph-to-relational` reads nodes and writes rows.
It uses the same format: each entity maps nodes with all its `labels` to
rows of its `table`, and each foreign-key relationship fills the owner's
foreign-key columns from the node at the other end. The semantics are
recorded in [ADR-0006](adr/0006-graph-to-relational-semantics.md).

```bash
export GRAPH_URL='neo4j://neo4j:<password>@localhost:7687'
export TARGET_URL='postgres://user:<password>@localhost:5432/shop'

rtogtor inspect  --source-env GRAPH_URL                      # labels and property types
rtogtor validate --mapping examples/ecommerce/mapping-reverse.yaml
rtogtor plan     --mapping examples/ecommerce/mapping-reverse.yaml --source-env GRAPH_URL --dest-env TARGET_URL
rtogtor migrate  --mapping examples/ecommerce/mapping-reverse.yaml --source-env GRAPH_URL --dest-env TARGET_URL --dry-run
rtogtor migrate  --mapping examples/ecommerce/mapping-reverse.yaml --source-env GRAPH_URL --dest-env TARGET_URL
rtogtor verify   --run-id <run-id> --source-env GRAPH_URL --dest-env TARGET_URL
```

- Planning inspects the destination too, so `--dest-env` is needed even for
  a dry run.
- Destination tables must exist with a primary key or unique constraint on
  the mapping key, unless `destination.createTables: true`; the plan then
  shows the `CREATE TABLE` statements before anything runs.
- Graph data that does not fit a table is never changed silently: duplicate
  keys (`RTG-EXEC-006`), several relationships for one foreign key
  (`RTG-EXEC-005`), a relationship to the wrong kind of node
  (`RTG-EXEC-002`) and values PostgreSQL would round (`RTG-EXEC-007`) each
  become failed records listed by `rtogtor failures`.
- Index the key properties of large labels in Neo4j first
  ([limitations](limitations.md#neo4j--postgresql)).
