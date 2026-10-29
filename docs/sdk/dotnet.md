# .NET SDK

Package `RtoGtoR` (NuGet), namespace `RtoGtoR`. User documentation:
[`sdks/dotnet/README.md`](../../sdks/dotnet/README.md). This page is for
contributors.

## How it works

```text
your code ──► RtoGtoR.dll ──(JSON Lines on stdin/stdout)──► rtogtor serve --stdio
               Migration / Engine                            (bundled engine process)
```

- `Engine` starts the binary, performs the handshake, and rejects engines
  outside `Engine.SupportedEngineVersions` (`RTG-PROTO-007`).
- `Migration` owns an engine process. It passes literal credentials as
  private environment variables, so they never appear in messages.
- `Mapping` loads JSON or YAML and validates it locally against the embedded
  `mapping.schema.json`. Its YAML typing follows the YAML 1.2 core schema,
  like the engine's YAML reader. All semantic validation is the engine's.
- Types in `RtoGtoR.Model` and constants in `ErrorCodes` are **generated**
  (`src/RtoGtoR/Generated`, ADR-0005). Never edit them.
- Cancellation sends a protocol `cancel`. The SDK reports the outcome as
  `OperationCanceledException`, with the engine's `RtoGtoRException` (and any
  run report) as the inner exception.
- Disposing sends `shutdown`, waits up to `ShutdownTimeout`, then kills the
  process tree.

## Choices per the specification

| Concern | Choice |
|---|---|
| `int64` values in documents | Counts are `long`. Value-level `int64` and `decimal` stay strings, as in the schemas |
| Errors | `RtoGtoRException` with `Code`, `Severity`, `Retryable`, `Path`, `Diagnostics`, `Report` |
| Async | Async-only API with `CancellationToken`; `IAsyncDisposable` |
| Progress | `IProgress<Progress>` |
| Nullability | Nullable reference types enabled. Schema-required members are C# `required` members |

## Build and test

From `sdks/dotnet/`:

```bash
bash ./build-engine.sh                 # engine for this platform → src/RtoGtoR/runtimes
dotnet build -c Release
dotnet test                            # offline, engine, protocol and conformance tests
```

Integration tests run when both variables are set. PostgreSQL must hold the
e-commerce example in schema `public` (the Compose database does):

```bash
export RTOGTOR_TEST_POSTGRES_URL="postgres://rtogtor:<password>@127.0.0.1:15432/ecommerce?sslmode=disable"
export RTOGTOR_TEST_NEO4J_URL="neo4j://neo4j:<password>@127.0.0.1:7687"
dotnet test --filter "FullyQualifiedName~IntegrationTests"
```

Set `RTOGTOR_CONFORMANCE_OUT=results.json` to write conformance results
([runner protocol](../../conformance/runner-protocol.md)).

## Package

```bash
./build-engine.sh all
dotnet pack src/RtoGtoR -c Release -o ../../dist/nuget
cd smoke && dotnet run -c Release -- ../../..    # installs the .nupkg and runs the bundled engine
```

The package carries `runtimes/<rid>/native/rtogtor[.exe]` for linux-x64,
linux-arm64, osx-x64, osx-arm64 and win-x64. On Unix, the SDK restores the
execute bit, which NuGet does not preserve.

## After changing a schema

```bash
go -C tools/codegen run . -repo ../..
```

Run this from the repository root, then commit the regenerated files under
`src/RtoGtoR/Generated`.
