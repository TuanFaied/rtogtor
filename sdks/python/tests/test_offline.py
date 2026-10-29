"""Tests that need no engine and no database."""

from __future__ import annotations

from pathlib import Path

import pytest

from rtogtor import ERROR_CODES, Connection, ErrorCode, Mapping, RtoGtoRError
from rtogtor._session import is_supported_engine, locate_engine

from conftest import read_json, repo_path

MINIMAL = """\
specVersion: "0.1.0"
name: test
direction: relational-to-graph
source: { kind: postgresql }
destination: { kind: neo4j }
entities:
  - name: Customer
    table: customers
    labels: [Customer]
    key: [id]
    properties:
      - { column: id, property: id, type: int64 }
"""


def test_example_mapping_loads() -> None:
    m = Mapping.from_file(repo_path("examples", "ecommerce", "mapping.yaml"))
    assert m.document.name == "ecommerce"
    item = next(e for e in m.document.entities if e.name == "OrderItem")
    assert item.key == ["order_id", "line_no"]
    assert m.document.relationships is not None
    assert m.document.relationships[0].foreign_key is not None
    assert m.document.relationships[0].foreign_key.direction == "referenced-to-owner"
    assert m.document.source.schema_ == "public"


def test_yaml_and_json_agree() -> None:
    from_yaml = Mapping.from_yaml(MINIMAL)
    from_json = Mapping.from_json(from_yaml.to_json())
    assert from_yaml.document == from_json.document


@pytest.mark.parametrize(
    ("old", "new", "code", "path"),
    [
        ('specVersion: "0.1.0"', "specVersion: 0.1", ErrorCode.MAP_001, "/specVersion"),
        ('specVersion: "0.1.0"', 'specVersion: "0.2.0"', ErrorCode.SPEC_001, "/specVersion"),
        ('specVersion: "0.1.0"', 'specVersion: "1.1.0"', ErrorCode.SPEC_001, "/specVersion"),
        ("type: int64 }", "type: integer }", ErrorCode.MAP_001, "/entities/0/properties/0/type"),
        ("name: test", "name: test\nname: again", ErrorCode.MAP_002, None),
    ],
)
def test_invalid_yaml_is_rejected_with_codes(old: str, new: str, code: str, path: str | None) -> None:
    with pytest.raises(RtoGtoRError) as exc:
        Mapping.from_yaml(MINIMAL.replace(old, new))
    assert exc.value.code == code
    assert exc.value.severity == "error"
    if path is not None:
        assert path in [d.path for d in exc.value.diagnostics] + [exc.value.path]


@pytest.mark.parametrize("version", ["0.1.0", "0.1.9", "1.0.0", "1.0.3"])
def test_supported_spec_versions_are_accepted(version: str) -> None:
    doc = Mapping.from_yaml(MINIMAL.replace('specVersion: "0.1.0"', f'specVersion: "{version}"')).document
    assert doc.spec_version == version


def test_yaml_12_core_schema_not_11() -> None:
    # YAML 1.1 (PyYAML's default) would read `yes` as true; the engine reads a string.
    doc = MINIMAL.replace("name: test", "name: yes")
    assert Mapping.from_yaml(doc).document.name == "yes"


def test_json_duplicate_keys_are_rejected() -> None:
    with pytest.raises(RtoGtoRError) as exc:
        Mapping.from_json('{"name": "a", "name": "b"}')
    assert exc.value.code == ErrorCode.MAP_002
    assert "duplicate key" in exc.value.message


def test_every_schema_problem_is_reported() -> None:
    with pytest.raises(RtoGtoRError) as exc:
        Mapping.from_json('{"specVersion": "0.1.0", "name": "x"}')
    assert exc.value.code == ErrorCode.MAP_001
    assert exc.value.diagnostics
    assert all(d.code == ErrorCode.MAP_001 for d in exc.value.diagnostics)


def test_mapping_can_be_built_in_code() -> None:
    doc = Mapping.from_yaml(MINIMAL).document.model_copy(update={"name": "renamed"})
    assert Mapping.from_document(doc).document.name == "renamed"


def test_error_codes_match_the_specification() -> None:
    spec = read_json("specs", "error-codes.json")["codes"]
    assert len(spec) == len(ERROR_CODES)
    for c in spec:
        info = ERROR_CODES[c["code"]]
        assert info.severity == c["severity"]
        assert info.retryable == c["retryable"]
    assert RtoGtoRError(ErrorCode.CONN_001, "x").retryable


def test_connections_never_show_credentials() -> None:
    pg = Connection.postgresql(url="postgres://app:S3cret@db/x")
    neo = Connection.neo4j("neo4j://db:7687", user="neo4j", password="Pa55word")
    assert repr(pg) == "<postgresql connection>"
    assert "Pa55word" not in repr(neo)
    env: dict[str, str] = {}
    wire = str(pg._to_wire(env)) + str(neo._to_wire(env))
    assert "S3cret" not in wire
    assert "Pa55word" not in wire
    assert "postgres://app:S3cret@db/x" in env.values()
    with pytest.raises(RuntimeError):
        pg._to_wire(None)


def test_connection_arguments_are_checked() -> None:
    with pytest.raises(ValueError, match="exactly one"):
        Connection.postgresql()
    with pytest.raises(ValueError, match="postgres://"):
        Connection.postgresql(url="mysql://x")
    with pytest.raises(ValueError, match="credentials"):
        Connection.neo4j("neo4j://u:p@host:7687")
    wire = Connection.neo4j("bolt://localhost:7687", auth_env="NEO4J")._to_wire(None)
    assert wire["userEnv"] == "NEO4J_USER"
    assert wire["passwordEnv"] == "NEO4J_PASSWORD"


@pytest.mark.parametrize(
    ("version", "supported"),
    [("1.0.0-dev", True), ("1.0.0", True), ("1.7.2", True), ("0.1.0", False), ("2.0.0", False), ("garbage", False)],
)
def test_engine_version_range(version: str, supported: bool) -> None:
    assert is_supported_engine(version) is supported


def test_missing_engine_is_reported_clearly(tmp_path: Path) -> None:
    with pytest.raises(RtoGtoRError) as exc:
        locate_engine(str(tmp_path / "no-such-rtogtor"))
    assert exc.value.code == ErrorCode.PROTO_008
