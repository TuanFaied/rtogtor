# Canonical value encoding

- **Spec version:** 0.1.0 (draft)
- **Status:** Normative unless a section is marked *informative*

Languages and databases disagree about numeric and temporal precision. This is
the most likely source of silent data loss, so every value that crosses a
boundary — in the IR, plans, reports and the engine ↔ SDK protocol — uses the
encodings defined here.

The keywords MUST, MUST NOT, SHOULD and MAY are used as in RFC 2119.

## 1. Logical types

Every value has a **logical type**, carried separately in type metadata (§4).
Values are not self-describing: a JSON string `"42"` is an `int64` only
because its metadata says so.

| Logical type | JSON encoding | Canonical form |
|---|---|---|
| `bool` | JSON `true` / `false` | — |
| `int64` | JSON **string** of decimal digits | §2.1 |
| `decimal` | JSON **string** | §2.2 |
| `float64` | JSON number; special values as strings | §2.3 |
| `string` | JSON string | §2.4 |
| `uuid` | JSON string | §2.5 |
| `date` | JSON string | §2.6 |
| `time` | JSON string | §2.7 |
| `timetz` | JSON string | §2.7 |
| `timestamp` | JSON string | §2.8 |
| `timestamptz` | JSON string | §2.8 |
| `duration` | JSON string | §2.9 |
| `bytes` | JSON string (Base64) | §2.10 |
| `json` | Embedded JSON value | §2.11 |
| `array` | JSON array of encoded elements | §2.12 |

Producers MUST emit canonical forms. Consumers MUST accept canonical forms and
MAY accept the additional input forms listed per type, which they MUST
normalize to canonical form before comparing, hashing or storing. Any other
input is rejected with `RTG-VAL-001`.

## 2. Type rules

### 2.1 `int64`

Signed 64-bit integer, range −9223372036854775808 to 9223372036854775807.

- **Canonical:** `0`, or an optional `-` followed by digits without leading
  zeros. `-0` is not canonical.
- **Accepted input:** optional leading `+` or `-`, digits, leading zeros
  (`"+007"` → `"7"`).
- **Why a string:** JavaScript `number` loses precision above 2^53.

### 2.2 `decimal`

Exact decimal number of arbitrary precision.

- **Canonical:** `-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?` — no leading zeros, no
  trailing fractional zeros, no trailing `.`, no exponent, and no `-0`.
  Examples: `"1.50"` → `"1.5"`, `"100.00"` → `"100"`, `"-0.0"` → `"0"`.
- **Accepted input:** optional `+`/`-`, at least one integer digit, optional
  `.` followed by at least one digit. Exponent notation (`1e3`), `.5` and
  `5.` are rejected.
- **Special values:** `"NaN"`, `"Infinity"`, `"-Infinity"` (PostgreSQL
  `numeric` supports them). They MUST NOT appear in identity keys.
- **Precision and scale** are declared in type metadata (§4), not in the value.
  Dropping trailing zeros is therefore not a loss.

### 2.3 `float64`

IEEE 754 binary64.

- **Canonical:** a JSON number serialized as in RFC 8785 §3.2.2.3
  (ECMAScript `Number.prototype.toString`, shortest round-trip form).
- **Special values** are JSON strings: `"NaN"`, `"Infinity"`, `"-Infinity"`,
  and `"-0"` (negative zero; RFC 8785 would otherwise print it as `0`).
- `float64` MUST NOT be an identity component.

### 2.4 `string`

A sequence of Unicode scalar values, encoded in JSON as a JSON string and in
identity serialization as UTF-8.

- Values MUST be valid Unicode (no lone surrogates; valid UTF-8 at byte
  boundaries).
- **No normalization.** Strings are compared byte-for-byte as UTF-8. Unicode
  normalization (NFC/NFD) is never applied, because PostgreSQL does not apply
  it either.

### 2.5 `uuid`

- **Canonical:** 36-character lowercase hyphenated form,
  `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`.
- **Accepted input:** the same form with uppercase hex digits.

### 2.6 `date`

Proleptic Gregorian calendar date, years 0001–9999.

