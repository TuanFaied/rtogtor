# Changelog

Each component (spec, engine, each SDK) is versioned independently. Entries are
grouped by component.

## Unreleased

### Specification 0.1.0 (draft)

- Canonical value encoding (`specs/values.md`)
- Identity derivation `rtg-id/v1` with test vectors (`specs/identity.md`)
- Stable error codes (`specs/error-codes.json`, `specs/error-codes.md`)
- JSON Schemas: mapping, schema metadata, IR (draft)

### Engine 0.1.0-dev

- Canonical value parsing and formatting
- `rtg_id` derivation
- Mapping loading (JSON/YAML) with schema and semantic validation
- CLI commands `version` and `validate`
- Relational and graph adapter interfaces (unstable)
