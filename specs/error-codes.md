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
| `NEO` | Neo4j adapter |
| `PLAN` | Planning |
| `EXEC` | Execution |
| `RUN` | Run state and resume |
| `VERIFY` | Verification |
| `PROTO` | Engine ↔ SDK protocol |
| `CLI` | Command-line usage |
| `INTERNAL` | Engine bugs |

## Codes

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
| `RTG-PG-002` | error | no | Row rejected by PostgreSQL |
| `RTG-PLAN-001` | error | no | Mapped table not found |
| `RTG-PLAN-002` | error | no | Mapped column not found |
| `RTG-PLAN-003` | error | no | Declared type does not match the source |
| `RTG-PLAN-004` | warning | no | Key column is nullable |
| `RTG-PLAN-005` | error | no | Lossy transformation not allowed |
| `RTG-PLAN-006` | error | no | Feature not yet supported by this engine |
| `RTG-PLAN-007` | error | no | Foreign key does not match the referenced key types |
| `RTG-PLAN-008` | error | no | Destination table has no matching unique key |
| `RTG-PLAN-009` | error | no | Destination table not found |
| `RTG-PLAN-010` | error | no | Deferred foreign key is NOT NULL |
| `RTG-PLAN-011` | warning | no | Source graph does not match the mapping everywhere |
| `RTG-EXEC-001` | error | no | Record could not be converted |
| `RTG-EXEC-002` | error | no | Relationship endpoint not found |
| `RTG-EXEC-003` | error | yes | Batch write failed |
| `RTG-EXEC-004` | error | yes | Run interrupted |
| `RTG-EXEC-005` | error | no | More than one related node for a foreign key |
| `RTG-EXEC-006` | error | no | Duplicate key in the source |
| `RTG-EXEC-007` | error | no | Value would lose precision in the destination |
| `RTG-RUN-001` | error | no | Run not found |
| `RTG-RUN-002` | error | no | Mapping changed since the run started |
| `RTG-RUN-003` | error | no | Run already completed |
| `RTG-RUN-004` | fatal | no | Run state unreadable |
| `RTG-NEO-001` | error | no | Neo4j query failed |
| `RTG-VERIFY-001` | error | no | Entity missing in destination |
| `RTG-VERIFY-002` | error | no | Entity count mismatch |
| `RTG-VERIFY-003` | error | no | Relationship count mismatch |
| `RTG-VERIFY-004` | warning | no | Dangling source reference |
| `RTG-PROTO-001` | error | no | Unsupported protocol version |
| `RTG-PROTO-002` | error | no | Malformed protocol message |
| `RTG-PROTO-003` | error | no | Unknown method |
| `RTG-PROTO-004` | error | no | Invalid request parameters |
| `RTG-PROTO-005` | error | no | Duplicate request ID |
| `RTG-PROTO-006` | error | no | Handshake required |
| `RTG-PROTO-007` | fatal | no | Engine version not supported by this SDK |
| `RTG-PROTO-008` | fatal | no | Engine unavailable |
| `RTG-CLI-001` | error | no | Invalid command-line usage |
| `RTG-INTERNAL-001` | fatal | no | Internal error |

The engine test suite checks that this table and `error-codes.json` list the
same codes.
