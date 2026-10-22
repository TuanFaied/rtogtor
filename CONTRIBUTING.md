# Contributing to RtoGtoR

Thanks for your interest in RtoGtoR! This guide explains how the project is
organized and what we expect from changes.

## Ground rules

1. **The specification comes first.** Behavior is defined in [`specs/`](specs/).
   If a change alters behavior (value encoding, identity, mapping semantics,
   error codes, report formats), change the spec first, in the same pull
   request, then the implementation.
2. **No SDK defines semantics.** Migration, planning and identity logic live
   only in the Go engine. SDKs are thin clients.
3. **Never silently lose data.** Any conversion that narrows precision or
   changes representation must go through the loss policy and be reported.
4. **Tests ship with features.** Do not skip, disable or loosen failing tests.
5. **No secrets.** Never commit credentials, and never log, print or store them
   in plans, reports or run state.
6. **Honest capabilities.** Do not mark a feature as supported until its tests
   (and, for SDKs, the conformance suite) pass.

## Development setup

| Component | Requirements |
|---|---|
| Engine | Go 1.25+ (latest patch release) |
| Integration tests | Docker (see [docs/development.md](docs/development.md)) |

```bash
cd engine
go test ./...
go vet ./...
gofmt -l .
```

If you change any file under `specs/` that the engine embeds, run:

```bash
cd engine
go generate ./...
```

The `TestEmbeddedSpecsMatchSource` test fails if the embedded copies drift
from `specs/`.

## Error codes

Error codes (`RTG-<AREA>-<NNN>`) are defined in
[`specs/error-codes.json`](specs/error-codes.json) and documented in
[`specs/error-codes.md`](specs/error-codes.md). Codes are **never reused or
renumbered**; deprecate a code instead of deleting it.

## Pull requests

- Keep pull requests focused and small.
- Use [Conventional Commits](https://www.conventionalcommits.org/) for commit
  titles (`feat(engine): …`, `fix(spec): …`, `docs: …`).
- Describe *why* the change is needed and how it was tested.
- Significant design decisions get an ADR in [`docs/adr/`](docs/adr/).

## Licensing

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
