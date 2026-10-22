# RtoGtoR for .NET

Migrate data from **PostgreSQL to Neo4j** with explicit mappings, plans you can
inspect before writing, resumable runs and verification.

> **Status: pre-alpha.** PostgreSQL → Neo4j only. See the
> [roadmap](https://github.com/TuanFaied/rtogtor/blob/main/docs/roadmap.md).

The package bundles the RtoGtoR engine for linux-x64, linux-arm64, osx-x64,
osx-arm64 and win-x64. The SDK starts it as a child process and stops it when
you dispose. All migration logic runs in the engine, which behaves identically
for every RtoGtoR SDK.

## Install

```bash
dotnet add package RtoGtoR --prerelease
```

Requires .NET 8 or later.

## Use

```csharp
using RtoGtoR;

var mapping = await Mapping.FromFileAsync("mapping.yaml");
await using var m = new Migration(
    source: Connection.PostgreSql(urlEnv: "SOURCE_URL"),
    destination: Connection.Neo4j("neo4j://localhost:7687", authEnv: "NEO4J"),
    mapping: mapping);

var plan = await m.PlanAsync(ct);                     // nothing is written
var report = await m.RunAsync(new RunOptions { DryRun = false }, ct);
var verification = await m.VerifyAsync(report.RunId, cancellationToken: ct);
```

- **Credentials** are read from environment variables: `SOURCE_URL`, plus
  `NEO4J_USER` and `NEO4J_PASSWORD` for `authEnv: "NEO4J"`. They are never sent
  in protocol messages or printed. Literal values
  (`Connection.PostgreSql(url: ...)`) are passed to the engine through private
  environment variables.
- **Errors** are `RtoGtoRException` with a stable `Code` (`ErrorCodes.Map005`,
  …), `Severity` and `Retryable`. Invalid mappings list every problem in
  `Diagnostics`.
- **Resuming:** if a run fails, is interrupted, or is cancelled, the exception
  (or `OperationCanceledException.InnerException`) carries the `Report`. Pass
  `report.RunId` as `RunOptions.ResumeRunId`. Committed batches are not
  repeated, and nothing is duplicated.
- **Progress:** set `RunOptions.Progress` to an `IProgress<Progress>`.
- **Typed model:** plans, reports, run state and mappings are generated from
  the specification's JSON Schemas (`RtoGtoR.Model`).

## Engine binary

The SDK looks for the engine, in order:

1. `EngineOptions.EnginePath`
2. the `RTOGTOR_ENGINE_PATH` environment variable
3. the binary bundled for your platform

## Learn more

- [Migration guide](https://github.com/TuanFaied/rtogtor/blob/main/docs/migration-guide.md)
- [Recovery guarantees](https://github.com/TuanFaied/rtogtor/blob/main/docs/recovery.md)
- [Known limitations](https://github.com/TuanFaied/rtogtor/blob/main/docs/limitations.md)

License: Apache-2.0.
