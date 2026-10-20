# ADR-0003: Data-integrity defaults for specification v0.1

- **Status:** Accepted
- **Date:** 2026-10-21

## Context

The project specification leaves several choices open that change either the
`rtg_id` of stored entities or the data written to the destination. Once data
has been migrated with a given choice, changing it breaks idempotency (reruns
would create duplicates) or silently changes values. These must be fixed before
any implementation.

## Decisions

| # | Question | Decision | Normative text |
|---|---|---|---|
| 1 | Identity serialization | `rtg-id/v1` domain tag, then length-prefixed fields with 4-byte big-endian lengths | `specs/identity.md` |
| 2 | Type tags in identity | Short fixed ASCII tags (`i64`, `dec`, `str`, …), frozen per identity version | `specs/identity.md` |
| 3 | Decimal equality | Canonical decimals drop trailing fractional zeros (`1.50` → `1.5`); declared precision/scale stay in type metadata | `specs/values.md` |
| 4 | Canonical JSON | RFC 8785 (JSON Canonicalization Scheme) | `specs/values.md` |
| 5 | `timestamp` without time zone | Stays zone-less (`LocalDateTime` in Neo4j). Never assumed UTC | `specs/values.md` |
| 6 | SQL `NULL` → graph | Property omitted. Because the mapping declares every mapped column, the reverse direction restores an absent mapped property as `NULL`. Omissions are counted in reports | `specs/values.md` |
| 7 | Tables without a primary key | Rejected in strict mode (no stable identity, no safe keyset resume) | `specs/error-codes.md` (assigned when the planner lands) |
| 8 | Run-state storage | JSON files under `.rtogtor/` by default; SQLite deferred (avoids cgo) | `specs/run-state.schema.json` (Milestone 2) |
| 9 | Resume semantics | One destination transaction per batch; checkpoint recorded only after commit; replay is safe because writes `MERGE` on `rtg_id` | `docs/recovery.md` (Milestone 2) |

### Refinement of decision 6

An earlier draft proposed storing a per-node marker listing null properties.
That is unnecessary for relational → graph → relational round trips: a
relational row cannot "omit" a column, so an absent mapped property always
means `NULL`. No extra marker is stored. The distinction between `null` and
*absent* remains meaningful in the IR and the protocol, where both can occur.

### Floating-point values are never identity components

`float64` cannot be an identity component. Equality on floats is unreliable
(`0.1 + 0.2`), and different engines print floats differently.

## Consequences

- ✅ Identities are reproducible byte-for-byte in every language, and verified
  by the shared vectors in `conformance/vectors/identity.json`.
- ✅ No silent time-zone shifts or precision changes.
- ⚠️ Changing any identity rule requires a new identity version (`rtg-id/v2`)
  and a documented re-keying path; `v1` behavior can never change.
- ⚠️ Decimal key values that differ only in trailing zeros (`1.5` vs `1.50`)
  collapse to one identity. This matches PostgreSQL `numeric` equality.
