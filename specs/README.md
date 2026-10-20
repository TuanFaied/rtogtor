# RtoGtoR specification

This directory is the **single source of truth** for every RtoGtoR component.
The engine and all SDKs implement what is written here, and nothing more.

- **Current spec version:** `0.1.0` (draft — may still change before release)
- Versioned independently of the engine and SDKs (semver). While the major
  version is `0`, a minor bump may be breaking.

| Document | Status | Defines |
|---|---|---|
| [`values.md`](values.md) | Draft | Canonical value encoding and loss kinds |
| [`identity.md`](identity.md) | Draft | Byte-exact `rtg_id` derivation (`rtg-id/v1`) |
| [`error-codes.json`](error-codes.json) / [`.md`](error-codes.md) | Draft | Stable error codes |
| [`mapping.schema.json`](mapping.schema.json) | Draft | Mapping definitions (both directions) |
| [`schema-metadata.schema.json`](schema-metadata.schema.json) | Draft | Output of `rtogtor inspect` |
| [`ir.schema.json`](ir.schema.json) | Early draft | Canonical intermediate representation |
| [`compatibility.md`](compatibility.md) | Draft | Spec × engine × SDK version matrix |
| `plan.schema.json` | Planned (M2) | Execution plan output |
| `report.schema.json` | Planned (M2) | Migration and verification reports |
| `run-state.schema.json` | Planned (M2) | Runs, checkpoints, failed records |
| `protocol.schema.json` | Planned (M3) | Engine ↔ SDK messages |

Shared test data that every implementation must pass lives in
[`../conformance/`](../conformance/).

## Changing the spec

1. Edit the spec document first, in the same pull request as the code.
2. If the engine embeds the file (all `*.json` here), run
   `go generate ./...` in `engine/`.
3. Add or update conformance vectors for any behavior change.
4. Breaking changes need an ADR in [`../docs/adr/`](../docs/adr/).
