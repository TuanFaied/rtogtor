# rtogtor

Migrate data between **PostgreSQL and Neo4j**, in either direction, with
explicit mappings, plans you can inspect before writing, resumable runs and
value-level verification.

Version 1.0. The mapping's `direction` decides the way:
`relational-to-graph` reads PostgreSQL and writes Neo4j, `graph-to-relational`
reads Neo4j and writes PostgreSQL. See the
[compatibility matrix](https://github.com/TuanFaied/rtogtor/blob/main/specs/compatibility.md).

Each wheel bundles the RtoGtoR engine for its platform (Linux x86_64/aarch64,
macOS 12+ x86_64/arm64, Windows x64). The SDK runs the engine as a child
process. All migration logic runs in the engine, which behaves identically for
every RtoGtoR SDK.

## Install

```bash
pip install rtogtor
```

## Use

```python
from rtogtor import Connection, Mapping, Migration

source = Connection.postgresql(url_env="SOURCE_URL")
destination = Connection.neo4j(uri="bolt://localhost:7687", auth_env="NEO4J")
mapping = Mapping.from_file("mapping.yaml")

with Migration(source=source, destination=destination, mapping=mapping) as m:
    plan = m.plan()  # nothing is written
    report = m.run(dry_run=False)
    verification = m.verify(report.run_id, values=True)  # every value, both sides
```

A complete program, migrating the example both ways:
[examples/ecommerce/python](https://github.com/TuanFaied/rtogtor/blob/main/examples/ecommerce/python/main.py).

- **Credentials** come from environment variables: `SOURCE_URL`, plus
  `NEO4J_USER` and `NEO4J_PASSWORD` for `auth_env="NEO4J"`. They are never sent
  in protocol messages or printed.
- **Errors** are `RtoGtoRError` with a stable `code` (`ErrorCode.MAP_005`, …),
  `severity` and `retryable`. Invalid mappings list every problem in
  `diagnostics`.
- **Resuming:** if a run fails, is interrupted or you press Ctrl-C, the error
  carries the `report`. Pass `report.run_id` as `resume_run_id`. Committed
  batches are not repeated, and nothing is duplicated.
- **Retries:** transient errors are retried per `retry=RetryPolicy(...)`
  (default 3 attempts, 200 ms to 5 s backoff).
- **asyncio:** `AsyncMigration` offers the same methods as coroutines.
- **Typed model:** `rtogtor.models` holds Pydantic models generated from the
  specification's JSON Schemas.
- **CLI:** `python -m rtogtor plan --mapping mapping.yaml --source-env SOURCE_URL`.

## Learn more

- [Migration guide](https://github.com/TuanFaied/rtogtor/blob/main/docs/migration-guide.md)
- [Recovery guarantees](https://github.com/TuanFaied/rtogtor/blob/main/docs/recovery.md)
- [Known limitations](https://github.com/TuanFaied/rtogtor/blob/main/docs/limitations.md)

License: Apache-2.0.
