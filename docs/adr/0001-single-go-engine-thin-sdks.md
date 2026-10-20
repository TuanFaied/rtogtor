# ADR-0001: A single Go engine with thin SDKs

- **Status:** Accepted
- **Date:** 2026-10-21

## Context

RtoGtoR must offer first-class SDKs for Go, Python, TypeScript and C# with
identical semantics. The hard parts of the system — planning, batching,
checkpoints, resume, retries, identity resolution and validation — are where
subtle bugs live. Four independent implementations would produce four sets of
bugs that disagree on edge cases, for example resuming after a mid-batch
failure.

Migration is a batch job. It does not need to run inside the caller's process.

## Options considered

| Option | Outcome | Reason |
|---|---|---|
| One Go engine + thin SDKs | **Chosen** | The hard logic is implemented once; a single static binary ships through every package manager |
| Shared native core + FFI bindings | Rejected | Every Go `c-shared` library carries its own runtime; cgo complicates builds, cross-compilation and debugging |
| Four native engines + a conformance suite | Rejected for migration | Four copies of the adapter, driver and recovery code. Reserved for future runtime interoperability, which must run in-process |

## Decision

- The migration engine is written **once, in Go**, and shipped as a single
  static binary (`rtogtor`).
- The Go SDK is the engine's own public library; Go callers use it in-process.
- Python, TypeScript and C# SDKs are thin, typed clients. They:
  - generate their types from the JSON Schemas in `specs/`;
  - validate mappings locally against those schemas;
  - launch the bundled engine binary and talk to it over a versioned
    JSON-lines protocol on stdin/stdout (`specs/protocol.schema.json`,
    Milestone 3);
  - contain **no** migration, planning or identity logic of their own.
- Every SDK passes the same conformance suite in `conformance/`.

## Consequences

- ✅ One implementation of the risky logic; bugs are fixed once.
- ✅ All SDKs behave identically by construction, and the conformance suite
  verifies it.
- ✅ The engine distributes as a plain binary (GitHub Releases, Homebrew, Docker)
  as well as inside each SDK package.
- ⚠️ Each SDK package must bundle platform-specific binaries
  (linux-x64, linux-arm64, darwin-x64, darwin-arm64, windows-x64), which
  makes packaging more involved.
- ⚠️ Non-Go SDKs pay a process start-up cost and need careful process
  lifecycle handling (cancellation, cleanup on dispose).
- ⚠️ A protocol version handshake is required so SDKs reject incompatible
  engines with a stable error code.
