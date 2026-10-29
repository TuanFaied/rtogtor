# Go SDK

The Go SDK is the engine's own library: `github.com/TuanFaied/rtogtor/engine`,
package `rtogtor` (ADR-0002). Go programs call it in-process; no subprocess is
involved.

```go
import "github.com/TuanFaied/rtogtor/engine"

src := rtogtor.PostgreSQLFromEnv("SOURCE_URL")
dst := rtogtor.Neo4j("neo4j://localhost:7687", rtogtor.BasicAuthFromEnv("NEO4J"))

m, err := rtogtor.LoadMapping("mapping.yaml")
if err != nil {
    // err is an rtgerr.List: every problem, each with a stable RTG-* code
}
mig := rtogtor.NewMigration(src, dst, m)
defer mig.Close()

plan, err := mig.Plan(ctx)
report, err := mig.Run(ctx, rtogtor.RunOptions{BatchSize: 1000})
if err != nil && report != nil {
    // resume later with rtogtor.RunOptions{ResumeRunID: report.RunID}
}
verification, err := mig.Verify(ctx, report.RunID, rtogtor.DefaultStateDir)
```

| Concept | Go |
|---|---|
| Connection | `PostgreSQL(url)`, `PostgreSQLFromEnv(name)`, `Neo4j(uri, auth)`, `Neo4jFromEnv(name)`; `String()` never prints credentials |
| Mapping | `LoadMapping(path)`, `DecodeMapping(data, format)` |
| Plan | `Migration.Plan(ctx)`, `PlanFromMetadata(mapping, metadata)` (offline) |
| Run | `Migration.Run(ctx, RunOptions)` returns `*Report` |
| Run state | `LoadRunState`, `LoadReport`, `LoadFailures` |
| Errors | `*rtgerr.Error` with `Code`, `Severity()`, `Retryable()`; `rtgerr.List` for several |
| Cancellation | `context.Context`. A cancelled run is `interrupted` and resumable |
| Progress | `RunOptions.Progress func(Progress)` |
