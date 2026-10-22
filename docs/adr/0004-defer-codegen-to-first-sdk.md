# ADR-0004: Build the codegen pipeline with the first SDK

- **Status:** Accepted
- **Date:** 2026-10-22

## Context

The project specification places the codegen pipeline (JSON Schemas → types
for Go, Python, TypeScript and C#) in Milestone 1. When Milestone 1 reached
that step:

- No SDK exists yet. Python, TypeScript and C# output would have no package
  to live in and nothing that compiles or tests it.
- The schemas are still drafts and will change during Milestone 2 (plan,
  report and run-state schemas do not exist yet).
- The Go engine's types are hand-written, but tests already validate their
  JSON output against the schemas (`schemameta`), and the mapping loader
  validates every input against the schema before decoding it, so drift is
  caught.

Generating code nobody consumes would add tooling to maintain (four
generators across four ecosystems) without verifying anything.

## Decision

- The codegen pipeline (`tools/codegen/`) and its CI drift check are built in
  **Milestone 3**, together with the first SDK (C#), where generated types
  are compiled, used and tested immediately.
- Until then, the Go engine keeps hand-written types, and every schema-backed
  type must have a test that validates its JSON against the schema.
- Milestone 3 also decides whether the engine switches to generated Go types.
- The CI check that embedded spec files match `specs/` (already in place)
  remains the guard against spec drift inside the engine.

## Consequences

- ✅ No unused generated code or idle generators in the repository.
- ✅ The generator choice is validated against real SDK code.
- ⚠️ Milestone 1 completes without codegen; Milestone 3 grows accordingly.
- ⚠️ Hand-written Go types depend on schema-validation tests to stay aligned.
