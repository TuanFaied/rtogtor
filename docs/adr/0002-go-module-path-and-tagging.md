# ADR-0002: Go module path and release tagging

- **Status:** Accepted
- **Date:** 2026-10-21

## Context

The repository is a monorepo (`specs/`, `engine/`, `sdks/`, `conformance/`).
The original specification named the Go import path
`github.com/<org>/rtogtor` while placing the Go module in `engine/`. Those two
choices conflict: a module in a subdirectory gets that subdirectory in its
path.

Options:

1. Put `go.mod` at the repository root — the import path stays short, but Go
   tooling then treats the whole monorepo (including SDK folders) as one
   module.
2. Keep the module in `engine/` with path
   `github.com/TuanFaied/rtogtor/engine`.
3. Use a vanity import domain that redirects to the subdirectory — requires
   hosting and a domain.

## Decision

Option 2.

- Module path: **`github.com/TuanFaied/rtogtor/engine`**.
- The package in `engine/` is declared **`package rtogtor`**, so callers write
  `rtogtor.NewMigration(...)` and the documented API reads naturally:

  ```go
  import "github.com/TuanFaied/rtogtor/engine"
  ```

- Engine releases are tagged **`engine/vX.Y.Z`**. Go only recognizes version
  tags carrying the module's subdirectory prefix; a plain `vX.Y.Z` tag is
  ignored by the module proxy.
- Other components use their own tag prefixes (`spec/v…`, `python/v…`,
  `typescript/v…`, `dotnet/v…`), matching independent versioning.
- A future major version 2 of the engine becomes
  `github.com/TuanFaied/rtogtor/engine/v2`.
- CLI install:

  ```bash
  go install github.com/TuanFaied/rtogtor/engine/cmd/rtogtor@latest
  ```

## Consequences

- ✅ The engine is an isolated module; SDK folders never affect Go builds.
- ✅ Independent per-component versioning maps directly onto tag prefixes.
- ⚠️ The package name (`rtogtor`) differs from the last path element
  (`engine`). Some linters flag this; it is intentional and documented.
- ⚠️ Release automation must create prefixed tags (release-please supports
  this per component).
