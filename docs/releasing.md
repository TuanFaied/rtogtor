# Releasing

Releases are automated by [`.github/workflows/release.yml`](../.github/workflows/release.yml)
with [release-please](https://github.com/googleapis/release-please). Nothing
is published until the one-time setup below is done, and every publishing
job waits for approval in the `release` environment.

## Components and tags

| Component | Path | Tag | Published to |
|---|---|---|---|
| Engine and Go API | `engine/` | `engine/v1.2.3` (the Go submodule tag) | Go module proxy; binaries, checksums and SBOM on the GitHub release |
| .NET SDK | `sdks/dotnet/` | `sdk-dotnet/v1.2.3` | nuget.org `RtoGtoR` |
| Python SDK | `sdks/python/` | `sdk-python/v1.2.3` | PyPI `rtogtor` (five platform wheels) |
| TypeScript SDK | `sdks/typescript/` | `sdk-typescript/v1.2.3` | npm `@rtogtor/sdk` and five `@rtogtor/sdk-<platform>` packages |

The spec version (`engine/spec/spec.go`) and protocol version change by hand,
together with [`specs/compatibility.md`](../specs/compatibility.md) and the
SDKs' supported engine ranges; they are not released on their own.

## How a release happens

1. Commits on `main` use [Conventional Commits](https://www.conventionalcommits.org/)
   (`feat:`, `fix:`, `perf:`, `security:`; `feat!:` or a `BREAKING CHANGE:`
   footer for a major version).
2. release-please keeps one release pull request open. It bumps the
   versions of the changed components (including `EngineVersion`, the
   NuGet `VersionPrefix`, `pyproject.toml`, `package.json` with the platform
   packages, and `SDK_VERSION`) and writes each component's `CHANGELOG.md`.
3. Merging that pull request tags the components and creates GitHub
   releases. The same workflow run then, per released component:
   - builds from the tag (the engine for five platforms, `-trimpath`, no
     cgo);
   - smoke-tests the installed package with its bundled engine;
   - creates [build provenance attestations](https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations)
     signed with Sigstore, and an SPDX SBOM for the engine;
   - publishes with OIDC trusted publishing (no stored registry tokens) and
     attaches the artifacts to the GitHub release.

The first release is pinned to **1.0.0** for every component by
`release-as` in [`release-please-config.json`](../release-please-config.json).
Remove those four `release-as` lines after it is published, so later
releases follow the commits.

## One-time setup (repository owner)

Do these before merging the first release pull request:

1. **Reserve the package names**: `RtoGtoR` on nuget.org, `rtogtor` on PyPI,
   and the `@rtogtor` scope on npm (create the organization). The platform
   packages `@rtogtor/sdk-{linux-x64,linux-arm64,darwin-x64,darwin-arm64,win32-x64}`
   are created on first publish.
2. **Create the `release` environment** (Settings → Environments) with
   required reviewers, and restrict it to the `main` branch and the
   component tags.
3. **Trusted publishers**, each bound to this repository, workflow
   `release.yml` and environment `release`:
   - PyPI: add a pending trusted publisher for project `rtogtor`.
   - npm: configure trusted publishing for `@rtogtor/sdk` and each platform
     package (or publish 1.0.0 once by hand, then configure).
   - nuget.org: add a trusted publishing policy, and set the repository
     variable `NUGET_USER` to the nuget.org account name.
4. **Allow release-please to open pull requests**: Settings → Actions →
   General → "Allow GitHub Actions to create and approve pull requests".
5. **Protect `main`** and require the engine, SDK, codegen and examples
   workflows to pass.

## Verifying a release

```bash
gh release download engine/v1.0.0 --repo TuanFaied/rtogtor
sha256sum --check SHA256SUMS
gh attestation verify rtogtor-linux-amd64 --repo TuanFaied/rtogtor
```

npm packages also carry npm provenance (`npm audit signatures`), and PyPI
files carry PyPI attestations from trusted publishing.

## Version numbers between releases

Between releases, the source carries the last released version (or
`1.0.0-dev` before the first one). `rtogtor version` also prints the commit,
so development builds are distinguishable.
