# Identity derivation (`rtg-id/v1`)

- **Spec version:** 1.0.0
- **Identity algorithm:** `rtg-id/v1`
- **Status:** Normative

Every migrated entity receives a stable identity, so re-running or resuming a
migration updates the same node or row instead of creating a duplicate. The
identity MUST be byte-identical in every language, on every platform, in every
run.

## 1. Inputs

An identity is derived from:

1. **Entity type** — the `name` of the entity in the mapping (for example
   `OrderItem`). It is case-sensitive. It is the mapping entity name, not the
   table name or the label, so renaming a table or label does not change
   identities.
2. **Key components** — the entity's key fields, **in the order declared in
   the mapping** (`key: [order_id, line_no]`). Each component is a pair
   *(logical type, value)*.

Rules:

- The entity type MUST be a non-empty, valid UTF-8 string. Otherwise:
  `RTG-ID-003`.
- There MUST be at least one key component. Otherwise: `RTG-ID-003`.
- No component may be null. Otherwise: `RTG-ID-001`.
- Each component's logical type MUST be one of the permitted types in §2.
  Otherwise: `RTG-ID-002`.
- Each value is first normalized to its canonical form as defined in
  [`values.md`](values.md). Invalid values: `RTG-VAL-001`.

## 2. Permitted types and tags

Each permitted logical type has a fixed ASCII tag. Tags are frozen for
`rtg-id/v1` and MUST NOT change.

| Logical type | Tag | Value bytes (UTF-8 of the canonical form) |
|---|---|---|
| `bool` | `bool` | `true` or `false` |
| `int64` | `i64` | Canonical decimal digits, e.g. `-42` |
| `decimal` | `dec` | Canonical decimal, e.g. `12.5` (special values not allowed) |
| `string` | `str` | The string itself, unnormalized |
| `uuid` | `uuid` | Lowercase hyphenated form |
| `date` | `date` | `YYYY-MM-DD` |
| `timestamp` | `ts` | Canonical zone-less timestamp |
| `timestamptz` | `tstz` | Canonical UTC timestamp ending in `Z` |
| `bytes` | `bytes` | Canonical padded Base64 text |

Not permitted: `float64` (unreliable equality), `time`, `timetz`, `duration`,
`json`, `array`.

## 3. Serialization

All integers are unsigned 32-bit **big-endian** (`u32be`). All strings are
UTF-8. Lengths count **bytes**, not characters.

```text
serialization =
    "rtg-id/v1"                      ; 9 ASCII bytes (domain separator)
    0x00                             ; 1 byte
    u32be(len(entityType))  entityType
    u32be(componentCount)
    component*                       ; in mapping-declared key order

component =
    tag                              ; ASCII, from §2
    ":"                              ; 0x3A
    u32be(len(value))  value         ; UTF-8 of the canonical form
```

The length prefixes make the encoding unambiguous: the composite key
`(1001, 3)` and `(100, 13)` produce different serializations even though their
digits concatenate to the same text.

## 4. The `rtg_id` value

```text
rtg_id = lowercase_hex( SHA-256( serialization ) )
```

The result is 64 lowercase hexadecimal characters. In the graph, it is stored
in the identity property (default name `rtg_id`) and protected by a uniqueness
constraint created before any data is loaded.

A mapping MAY choose `identity.strategy: natural`, which stores the natural key
properties instead of `rtg_id`. The derivation above still defines entity
equality inside the engine.

Implementations MUST NOT use language built-ins such as `hash()`,
`GetHashCode()`, `String()` or `toString()` on composite objects to derive
identities.

## 5. Worked example

Entity type `Customer`, key `[(int64, "42")]`.

| Part | Bytes (hex) | Meaning |
|---|---|---|
| Domain | `7274672d69642f7631` | `rtg-id/v1` |
| Separator | `00` | |
| Entity type length | `00000008` | 8 bytes |
| Entity type | `437573746f6d6572` | `Customer` |
| Component count | `00000001` | 1 |
| Tag | `693634` | `i64` |
| Colon | `3a` | `:` |
| Value length | `00000002` | 2 bytes |
| Value | `3432` | `42` |

```text
serialization = 7274672d69642f76310000000008437573746f6d6572000000016936343a000000023432
rtg_id        = 3260a403a6d59fd3b308276126826219271f2ab48ba6a540b253a718ed6528ff
```

The input `"+0042"` normalizes to `"42"` and yields the same `rtg_id`. The
`string` value `"42"` yields a different one.

Composite example — `OrderItem`, key `[(int64, "1001"), (int64, "3")]`:

```text
rtg_id = cdb11d1fed94472fc66fff69484a6cbdbc8cea0a7c9849edeebfc6303fedca7e
```

## 6. Test vectors

The complete set of valid and invalid vectors is in
[`conformance/vectors/identity.json`](../conformance/vectors/identity.json).
The engine and every SDK MUST:

- for each entry in `vectors`: normalize `key` to `canonicalKey`, and
  reproduce `serializationHex` and `rtgId` exactly;
- for each entry in `invalid`: fail with exactly `errorCode`.

## 7. Versioning

`rtg-id/v1` is immutable. Any change to tags, ordering, canonical forms of
permitted types, or the hash function requires a new algorithm version
(`rtg-id/v2`) and a documented re-keying path for already-migrated data.
