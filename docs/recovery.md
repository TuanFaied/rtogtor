# Recovery and delivery guarantees

This document states exactly what RtoGtoR guarantees when a migration is
interrupted, and what it does not. It covers both directions implemented by
engine 0.1: PostgreSQL → Neo4j, and Neo4j → PostgreSQL (see
[the section below](#neo4j--postgresql)).

## Summary

| Property | Guarantee |
|---|---|
| Atomicity | Each **batch** is atomic in the destination (one Neo4j transaction). A run as a whole is **not** atomic. |
| Delivery | **At-least-once** per batch: a batch interrupted after its commit but before its checkpoint is written again on resume. |
| Duplicates | **None**, because every write is idempotent (`MERGE` on `rtg_id` with a uniqueness constraint). At-least-once delivery with idempotent writes gives **effectively-once** results. |
| Resume point | The last **committed and checkpointed** batch of each step. |
| Distributed transactions | **Not provided.** PostgreSQL and Neo4j are never in one transaction. |
| Source consistency | Each batch reads a consistent snapshot. The run as a whole does **not** see one snapshot of the source (see "Changing sources"). |

## How a run proceeds

1. A run gets a time-ordered ID (UUIDv7). Its directory
   `<state-dir>/runs/<run-id>/` holds `state.json`, `mapping.json`,
   `plan.json`, `failures.jsonl` and `report.json`. None of them contain
   credentials.
2. Identity constraints (`CREATE CONSTRAINT … IF NOT EXISTS … IS UNIQUE`) are
   created before any data is loaded.
3. Steps run in plan order: all node loads, then all relationship loads.
   Each step reads its table in key order (keyset pagination on the entity's
   key), one batch at a time. The reader runs at most two batches ahead, so
   memory use is bounded by the batch size, not the table size.
4. For each batch:
   1. rows are converted and identities derived; rows that cannot be
      converted become failed records;
   2. the remaining rows are written in **one** destination transaction,
      retried on transient errors with exponential backoff and jitter;
   3. **after the commit**, the batch's failed records are appended to
      `failures.jsonl` and synced to disk;
   4. the checkpoint (the last key of the batch, plus counters) is written
      to `state.json` atomically (write to a temporary file, then rename).

## What happens on a crash

| The process stops… | Effect | On resume |
|---|---|---|
| before a batch's transaction commits | Neo4j rolls the transaction back | The batch is read and written again |
| after the commit, before failed records are synced | The batch is in Neo4j, not checkpointed | The batch is written again; `MERGE` makes this a no-op |
| after failed records are synced, before the checkpoint | Failed records for an uncommitted (from the run's view) batch exist | They are ignored until the batch is checkpointed; the replay writes them again and listings show each once |
| after the checkpoint | Nothing is lost | The next batch is read |

`failures.jsonl` is append-only. Readers ignore lines whose batch is not yet
checkpointed, count a replayed batch's lines once, and ignore a torn final
line.

All four crash points are covered by tests, both against in-memory adapters
(`engine/execution`) and against real PostgreSQL and Neo4j
(`engine/internal/e2e`).

## Resuming

```bash
rtogtor migrate --mapping mapping.yaml --source-env SOURCE_URL --dest-env NEO4J_URL --resume <run-id>
```

- The run continues with the **plan stored in the run**, so checkpoints and
  steps always match.
- The mapping file must be identical to the original (same canonical hash);
  otherwise the engine refuses with `RTG-RUN-002`.
- The original batch size is kept.
- A finished run cannot be resumed (`RTG-RUN-003`). Re-running the whole
  migration as a new run is always safe.

## Retries

| Error | Behavior |
|---|---|
| Transient Neo4j errors (deadlocks, leader switches, unavailable servers) | Retried by the driver's managed transactions, then up to 3 attempts by the engine (200 ms → 5 s backoff). If they persist, the run stops as `interrupted` with `RTG-EXEC-003`; resume later. |
| Non-retryable error for a whole batch | The batch is written one row at a time to isolate bad rows; those become failed records and the rest are written. |
| Cancellation (Ctrl-C) | The current batch is abandoned before its checkpoint; the run is `interrupted` and resumable. |

## Changing sources

The source is read in key order, one batch per query. If rows change while a
run is in progress:

- rows inserted **behind** the cursor of a finished or running step are not
  migrated by this run;
- rows updated after being read keep the value read at the time;
- rows deleted after being read remain in the destination.

Run migrations against a quiescent source (or a snapshot or replica) when
exact correspondence matters, and use `rtogtor verify` afterwards.
Re-running the migration as a new run picks up inserts and updates, but it
never deletes destination data. Continuous synchronization, including
deletes, is a later milestone.

## Neo4j → PostgreSQL

The same guarantees hold in the other direction
([ADR-0006](adr/0006-graph-to-relational-semantics.md)):

| Property | Graph → relational |
|---|---|
| Atomicity | Each batch is **one** PostgreSQL statement (`INSERT … SELECT FROM unnest(…)`), so it is atomic. |
| Idempotence | Rows are written with `INSERT … ON CONFLICT (key) DO UPDATE`; deferred foreign keys with `UPDATE … WHERE key = …`. Replaying a batch changes nothing. |
| Resume point | The last checkpointed node of each step: its key values followed by its element ID. |
| Read consistency | Each batch is read in one read transaction. Neo4j is never written. |

A run proceeds as:

1. `ensure-table` steps run the plan's `CREATE TABLE IF NOT EXISTS`
   statements (not in a dry run).
2. `load-rows` steps, referenced tables first, write each entity's rows,
   including foreign keys whose referenced rows are already loaded. Nodes
   are read in key order, then element ID, so duplicate keys are adjacent:
   the first is written and later ones are failed records (`RTG-EXEC-006`).
3. `update-foreign-keys` steps fill self-references and foreign keys inside
   cycles, after every row exists.

When PostgreSQL rejects a batch because of its data (SQLSTATE classes 22
and 23, for example a foreign-key or length violation), the batch is written
one row at a time and each rejected row becomes a failed record
(`RTG-PG-002`). Transient errors (serialization failures, deadlocks, lost
connections) are retried and then stop the run as `interrupted`
(`RTG-EXEC-003`). Crash points and resume are tested against real databases
in `engine/internal/e2e/reverse_test.go`.

## Verification

`rtogtor verify --run-id <id>` streams every entity's source keys, derives
each `rtg_id`, and checks that a node exists for it (`RTG-VERIFY-001`). It
also compares node counts per label (`RTG-VERIFY-002`) and relationship
counts per type with the number of source references that point to existing
rows (`RTG-VERIFY-003`). It reports references to non-existent keys in the
source as `RTG-VERIFY-004`. Verification is read-only.

For Neo4j → PostgreSQL, verification streams every entity's node keys and
checks that each has a row (`RTG-VERIFY-001`), compares the row count with
the number of distinct node keys (`RTG-VERIFY-002`), and compares each
filled foreign key with the nodes that have exactly one matching
relationship for it (`RTG-VERIFY-003`, which also reports foreign keys that
reference missing rows). Failed records make verification fail, by design:
the destination then does not match the source.
