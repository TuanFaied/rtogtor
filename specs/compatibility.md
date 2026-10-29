# Compatibility

- **Spec version:** 1.0.0

RtoGtoR has four independently versioned parts, each following
[Semantic Versioning](https://semver.org/):

| Part | Version | Where it is declared |
|---|---|---|
| Specification | `specVersion` | Every mapping, plan, report, run state and schema-metadata document |
| Protocol | `protocolVersion` (major.minor) | Every engine ↔ SDK message ([protocol.md](protocol.md)) |
| Engine | `engineVersion` | `rtogtor version`, the protocol handshake |
| SDKs | package version | NuGet `RtoGtoR`, PyPI `rtogtor`, npm `@rtogtor/sdk` |

## Matrix

| Engine | Spec it writes | Spec documents it accepts | Protocol | Identity algorithm |
|---|---|---|---|---|
| 1.0.x | 1.0.0 | 1.0.x, 0.1.x | 1.0 | `rtg-id/v1` |
| 0.1.0-dev (pre-release) | 0.1.0 | 0.1.x | 1.0 | `rtg-id/v1` |

| SDK | Engines it starts | Spec documents it accepts | Protocol | Bundled engine |
|---|---|---|---|---|
| .NET `RtoGtoR` 1.0.x | ≥ 1.0.0-0, < 2.0.0 | 1.0.x, 0.1.x | 1.x | The engine of the same release |
| Python `rtogtor` 1.0.x | ≥ 1.0.0-0, < 2.0.0 | 1.0.x, 0.1.x | 1.x | The engine of the same release |
| TypeScript `@rtogtor/sdk` 1.0.x | ≥ 1.0.0-0, < 2.0.0 | 1.0.x, 0.1.x | 1.x | The engine of the same release |
| Go (`github.com/TuanFaied/rtogtor/engine`) | — (it is the engine) | as the engine | — | — |

A component is listed for a spec version only after it passes the
conformance suite for that version:

| Component | Identity vectors | Value vectors | `cases/ecommerce/plan` | `cases/ecommerce-reverse/plan` |
|---|---|---|---|---|
| Go engine 1.0.x | ✅ | ✅ | ✅ | ✅ |
| .NET SDK 1.0.x | ✅ | ✅ | ✅ | ✅ |
| Python SDK 1.0.x | ✅ | ✅ | ✅ | ✅ |
| TypeScript SDK 1.0.x | ✅ | ✅ | ✅ | ✅ |

Every SDK checks the engine version during the handshake and refuses an
engine outside its range (`RTG-PROTO-007`). Each SDK release bundles the
engine of the same release, so the range matters only when
`RTOGTOR_ENGINE_PATH` points at another binary.

## Rules

### Specification

- An implementation of spec `M.m` accepts documents declaring `M.n.p` with
  `n ≤ m`, and rejects others with `RTG-SPEC-001` before validating them.
  Minor versions only add optional fields, step kinds, error codes and
  enum values that older documents do not use.
- **0.1.x is accepted by every 1.x implementation.** 0.1.0 was the draft
  that became 1.0.0 without schema changes, so mappings written for it keep
  working; only the declared version differs. A mapping's hash covers its
  `specVersion`, so changing the declaration of a mapping changes its hash
  and runs started with the old declaration cannot be resumed with the new
  one (`RTG-RUN-002`).
- Documents an implementation writes (plans, reports, run state, schema
  metadata) always declare the implementation's own spec version.
- Error codes are never reused or renumbered. Deprecated codes stay defined.
- `rtg-id/v1` never changes. A different identity derivation would be a new
  algorithm name (`rtg-id/v2`), declared in every plan.

### Protocol

- Engines and SDKs agree on a protocol version during the handshake. A
  different major version is refused (`RTG-PROTO-001`); minor versions only
  add methods and optional parameters.

### Engine and SDKs

- Within a major version, a newer engine works with every older SDK of the
  same major version.
- An SDK never implements behavior the spec does not define; a feature is
  supported in an SDK only after it passes the conformance suite
  ([conformance/README.md](../conformance/README.md)).

### Run state

- A run can be resumed by any engine of the same major version that
  accepts its run state's spec version, with the identical mapping.
