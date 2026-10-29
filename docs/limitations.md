# Known limitations

What engine 0.1 does **not** do yet, so nobody has to find out the hard way.
Each item fails clearly with an error code; none is silently ignored.

## Directions and features

| Limitation | What happens | Planned |
|---|---|---|
| Junction-table relationships (both directions) | `RTG-PLAN-006`. Map the junction table as an entity with two foreign-key relationships instead | Later |
| Relationship properties, Neo4j → PostgreSQL | `RTG-PLAN-006` | Later |
| A foreign-key column filled after the rows (self-reference or cycle) that is also a mapped property | `RTG-PLAN-006` | — |
| Neo4j → other relational databases, other graph databases | Only PostgreSQL and Neo4j adapters exist | Later |
| `identity.strategy: natural` | `RTG-PLAN-006`; use `hash` | Later |
| SDK connections with literal credentials outside `Migration` (all SDKs) | `Engine` methods accept only connections that name environment variables | — |
| SDK packages on PyPI, npm and NuGet | Built and smoke-tested, not yet published | Milestone 7 |

## Types

| Type | Status |
|---|---|
| `time`, `timetz`, `interval` (`duration`) | Inspected, but cannot be written to Neo4j yet (`RTG-PLAN-006`) |
| `decimal`, `uuid`, `json` | Stored in Neo4j as strings, which is a `representation` loss. They need a `policies.allow` entry or the flexible policy |
| `timestamptz` | Stored as a Neo4j `DateTime` in zone `UTC`. The original offset is not kept, because PostgreSQL does not store it either |
| Arrays | One dimension only. An array containing `NULL` fails that record (`RTG-EXEC-001`), because Neo4j lists cannot hold null |
| JSON numbers a double cannot hold exactly (for example `12345678901234567890`) | That record fails (`RTG-EXEC-001`) rather than being rounded |
| PostgreSQL `BC` dates, years after 9999 | That record fails (`RTG-EXEC-001`) |
| Geometric, network, range, money and other types | Reported by `inspect` as unsupported (`RTG-SCHEMA-001`); cannot be mapped |

## Behavior

- **Deletes are never propagated.** A migration only creates and updates.
  Re-running after source deletes leaves the deleted rows' nodes in Neo4j.
- **No consistent snapshot across a whole run.** See
  [recovery.md](recovery.md#changing-sources).
- **Rows with a NULL key** are not migrated. They are counted and recorded
  as one aggregate failed record (`RTG-ID-001`).
- **Verification of shared labels:** when several entities share a primary
  label, node counts are compared per label, not per entity.
- **Relationships are merged per (start node, type, end node).** Two foreign
  keys of the same type between the same two nodes produce one relationship.
- **Plans do not estimate run time** or row counts yet.

## Neo4j → PostgreSQL

- **Unmapped labels, properties and relationship types are not migrated**
  ([ADR-0006](adr/0006-graph-to-relational-semantics.md) §8).
- **Index the key properties.** Nodes are read in key order, then element
  ID. Without an index on the key properties of each label, Neo4j sorts the
  label for every batch, which is slow for large labels. RtoGtoR never
  creates the index: the source is never modified.
- **Duplicate keys are detected when they are adjacent in key order.** Keys
  are compared after canonicalization, so stored values that order apart but
  canonicalize equal (a decimal key stored as `"1.0"` and `"1"`, uuid keys
  in mixed case, DateTime keys with different zones for the same instant)
  are not adjacent: the second one silently updates the first row.
  `rtogtor verify` then reports a row count below the distinct node keys
  (`RTG-VERIFY-002`). Keys written by RtoGtoR are always canonical.
- **Created tables only declare the key as `NOT NULL`.** Inspection samples
  whether a property is on every node, which does not prove it always will
  be. Create the tables yourself for stricter constraints.
- **`createTables` never alters a table.** An existing table that does not
  match the mapping is rejected at planning time.
- **`float64` into a `real` column** is assigned through PostgreSQL's cast
  and rounded to single precision without a recorded loss.
- **Deferred foreign keys need nullable columns**: rows are inserted first
  and the column is filled afterwards (`RTG-PLAN-010`).
- **Planning needs the destination**, even for a dry run, because it checks
  the tables that already exist.
