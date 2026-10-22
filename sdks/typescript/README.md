# @rtogtor/sdk

Migrate data from **PostgreSQL to Neo4j** with explicit mappings, plans you
can inspect before writing, resumable runs and verification.

> **Status: pre-alpha.** PostgreSQL → Neo4j only. See the
> [roadmap](https://github.com/TuanFaied/rtogtor/blob/main/docs/roadmap.md).

The SDK runs the RtoGtoR engine as a child process. npm installs the engine
for your platform automatically (`@rtogtor/sdk-<os>-<cpu>`). All migration
logic runs in the engine, which behaves identically for every RtoGtoR SDK.

## Install

```bash
npm install @rtogtor/sdk
```

Requires Node.js 22 or later. ESM only.

`await using` needs TypeScript (which compiles it) or Node.js 24+. In plain
JavaScript on Node.js 22, call `close()` in a `finally` block instead.

## Use

```ts
import { Connection, Mapping, Migration } from "@rtogtor/sdk";

const mapping = await Mapping.fromFile("mapping.yaml");
await using m = new Migration({
  source: Connection.postgresql({ urlEnv: "SOURCE_URL" }),
  destination: Connection.neo4j({ uri: "bolt://localhost:7687", authEnv: "NEO4J" }),
  mapping,
});
const plan = await m.plan(); // nothing is written
const report = await m.run({ dryRun: false, onProgress: (p) => console.log(p.stepId, p.written) });
const verification = await m.verify(report.runId);
```

- **Credentials** come from environment variables: `SOURCE_URL`, plus
  `NEO4J_USER` and `NEO4J_PASSWORD` for `authEnv: "NEO4J"`. They are never
  sent in protocol messages, printed, or serialized by `JSON.stringify`.
- **Errors** are `RtoGtoRError` with a stable `code` (`ErrorCodes.Map005`, …),
  `severity` and `retryable`. Invalid mappings list every problem in
  `diagnostics`.
- **Cancellation:** pass `signal` (an `AbortSignal`). The engine stops the
  request. For a run, the rejection's `cause` is the engine's `RtoGtoRError`,
  whose `report.runId` you can pass as `resumeRunId`. Committed batches are not
  repeated, and nothing is duplicated.
- **Numbers:** schema integers (counts) are `number`; they stay far below
  2^53. Value-level `int64` and `decimal` values are strings, exactly as in
  the specification. Convert with `BigInt(value)` when you need arithmetic.
- **Typed model:** plans, reports, run state and mappings are TypeScript
  types generated from the specification's JSON Schemas.

## CLI

```bash
npx rtogtor plan --mapping mapping.yaml --source-env SOURCE_URL
```

## Learn more

- [Migration guide](https://github.com/TuanFaied/rtogtor/blob/main/docs/migration-guide.md)
- [Recovery guarantees](https://github.com/TuanFaied/rtogtor/blob/main/docs/recovery.md)
- [Known limitations](https://github.com/TuanFaied/rtogtor/blob/main/docs/limitations.md)

License: Apache-2.0.
