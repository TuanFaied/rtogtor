<!--
Title: use Conventional Commits, e.g. `feat(engine): …`, `fix(spec): …`, `docs: …`.
Keep pull requests focused and small. See CONTRIBUTING.md.
-->

## Summary

<!-- What does this change do, and why is it needed? -->

Closes #

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Specification change
- [ ] Refactor / internal change (no behavior change)
- [ ] Documentation
- [ ] CI / tooling

## How was this tested?

<!-- Commands run, new or updated tests, integration tests against which database versions. -->

## Checklist

- [ ] `go test ./...`, `go vet ./...` and `gofmt -l .` pass in `engine/`
- [ ] Tests are added or updated; no tests were skipped, disabled or loosened
- [ ] If behavior changed, `specs/` is updated in this PR, before the implementation
- [ ] If files under `specs/` changed, I ran `go generate ./...` in `engine/`
- [ ] New error codes are added to `specs/error-codes.json` and `specs/error-codes.md`; no codes were reused or renumbered
- [ ] Any lossy conversion goes through the loss policy and is reported
- [ ] No credentials or secrets are committed, logged, or written to plans, reports or run state
- [ ] The README capabilities table is updated only for features whose tests pass
- [ ] Significant design decisions have an ADR in `docs/adr/`
- [ ] `CHANGELOG.md` is updated for user-visible changes

## Notes for reviewers

<!-- Anything that needs special attention, follow-ups, or known limitations. -->
