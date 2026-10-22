# TypeScript SDK

Package `@rtogtor/sdk` (npm), Node.js 22+, ESM. Contributor notes; user
documentation lives in the [package README](../../sdks/typescript/README.md).

## Use

```typescript
import { Connection, Mapping, Migration } from "@rtogtor/sdk";

const mapping = await Mapping.fromFile("mapping.yaml");
await using m = new Migration({
  source: Connection.postgresql({ urlEnv: "SOURCE_URL" }),
  destination: Connection.neo4j({ uri: "bolt://localhost:7687", authEnv: "NEO4J" }),
  mapping,
});
const plan = await m.plan();
const report = await m.run({ dryRun: false, signal: AbortSignal.timeout(3_600_000) });
```

## Choices per the specification

| Concern | Choice |
|---|---|
| Typed model | Generated interfaces whose property names are the schemas' JSON names, so documents are plain objects |
| `int64` | The specification's suggestion was `bigint`. Value-level `int64` and `decimal` are **strings** in every RtoGtoR document, so they are `string` here and lose nothing; convert with `BigInt(s)`. Schema integers (counts, batch numbers) are `number`, bounded far below 2^53 |
| Errors | `RtoGtoRError` with `code`, `severity`, `retryable`, `path`, `diagnostics`, `report` |
| Cancellation | `AbortSignal`. The rejection is the signal's reason, with the engine's `RtoGtoRError` as `cause` |
| Disposal | `Symbol.asyncDispose` (`await using`) and `close()`. `await using` needs TypeScript or Node.js 24+ |
| Progress | `onProgress` callback |
| YAML | `yaml` with the YAML 1.2 core schema and unique keys. JSON is read with the same parser, so duplicate keys are rejected |
| Schema validation | Ajv (draft 2020-12) |
| Secrets | `Connection` hides credentials from `toString`, `JSON.stringify` and `util.inspect` |

## Develop

From `sdks/typescript/`:

```bash
npm ci
npm run build:engine     # engine for this machine → engine-bin/ (tests use it)
npm run typecheck && npm run lint && npm test
```

## Package (esbuild pattern)

```bash
node scripts/pack.mjs    # @rtogtor/sdk + @rtogtor/sdk-{linux,darwin}-{x64,arm64}, -win32-x64 → ../../dist/npm
node scripts/smoke.mjs   # installs them into a fresh project and runs the bundled engine
```

`@rtogtor/sdk` lists the platform packages as `optionalDependencies`. Each
one declares `os` and `cpu`, so npm installs only the matching binary. Pack
releases on Linux or macOS so the binaries keep their execute bit; the SDK
also restores it at run time if needed.
