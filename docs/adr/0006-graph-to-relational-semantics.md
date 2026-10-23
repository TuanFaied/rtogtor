# ADR-0006: Graph → relational semantics

- **Status:** Accepted (review requested)
- **Date:** 2026-10-23

## Context

Milestone 5 adds Neo4j → PostgreSQL. Graphs allow things tables do not: any
number of relationships of a type per node, nodes without key properties,
duplicate natural keys, nanosecond timestamps, and cycles. Every choice below
decides whether data is migrated, rejected, or changed, so each is explicit.

## Decisions

1. **Same mapping format, reversed.** A `graph-to-relational` mapping uses the
   structure the forward direction uses. An entity maps nodes carrying **all**
   of its `labels` to rows of its `table`. Properties map to columns. A
   `foreignKey` relationship fills the owner row's foreign-key columns with the
   key of the node at the other end of a relationship of that `type` and
   `direction`. The e-commerce mapping becomes its own reverse by swapping
   `direction`, `source` and `destination`.

2. **Foreign keys are written with the row.** Entities load in topological
   order of their foreign keys, so referenced rows exist first.
   **Self-references and cycles are deferred:** their rows are inserted with
   the foreign key `NULL`, then an `update-foreign-keys` step fills it in. A
   deferred foreign key whose column is `NOT NULL` in an existing table cannot
   be loaded, so the planner rejects it (`RTG-PLAN-010`).

3. **Idempotent writes.** Rows are written with
   `INSERT … ON CONFLICT (key) DO UPDATE`, and foreign-key updates are plain
   `UPDATE … WHERE key = …`. Both are safe to replay. `ON CONFLICT` needs a
   primary key or unique constraint on exactly the key columns; without one,
   the planner rejects the table (`RTG-PLAN-008`).

4. **Nothing merges silently.**
   - Two nodes with the same key values: the first in key order is written;
     each further one is a failed record (`RTG-EXEC-006`). They are never
     merged into one row.
   - A node with more than one relationship for a foreign key: failed record
     (`RTG-EXEC-005`). A column holds one reference.
   - A relationship whose other end is not a node of the referenced entity
     (wrong label, or missing key properties): failed record (`RTG-EXEC-002`).
     It is not dropped.
   - A node missing a key property: failed record (`RTG-ID-001`).

5. **Precision is checked per value.** Neo4j stores nanoseconds; PostgreSQL
   stores microseconds. A temporal value with sub-microsecond digits, or a
   decimal with more fractional digits than its column's declared scale, would
   be rounded by PostgreSQL. Under the strict policy such a value is a failed
   record (`RTG-EXEC-007`). With a `precision` allow rule or the flexible
   policy, it is written and rounded, and the plan lists the transformation.
   Values that fit are never affected, so typical data needs no allow rule.

6. **Tables.** By default, destination tables must exist. The planner checks
   columns, types, the key constraint and nullability against the inspected
   schema. With `destination.createTables: true`, the plan contains the
   `CREATE TABLE IF NOT EXISTS` statements for missing tables (types derived
   from logical types, primary key, foreign keys), so they can be reviewed
   before execution. Existing tables are never altered or dropped.

7. **Reading.** Nodes are read in batches ordered by their key properties,
   then by element ID, which makes the order total and puts duplicate keys
   next to each other. The checkpoint is the last (key, element ID). Reading
   is read-only. Neo4j sorts per batch, so an index on the key properties is
   strongly recommended for large labels (docs/limitations.md). RtoGtoR never
   creates the index, because the source is never modified.

8. **Unmapped data is not migrated.** Labels, properties and relationship
   types the mapping does not name are ignored. This is the meaning of an
   explicit mapping, and `rtogtor plan` shows exactly what is read.

## Consequences

- ✅ Relational → graph → relational round-trips the e-commerce example exactly
  (tested), including composite keys, self-references and decimals.
- ✅ Every graph shape that does not fit a table becomes a counted, inspectable
  failed record, never a silent change.
- ⚠️ Deferred foreign keys need nullable columns or deferrable constraints.
- ⚠️ Key-ordered reads cost more on large labels without an index.
- ⚠️ Junction-table relationships remain unsupported in both directions.
