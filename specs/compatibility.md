# Compatibility matrix

Which component versions support which specification and protocol versions.
A component is listed for a spec version only after it passes the conformance
suite for that version.

## Specification

| Spec version | Status | Identity algorithm |
|---|---|---|
| 0.1.0 | Draft | `rtg-id/v1` |

## Engine

| Engine version | Spec versions | Protocol versions | Status |
|---|---|---|---|
| 0.1.0-dev | 0.1.x | 1.0 | In development |

## SDKs

| SDK | Version | Spec versions | Engine range | Conformance |
|---|---|---|---|---|
| Go (engine library) | 0.1.0-dev | 0.1.x | same module | Identity + value vectors and the e-commerce plan case pass |
| C# (`RtoGtoR`) | 0.1.0-dev | 0.1.x | `>= 0.1.0-0, < 0.2.0`, protocol 1.x | All identity, value and plan cases pass |
| Python (`rtogtor`) | 0.1.0.dev0 | 0.1.x | `>= 0.1.0-0, < 0.2.0`, protocol 1.x | All identity, value and plan cases pass |
| TypeScript (`@rtogtor/sdk`) | 0.1.0-dev.0 | 0.1.x | `>= 0.1.0-0, < 0.2.0`, protocol 1.x | All identity, value and plan cases pass |
