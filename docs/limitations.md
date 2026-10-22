# Known limitations

What engine 0.1 does **not** do yet, so nobody has to find out the hard way.
Each item fails clearly with an error code; none is silently ignored.

## Directions and features

| Limitation | What happens | Planned |
|---|---|---|
| Neo4j → PostgreSQL | `rtogtor plan`/`migrate` fail with `RTG-PLAN-006`; `validate` warns with `RTG-MAP-010` | Milestone 5 |
| Junction-table relationships | `RTG-PLAN-006`. Map the junction table as an entity with two foreign-key relationships instead | Later in Milestone 2 / 5 |
| `identity.strategy: natural` | `RTG-PLAN-006`; use `hash` | Later |
| Python, TypeScript and C# SDKs | Not available; use the CLI or the Go library | Milestones 3–4 |

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
