# RtoGtoR specification

This directory is the **single source of truth** for every RtoGtoR component.
The engine and all SDKs implement what is written here, and nothing more.

- **Current spec version:** `1.0.0`. Documents declaring `0.1.x` (the draft) remain valid; see [compatibility.md](compatibility.md).
- Versioned independently of the engine and SDKs (semver): minor versions
  only add; see [compatibility.md](compatibility.md).

| Document | Status | Defines |
|---|---|---|
| [`values.md`](values.md) | Stable | Canonical value encoding and loss kinds |
| [`identity.md`](identity.md) | Stable | Byte-exact `rtg_id` derivation (`rtg-id/v1`) |
| [`error-codes.json`](error-codes.json) / [`.md`](error-codes.md) | Stable | Stable error codes |
| [`mapping.schema.json`](mapping.schema.json) | Stable | Mapping definitions (both directions) |
| [`schema-metadata.schema.json`](schema-metadata.schema.json) | Stable | Output of `rtogtor inspect` |
| [`ir.schema.json`](ir.schema.json) | Informative | Canonical intermediate representation |
| [`compatibility.md`](compatibility.md) | Stable | Spec × engine × SDK version matrix |
| [`plan.schema.json`](plan.schema.json) | Stable | Execution plan output |
| [`report.schema.json`](report.schema.json) | Stable | Migration and verification reports |
| [`run-state.schema.json`](run-state.schema.json) | Stable | Runs, checkpoints, failed records |
| [`protocol.md`](protocol.md) / [`protocol.schema.json`](protocol.schema.json) | Stable (protocol 1.0) | Engine ↔ SDK messages |

Shared test data that every implementation must pass lives in
[`../conformance/`](../conformance/).

## Changing the spec

1. Edit the spec document first, in the same pull request as the code.
2. If the engine embeds the file (all `*.json` here), run
   `go generate ./...` in `engine/`, and regenerate SDK types with
   `go -C tools/codegen run . -repo ../..` from the repository root.
3. Add or update conformance vectors for any behavior change.
4. Breaking changes need an ADR in [`../docs/adr/`](../docs/adr/).