- **Canonical:** `YYYY-MM-DD`.
- Dates before year 1 (PostgreSQL `BC` dates) and after 9999 are not
  representable in v0.1 and are rejected with `RTG-VAL-001`.

### 2.7 `time` and `timetz`

- **Canonical `time`:** `HH:MM:SS` optionally followed by `.` and 1–9
  fractional digits with no trailing zeros. `24:00:00` is rejected.
- **Canonical `timetz`:** a canonical `time` followed by `Z` for a zero offset,
  otherwise `±HH:MM`. Accepted input: `+00:00` and `-00:00` (normalized to
  `Z`).

### 2.8 `timestamp` and `timestamptz`

- **`timestamp`** is a calendar date and wall-clock time **without** a time
  zone. It MUST NOT be interpreted as UTC or as any other zone.
  - **Canonical:** `YYYY-MM-DDTHH:MM:SS` optionally followed by `.` and 1–9
    fractional digits with no trailing zeros.
- **`timestamptz`** is an absolute instant.
  - **Canonical:** the instant converted to UTC, in the `timestamp` form
    followed by `Z`. Example: `2026-10-21T14:30:00+02:00` →
    `2026-10-21T12:30:00Z`.
  - **Accepted input:** any RFC 3339 offset (`Z`, `±HH:MM`).
  - Named time zones (`Europe/Berlin`) are not representable in v0.1. A
    source value carrying a named zone is converted to UTC and the conversion
    is reported as a `representation` loss (§5).
- Fractional digits beyond 9 (finer than nanoseconds) are rejected.
- The **declared** precision (PostgreSQL defaults to microseconds) is carried
  in type metadata as `fractionalDigits`. The canonical value never pads
  trailing zeros.

### 2.9 `duration`

A calendar-aware duration of months, days and seconds, matching both
PostgreSQL `interval` and Neo4j `Duration`.

- **Canonical:** `P<months>M<days>DT<seconds>S` where all three components are
  always present. `<months>` and `<days>` are signed integers without leading
  zeros; `<seconds>` is a signed decimal with 0–9 fractional digits and no
  trailing fractional zeros. A component equal to zero is written `0` without
  a sign.
  Examples: `P14M3DT3600.5S`, `P0M-1DT0S`, `P0M0DT-0.25S`.
- Components are not normalized into each other (12 months are not converted
  to a year, and 24 hours are not converted to a day), because their lengths
  vary by calendar.
- Other ISO 8601 duration forms (`P1Y2M`, `PT1H`) are not accepted in v0.1.

### 2.10 `bytes`

- **Canonical:** standard Base64 alphabet with `=` padding (RFC 4648 §4), no
  line breaks or whitespace.
- Non-canonical Base64 (incorrect padding, non-zero padding bits) is rejected.

### 2.11 `json`

- The value is embedded directly as a JSON value (object, array, string,
  number, boolean or `null`), not as a string containing JSON. This avoids
  double encoding.
- The fact that the value is a JSON document comes from type metadata.
- Equality is defined on the RFC 8785 canonical form.
- PostgreSQL `jsonb` does not preserve key order or duplicate keys; PostgreSQL
  `json` does. Converting `json` → `jsonb` semantics is a `representation`
  loss.

### 2.12 `array`

- A JSON array whose elements are each encoded per the element type in
  metadata (`element`).
- Elements MAY be JSON `null`.
- Arrays are one-dimensional in v0.1; multi-dimensional arrays are rejected
  with `RTG-VAL-001`.

## 3. Null and absent

`null` and *absent* are distinct:

| State | Meaning | IR / protocol encoding |
|---|---|---|
| null | The value is known to be SQL `NULL` | JSON `null` |
| absent | The attribute is not present on this record | The key is omitted |

Neo4j cannot store a property whose value is `null`. When writing to a graph,
a `null` attribute is written by **omitting the property**. When reading a
graph with a mapping, an **absent mapped property** is read back as `null`.
Because a relational row always has a value for every column, this round trip
is lossless. Reports count the omitted properties.

## 4. Type metadata

Type metadata accompanies values in the IR and schema metadata:

```json
{
  "type": "decimal",
  "precision": 12,
  "scale": 2,
  "nativeType": "numeric(12,2)"
}
```

