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
- PostgreSQL adapter: schema inspection from pg_catalog in a read-only
  snapshot (types, precision, enums, domains, arrays, keys, foreign keys)
- CLI command `inspect` (connection URL from an environment variable only;
  credentials redacted from every error)

### Specification 0.1.0 (draft), continued

- Error codes RTG-SCHEMA-001…003, RTG-CONN-001…002, RTG-PG-001

### Repository

- `docker-compose.yml` with PostgreSQL 17 and Neo4j 5 (localhost-only ports)
- CI workflow for the engine; Dependabot for Go modules and GitHub Actions
- Development guide
