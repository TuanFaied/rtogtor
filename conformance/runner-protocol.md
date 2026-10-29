# Conformance runner protocol

Every SDK ships a harness that runs the shared conformance data **through its
public API** and reports the results in one format. A language is announced as
supported for a spec version only when its harness reports every case as
`passed`.

## Cases

| Case ID | Input | Check |
|---|---|---|
| `vectors/identity/<name>` | an entry of `vectors/identity.json` → `vectors` | the engine's `identity` result has exactly `rtgId`, `serializationHex` and the `canonicalKey` values |
| `vectors/identity/invalid/<name>` | an entry of `vectors/identity.json` → `invalid` | the SDK raises its error type with exactly `errorCode` |
| `vectors/values/valid/<NNN>-<type>` | the NNN-th (0-based, three digits) entry of `vectors/values.json` → `valid` | `canonicalize` returns exactly `canonical` |
| `vectors/values/invalid/<NNN>-<type>` | the NNN-th entry of `vectors/values.json` → `invalid` | the SDK raises its error type with exactly `errorCode` |
| `cases/ecommerce/plan` | `examples/ecommerce/mapping.yaml` loaded by the SDK's mapping loader; `cases/ecommerce/expected-schema-metadata.json` | planning against the metadata returns a plan whose **typed model**, serialized and canonicalized with RFC 8785, equals `cases/ecommerce/expected-plan.json` byte for byte |

The plan case checks three things at once: the SDK's YAML reading matches the
engine's (the mapping hash is part of the plan), the protocol transports the
plan intact, and the SDK's generated types lose no field.

## Results

When the environment variable `RTOGTOR_CONFORMANCE_OUT` is set, the harness
writes a JSON document to that path:

```json
{
  "runnerProtocol": "1",
  "sdk": "dotnet",
  "sdkVersion": "0.1.0",
  "engineVersion": "0.1.0-dev",
  "specVersion": "0.1.0",
  "results": [
    {"case": "cases/ecommerce/plan", "status": "passed", "detail": null}
  ]
}
```

- `results` is ordered by `case` (ordinal string order).
- `status` is `passed` or `failed`; `detail` may hold the actual value or
  error code for diagnosis.
- CI uploads the file as an artifact for each SDK.

## Harnesses

| SDK | Harness | Command |
|---|---|---|
| Go (engine) | `engine/ir/vectors_test.go`, `engine/planning/build_test.go`, `engine/protocol/server_test.go` | `go test ./...` in `engine/` |
| C# | `sdks/dotnet/tests/RtoGtoR.Tests/ConformanceTests.cs` | `dotnet test` in `sdks/dotnet/` |
| Python | `sdks/python/tests/test_conformance.py` | `pytest` in `sdks/python/` |
| TypeScript | `sdks/typescript/tests/conformance.test.ts` | `npm test` in `sdks/typescript/` |
