# Python SDK

Package `rtogtor` (PyPI), Python 3.10+. Contributor notes; user
documentation lives in the [package README](../../sdks/python/README.md).

## Use

```python
from rtogtor import Connection, Mapping, Migration

source = Connection.postgresql(url_env="SOURCE_URL")
destination = Connection.neo4j(uri="bolt://localhost:7687", auth_env="NEO4J")
mapping = Mapping.from_file("mapping.yaml")

with Migration(source=source, destination=destination, mapping=mapping) as m:
    plan = m.plan()
    report = m.run(dry_run=False, on_progress=lambda p: print(p.step_id, p.written))
    verification = m.verify(report.run_id)
```

The async variant is `AsyncMigration`, with `async with` and `await m.run()`.
Cancelling the task cancels the engine request.

## Choices per the specification

| Concern | Choice |
|---|---|
| Typed model | Generated Pydantic v2 models (`rtogtor.models`), `strict` and `frozen`; use `model_copy(update=...)` |
| JSON names | snake_case attributes with the schema's names as aliases; write with `model_dump(mode="json", by_alias=True, exclude_unset=True)` |
| `int64` / `decimal` values | Strings, as in the schemas; counts are `int` |
| Errors | `RtoGtoRError` with `code`, `severity`, `retryable`, `path`, `diagnostics`, `report` |
| Cancellation | Sync: Ctrl-C (`KeyboardInterrupt`) cancels the engine request, then re-raises with the engine error as `__cause__`. Async: task cancellation |
| YAML | PyYAML's parser with YAML **1.2** core-schema typing (PyYAML itself follows 1.1, where `yes` is true) |
| Schema validation | `jsonschema` (draft 2020-12) against schemas shipped in the package |
| CLI | `python -m rtogtor ...` and the `rtogtor` script run the bundled engine |

## Develop

From `sdks/python/`:

```bash
python -m venv .venv && .venv/bin/pip install -e ".[dev]"
python build_wheels.py --dev        # engine for this machine → src/rtogtor/_bin
pytest                              # offline, engine, conformance, Hypothesis properties
ruff check . && ruff format --check . && mypy
```

Integration tests run when `RTOGTOR_TEST_POSTGRES_URL` and
`RTOGTOR_TEST_NEO4J_URL` are set (see [dotnet.md](dotnet.md) for the setup).

## Package

```bash
python build_wheels.py              # five platform wheels → ../../dist/python
python smoke.py                     # installs this platform's wheel into a fresh venv and runs it
```

Each wheel holds exactly one engine binary and is tagged
`py3-none-<platform>`: `manylinux2014`/`musllinux_1_1` (x86_64, aarch64),
`macosx_12_0` (x86_64, arm64) and `win_amd64`. The engine is a static Go
binary, so one Linux wheel serves glibc and musl.
