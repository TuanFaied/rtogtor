# Error codes

- **Spec version:** 0.1.0 (draft)
- **Source of truth:** [`error-codes.json`](error-codes.json). This page
  describes the format and summarizes the codes; the JSON file holds the
  full meaning and remediation text that tools and SDKs use.

## Format

```text
RTG-<AREA>-<NNN>
```

- `AREA` is an uppercase name for the subsystem (below).
- `NNN` is a three-digit number, unique within the area.
- Codes are **never reused or renumbered**. A code that is no longer emitted
  stays in the file with `"deprecated": true`.
- SDKs expose the code on their error/exception types and **never invent
  codes** of their own.

Each code defines:

| Field | Meaning |
|---|---|
| `severity` | `info`, `warning`, `error` (the operation fails) or `fatal` (the engine cannot continue) |
| `retryable` | Whether retrying the same operation unchanged may succeed (for example, a dropped connection) |
| `meaning` | What happened |
| `remediation` | What the user can do about it |

## Areas

| Area | Subsystem |
|---|---|
| `SPEC` | Specification and document versions |
| `MAP` | Mapping loading and validation |
| `VAL` | Value encoding (`values.md`) |
| `ID` | Identity derivation (`identity.md`) |
| `SCHEMA` | Schema inspection |
| `CONN` | Connections |
| `PG` | PostgreSQL adapter |
| `PROTO` | Engine ↔ SDK protocol |
| `CLI` | Command-line usage |
| `INTERNAL` | Engine bugs |

Areas reserved for later milestones: `PLAN`, `EXEC`, `VERIFY`, `RUN` (run
state), `NEO` (Neo4j adapter).

## Codes in v0.1

| Code | Severity | Retryable | Title |
|---|---|---|---|
| `RTG-SPEC-001` | error | no | Unsupported specification version |
| `RTG-MAP-001` | error | no | Mapping does not match the schema |
| `RTG-MAP-002` | error | no | Mapping cannot be read or parsed |
| `RTG-MAP-003` | error | no | Duplicate entity name |
| `RTG-MAP-004` | error | no | Unknown entity reference |
| `RTG-MAP-005` | error | no | Key field not mapped |
| `RTG-MAP-006` | error | no | Key field type not allowed in identity |
| `RTG-MAP-007` | error | no | Foreign key does not match the referenced key |
| `RTG-MAP-008` | error | no | Duplicate property name |
| `RTG-MAP-009` | error | no | Duplicate column mapping |
| `RTG-MAP-010` | warning | no | Direction not yet implemented |
| `RTG-MAP-011` | error | no | Duplicate relationship name |
| `RTG-MAP-012` | error | no | Reserved property name |
| `RTG-VAL-001` | error | no | Invalid value for logical type |
| `RTG-VAL-002` | error | no | Unknown or unsupported logical type |
| `RTG-ID-001` | error | no | Null identity component |
| `RTG-ID-002` | error | no | Type not permitted in identity |
| `RTG-ID-003` | error | no | Empty identity input |
| `RTG-SCHEMA-001` | warning | no | Unsupported column type |
| `RTG-SCHEMA-002` | warning | no | Table has no primary key |
| `RTG-SCHEMA-003` | error | no | Schema not found |
| `RTG-CONN-001` | error | yes | Cannot connect to the database |
| `RTG-CONN-002` | error | no | Connection settings missing or invalid |
| `RTG-PG-001` | error | no | PostgreSQL query failed |
| `RTG-PROTO-001` | error | no | Unsupported protocol version |
| `RTG-CLI-001` | error | no | Invalid command-line usage |
| `RTG-INTERNAL-001` | fatal | no | Internal error |

The engine test suite checks that this table and `error-codes.json` list the
same codes.
