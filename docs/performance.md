# Performance and memory

## Memory is bounded

Every step reads one batch at a time, and the reader runs at most two
batches ahead of the writer. Memory therefore depends on the batch size and
record size, not on the size of a table or label. Value verification streams
the same way.

Two end-to-end tests enforce a ceiling: they move 150 000 records of about
2 KiB each way with batch size 1000, sample the heap every 20 ms, and fail
above 96 MiB in use (`engine/internal/e2e`, `TestBoundedMemory` and
`TestBoundedMemoryReverse`).

## Measured throughput

Local Docker containers (PostgreSQL 17, Neo4j 5.26 Community), one laptop
(8 logical CPUs, Windows 11), batch size 1000, records of about 2 KiB,
measured by the memory-ceiling tests:

| Direction | Records | Time | Records/s | Peak heap in use |
|---|---|---|---|---|
| PostgreSQL → Neo4j | 150 000 | 35.1 s | ≈ 4 300 | 30 MiB |
| Neo4j → PostgreSQL (key index present) | 150 000 | 35.0 s | ≈ 4 300 | 39 MiB |

These numbers show the shape of the cost, not a guarantee: the databases'
write paths dominate. Run the tests at your scale with:

```bash
RTOGTOR_PERF_ROWS=1000000 go test -tags integration -run BoundedMemory -v -timeout 60m ./internal/e2e/
```

## Engine CPU per value

`go test -bench . -benchmem ./ir/` on the same machine:

| Operation | Time | Allocations |
|---|---|---|
| `rtg_id` of a two-column key | ≈ 1.3 µs | 5 |
| Canonicalize an `int64` | ≈ 0.7 µs | 1 |
| Canonicalize a `decimal` | ≈ 1.8 µs | 3 |
| Canonicalize a `timestamptz` with offset | ≈ 2.2 µs | 5 |
| Canonicalize a `uuid` | ≈ 0.3 µs | 1 |
| Canonicalize a `string` | ≈ 0.03 µs | 0 |

At a few microseconds per value, the engine's own work is a small fraction
of each batch's database round trips.

## Tuning

- **Batch size** (`--batch-size`, default 1000) trades round trips against
  memory and the size of a replayed batch after a crash.
- **Neo4j → PostgreSQL:** create a range index on each label's key
  properties before migrating large labels; the plan warns when one is
  missing and prints the statement.
- **PostgreSQL → Neo4j:** the identity constraints RtoGtoR creates are
  what make `MERGE` fast; do not drop them during a run.