| Field | Applies to | Meaning |
|---|---|---|
| `type` | all | Logical type (§1) |
| `nativeType` | all | The database's own type name, informative only |
| `precision`, `scale` | `decimal` | Declared precision and scale, if any |
| `fractionalDigits` | `time`, `timetz`, `timestamp`, `timestamptz`, `duration` | Declared fractional-second digits (0–9) |
| `element` | `array` | Type metadata of the elements |
| `maxLength` | `string`, `bytes` | Declared maximum length, if any |

## 5. Lossy transformations

Any conversion that narrows precision or range, or changes representation, is
a **lossy transformation**. It follows the mapping's loss policy:

- **strict** (default): rejected unless the mapping explicitly allows it;
- **flexible**: permitted, and every occurrence is reported.

Loss kinds:

| Kind | Example |
|---|---|
| `precision` | `timestamp` with nanoseconds → PostgreSQL microseconds; `decimal` → `float64` |
| `range` | `int64` → 32-bit integer column; year 10000 → `date` |
| `representation` | `decimal` stored as a Neo4j `String`; `uuid` stored as a `String`; named zone → UTC |
| `structure` | Multiple labels → one table; nested JSON → flattened columns |

Widening conversions that preserve every value exactly (32-bit → 64-bit
integer, microseconds → nanoseconds) are not lossy.

## 6. Canonical JSON documents

Whenever documents are compared byte-for-byte (plans, reports, conformance
outputs, mapping hashes), they are serialized with the
**JSON Canonicalization Scheme, RFC 8785**: sorted object keys, no
insignificant whitespace, ECMAScript number formatting, and UTF-8 output.
Array order is defined per document type by its schema.

**Stricter than RFC 8785 on numbers.** RFC 8785 converts every JSON number
to an IEEE 754 double, which silently changes numbers that a double cannot
hold exactly (for example `12345678901234567890` would become
`12345678901234567000`). RtoGtoR rejects such numbers instead: a JSON
document containing one cannot be canonicalized and the record fails with
`RTG-EXEC-001`. Numbers that a double holds exactly (`0.1`, `4.50`, `1E30`)
are written in the RFC 8785 form. RtoGtoR's own documents never contain
such numbers, because `int64` and `decimal` values are encoded as strings.

## 7. Database type mappings (informative)

### PostgreSQL → logical type

| PostgreSQL | Logical type | Notes |
|---|---|---|
| `smallint`, `integer`, `bigint` | `int64` | Width kept in `nativeType` |
| `numeric(p,s)`, `numeric` | `decimal` | `precision`/`scale` when declared |
| `real`, `double precision` | `float64` | `real` → `float64` is exact |
| `text`, `varchar(n)`, `char(n)` | `string` | `char(n)` values keep their padding |
| `boolean` | `bool` | |
| `uuid` | `uuid` | |
| `date` | `date` | |
| `time(p)`, `timetz(p)` | `time`, `timetz` | |
| `timestamp(p)`, `timestamptz(p)` | `timestamp`, `timestamptz` | Default `fractionalDigits` 6 |
| `interval` | `duration` | |
| `bytea` | `bytes` | |
| `json`, `jsonb` | `json` | |
| `T[]` | `array` | One dimension only |
| enum types | `string` | Allowed values recorded in schema metadata |
| others (`money`, `inet`, geometric, ranges, …) | — | Unsupported in v0.1 |

### Logical type → Neo4j

| Logical type | Neo4j type | Lossy? |
|---|---|---|
| `bool` | Boolean | no |
| `int64` | Integer | no |
| `decimal` | String | `representation` |
| `float64` | Float | no |
| `string` | String | no |
| `uuid` | String | `representation` |
| `date` | Date | no |
| `time` / `timetz` | LocalTime / Time | no (not yet written by engine 0.1) |
| `timestamp` / `timestamptz` | LocalDateTime / DateTime in zone `UTC` | no |
| `duration` | Duration | no (not yet written by engine 0.1) |
| `bytes` | ByteArray | no |
| `json` | String (canonical JSON) | `representation` |
| `array` | List | `structure` if elements contain `null` (Neo4j lists cannot hold `null`) |
