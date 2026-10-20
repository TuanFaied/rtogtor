# Governance

RtoGtoR currently follows a **maintainer-led** model.

## Roles

- **Maintainer** — [@TuanFaied](https://github.com/TuanFaied). Reviews and
  merges pull requests, decides on the roadmap and specification changes, and
  manages releases.
- **Contributors** — anyone who opens issues, discussions or pull requests.

## Decisions

- Day-to-day changes are decided through pull-request review.
- Significant design decisions are recorded as ADRs in
  [`docs/adr/`](docs/adr/) and discussed in the pull request that adds them.
- Changes to [`specs/`](specs/) follow semantic versioning of `specVersion`:
  breaking changes require a major version bump and an ADR.

## Evolution

As the contributor base grows, the project intends to add maintainers per
component (engine, each SDK, specification) and document a lazy-consensus
process for spec changes here.
