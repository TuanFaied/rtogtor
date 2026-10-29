# Engine ↔ SDK protocol, version 1

- **Spec version:** 0.1.0 (draft)
- **Protocol version:** `1.0`
- **Machine-readable:** [`protocol.schema.json`](protocol.schema.json)

The Python, TypeScript and C# SDKs run the engine binary as a child process
(`rtogtor serve --stdio`) and exchange JSON messages with it, one per line
(JSON Lines, UTF-8, `\n`-terminated) on stdin and stdout. The engine writes
nothing else to stdout; diagnostics go to stderr.

## Messages

Every message carries `protocolVersion`. Each line is one complete JSON
object of at most 64 MiB.

| Direction | Message | Shape |
|---|---|---|
| SDK → engine | request | `{"protocolVersion","id","method","params"}` |
| engine → SDK | response | `{"protocolVersion","id","result"}` or `{"protocolVersion","id","error"}` |
| engine → SDK | event | `{"protocolVersion","id","event","data"}` |

- `id` is a string chosen by the SDK, unique among its in-flight requests.
  Responses and events carry the `id` of the request they belong to.
- Each request gets **exactly one** response. Events for a request are sent
  before its response.
- Requests may be in flight concurrently; responses may arrive in any order.

### Versioning

A message whose `protocolVersion` has an unsupported **major** version is
answered with error `RTG-PROTO-001` (when it has an `id`) and otherwise
ignored. Minor versions only add optional fields, methods and events;
receivers ignore fields they do not know.

## Session

1. The SDK starts the engine and sends `handshake` first. Any other request
   before a successful handshake fails with `RTG-PROTO-006`.
2. The SDK checks the engine version in the handshake result against its
   supported range and refuses to continue on a mismatch.
3. The SDK sends `shutdown` (or closes stdin) to end the session. The
   engine cancels in-flight requests, answers them with `RTG-EXEC-004` or
   their result, and exits. The SDK kills the process if it has not exited
   within a few seconds.

## Credentials

Credentials **never** appear in messages. A connection names environment
variables that hold them; the SDK sets those variables in the engine
process's environment (or the engine inherits them).

```json
{"product": "postgresql", "urlEnv": "SOURCE_URL"}
{"product": "neo4j", "urlEnv": "NEO4J_URL"}
{"product": "neo4j", "uri": "neo4j://localhost:7687", "userEnv": "NEO4J_USER", "passwordEnv": "NEO4J_PASSWORD"}
```

A `uri` field must not contain a user name or password (`RTG-PROTO-004`).

## Errors

```json
{"code": "RTG-MAP-005", "severity": "error", "retryable": false,
 "message": "...", "path": "/entities/3/key/1",
 "diagnostics": [ ...every problem, when there are several... ]}
```

Codes come from [`error-codes.json`](error-codes.json). SDKs expose
`code`, `severity` and `retryable` on their error types and never invent
codes.

## Methods

| Method | Params | Result | Events |
|---|---|---|---|
| `handshake` | `client: {name, version}`, `protocolVersions: [string]` | `engineVersion`, `specVersion`, `identityAlgorithm`, `protocolVersion` | — |
| `validateMapping` | `mapping` (object) | `valid`, `diagnostics`, `mappingHash`? | — |
| `inspect` | `source` (connection), `schemas`? | schema metadata ([`schema-metadata.schema.json`](schema-metadata.schema.json)) | — |
| `plan` | `mapping`, and either `source` (connection) or `sourceMetadata` (document) | plan ([`plan.schema.json`](plan.schema.json)) | — |
| `migrate` | `mapping`, `source`, `destination`?, `dryRun`?, `batchSize`?, `resumeRunId`?, `stateDir`? | report ([`report.schema.json`](report.schema.json)) | `progress` |
| `verify` | `runId`, `source`, `destination`, `stateDir`? | verification (the report's `verification`) | — |
| `report` | `runId`, `stateDir`? | report | — |
| `runState` | `runId`, `stateDir`? | run state ([`run-state.schema.json`](run-state.schema.json)) | — |
| `failures` | `runId`, `stateDir`? | `{failures: [failure]}` | — |
| `identity` | `entityType`, `key: [{type, value}]` | `rtgId`, `canonicalKey`, `serializationHex` | — |
| `canonicalize` | `type`, `value` | `canonical` | — |
| `cancel` | `requestId` | `{}` | — |
| `shutdown` | — | `{}` | — |

Notes:

- `plan` with `sourceMetadata` connects to nothing; it is how conformance
  cases check plans without a database.
- `migrate` returns the report also when the run fails or is interrupted:
  the response is then an **error** whose `report` field holds the report,
  so the SDK can offer `resumeRunId`.
- `identity` and `canonicalize` let SDKs and conformance harnesses use the
  engine's value and identity rules; SDKs must not reimplement them.
- `cancel` cancels the request with `requestId`; that request then fails
  with `RTG-EXEC-004` (or completes, if it finished first).

### `progress` event

```json
{"protocolVersion": "1.0", "id": "7", "event": "progress",
 "data": {"stepId": "nodes:Customer", "batch": 3, "read": 3000, "written": 2998, "failed": 2, "done": false}}
```

## Example session

```text
→ {"protocolVersion":"1.0","id":"1","method":"handshake","params":{"client":{"name":"RtoGtoR.NET","version":"0.1.0"},"protocolVersions":["1.0"]}}
← {"protocolVersion":"1.0","id":"1","result":{"engineVersion":"0.1.0-dev","specVersion":"0.1.0","identityAlgorithm":"rtg-id/v1","protocolVersion":"1.0"}}
→ {"protocolVersion":"1.0","id":"2","method":"canonicalize","params":{"type":"decimal","value":"0012.500"}}
← {"protocolVersion":"1.0","id":"2","result":{"canonical":"12.5"}}
→ {"protocolVersion":"1.0","id":"3","method":"shutdown","params":{}}
← {"protocolVersion":"1.0","id":"3","result":{}}
```
