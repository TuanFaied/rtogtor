# ADR-0005: A project-owned code generator for SDK types

- **Status:** Accepted
- **Date:** 2026-10-23

## Context

SDK types must be generated from the JSON Schemas in `specs/`, never written
by hand. The project specification suggested one third-party generator per
language: `go-jsonschema`, `datamodel-code-generator`,
`json-schema-to-typescript` and NJsonSchema.

Our schemas use draft 2020-12 features: `$defs`, cross-file `$ref` (reports
reference plan and run-state definitions), and `oneOf` unions (plan steps,
connections). Generator support for these varies between tools. Four
generators would also mean four toolchains in CI and four sets of naming
rules. That works against "the specification is the product": the same
schema should produce the same shapes in every language.

## Decision

- `tools/codegen` (Go, no dependencies) reads `specs/*.json` into one
  language-neutral model of classes and typed fields, then emits each SDK
  language from that model. Milestone 3 emits C#; Milestone 4 adds Python
  and TypeScript emitters.
- Model rules, the same for every language:
  - every object definition becomes a class named after its `$defs` key or
    its location; names shared by two schemas get a schema prefix;
  - a `oneOf` of object variants becomes one class whose variant-specific
    fields are optional;
  - `int64` and `decimal` *values* are strings in the schemas and stay
    strings, so no language loses precision;
  - validation-only keywords (`if`/`then`, `not`, patterns, bounds) do not
    affect types. SDKs validate documents against the embedded schemas
    instead.
- Error codes are generated from `specs/error-codes.json` into constants and
  a lookup table.
- CI regenerates the files and fails if any generated file differs
  (`go -C tools/codegen run . -repo ../.. -check`).
- The Go engine keeps hand-written types (ADR-0004's open question). Tests
  validate every document the engine emits against the schemas, which catches
  drift without the cost of generated Go types inside the engine.

## Consequences

- ✅ One set of naming and typing rules for every SDK; one tool in CI.
- ✅ Full control over 2020-12 constructs we actually use. Unsupported
  constructs fail the generator loudly instead of producing wrong types.
- ⚠️ We maintain a small generator (about 900 lines of Go) instead of reusing tools.
- ⚠️ Schema authors should name reusable shapes in `$defs`, which gives
  better generated names (done for report and run-state in this milestone).
