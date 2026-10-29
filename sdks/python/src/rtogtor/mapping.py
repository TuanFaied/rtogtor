"""Mappings: loaded from JSON or YAML and validated locally against the schema."""

from __future__ import annotations

import json
import re
from functools import cache
from importlib import resources
from pathlib import Path
from typing import Any

import yaml
from jsonschema import Draft202012Validator, ValidationError
from referencing import Registry, Resource

from ._generated.error_codes import ErrorCode
from ._generated.models import MappingDocument
from .errors import Diagnostic, RtoGtoRError

_BASE = "https://github.com/TuanFaied/rtogtor/specs/"
_SUPPORTED_SPEC = "0.1."


@cache
def _registry() -> Registry[Any]:
    files = resources.files("rtogtor._generated")
    pairs = []
    for entry in files.iterdir():
        if entry.name.endswith(".schema.json"):
            schema = json.loads(entry.read_text(encoding="utf-8"))
            pairs.append((_BASE + entry.name, Resource.from_contents(schema)))
    return Registry().with_resources(pairs)


@cache
def _validator(schema_file: str) -> Draft202012Validator:
    schema = _registry().contents(_BASE + schema_file)
    return Draft202012Validator(schema, registry=_registry())


def _leaves(error: ValidationError) -> list[ValidationError]:
    if not error.context:
        return [error]
    return [leaf for sub in error.context for leaf in _leaves(sub)]


def _pointer(error: ValidationError) -> str:
    return "".join("/" + str(p).replace("~", "~0").replace("/", "~1") for p in error.absolute_path)


def _validate(schema_file: str, instance: Any, code: str) -> list[Diagnostic]:
    problems = sorted(
        {(_pointer(leaf), leaf.message) for e in _validator(schema_file).iter_errors(instance) for leaf in _leaves(e)}
    )
    return [Diagnostic(code, "error", False, message, path) for path, message in problems]


class Mapping:
    """A mapping (specs/mapping.schema.json).

    Loading validates against the schema; semantic checks (unique names, key
    coverage, foreign-key arity) are the engine's: see
    ``Engine.validate_mapping`` or simply plan the migration.
    """

    def __init__(self, document: dict[str, Any]) -> None:
        """Use ``from_file``, ``from_json``, ``from_yaml`` or ``from_document``."""
        spec_version = document.get("specVersion") if isinstance(document, dict) else None
        if isinstance(spec_version, str) and not spec_version.startswith(_SUPPORTED_SPEC):
            raise RtoGtoRError(
                ErrorCode.SPEC_001,
                f"mapping declares specVersion {spec_version}; this SDK supports 0.1.x",
                path="/specVersion",
            )
        problems = _validate("mapping.schema.json", document, ErrorCode.MAP_001)
        if problems:
            more = f" (and {len(problems) - 1} more problem{'s' if len(problems) > 2 else ''})" if len(problems) > 1 else ""
            raise RtoGtoRError(ErrorCode.MAP_001, problems[0].message + more, path=problems[0].path, diagnostics=problems)
        self._json = document
        self.document = MappingDocument.model_validate(document)

    @classmethod
    def from_file(cls, path: str | Path) -> Mapping:
        """Loads a ``.json``, ``.yaml`` or ``.yml`` file."""
        p = Path(path)
        try:
            text = p.read_text(encoding="utf-8")
        except OSError as e:
            raise RtoGtoRError(ErrorCode.MAP_002, f"cannot read the mapping file: {e}") from e
        suffix = p.suffix.lower()
        if suffix == ".json":
            return cls.from_json(text)
        if suffix in (".yaml", ".yml"):
            return cls.from_yaml(text)
        raise RtoGtoRError(ErrorCode.MAP_002, f'cannot infer the format of "{p.name}": use a .json, .yaml or .yml extension')

    @classmethod
    def from_json(cls, text: str) -> Mapping:
        """Parses a JSON mapping. Duplicate object keys are rejected."""

        def no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
            out: dict[str, Any] = {}
            for k, v in pairs:
                if k in out:
                    raise ValueError(f'duplicate key "{k}"')
                out[k] = v
            return out

        try:
            document = json.loads(text, object_pairs_hook=no_duplicates)
        except ValueError as e:
            raise RtoGtoRError(ErrorCode.MAP_002, f"invalid JSON: {e}") from e
        return cls(document)

    @classmethod
    def from_yaml(cls, text: str) -> Mapping:
        """Parses a YAML mapping.

        Plain scalars are typed with the YAML 1.2 core schema, as the engine
        does: ``specVersion: 0.1`` is a number (and invalid); quote it.
        """
        try:
            nodes = list(yaml.compose_all(text, Loader=_PlainLoader))
        except yaml.YAMLError as e:
            raise RtoGtoRError(ErrorCode.MAP_002, f"invalid YAML: {e}") from e
        if len(nodes) != 1:
            raise RtoGtoRError(
                ErrorCode.MAP_002,
                "mapping document is empty" if not nodes else "a mapping file must contain exactly one YAML document",
            )
        return cls(_to_json(nodes[0]))

    @classmethod
    def from_document(cls, document: MappingDocument) -> Mapping:
        """Creates a mapping from a typed document, for example one built in code."""
        return cls(document.model_dump(mode="json", by_alias=True, exclude_unset=True))

    def to_json(self) -> str:
        """The mapping as JSON."""
        return json.dumps(self._json, ensure_ascii=False)

    def _wire(self) -> dict[str, Any]:
        return self._json


# --- YAML 1.2 core schema --------------------------------------------------------

_PLAIN = "rtogtor:plain"


class _PlainLoader(yaml.SafeLoader):
    """Marks untagged plain scalars so they can be typed with the YAML 1.2 core
    schema (PyYAML's own resolvers follow YAML 1.1, where ``yes`` is true)."""

    def resolve(self, kind: Any, value: Any, implicit: Any) -> Any:
        if kind is yaml.ScalarNode and implicit[0]:
            return _PLAIN
        return super().resolve(kind, value, implicit)  # type: ignore[no-untyped-call]  # PyYAML stubs leave resolve untyped


_INT10 = re.compile(r"[-+]?[0-9]+")
_INT8 = re.compile(r"0o[0-7]+")
_INT16 = re.compile(r"0x[0-9a-fA-F]+")
_FLOAT = re.compile(r"[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?")


def _scalar(node: yaml.ScalarNode) -> Any:
    value: str = node.value
    tag = node.tag
    if tag == "tag:yaml.org,2002:str" or (tag != _PLAIN and not tag.startswith("tag:yaml.org,2002:")):
        return value
    if value in ("", "~", "null", "Null", "NULL"):
        return None
    if value in ("true", "True", "TRUE"):
        return True
    if value in ("false", "False", "FALSE"):
        return False
    if value in (".inf", ".Inf", ".INF", "+.inf", "-.inf", ".nan", ".NaN", ".NAN"):
        raise RtoGtoRError(ErrorCode.MAP_002, f"YAML value {value} cannot be represented as JSON")
    if _INT10.fullmatch(value):
        return int(value)
    if _INT8.fullmatch(value):
        return int(value[2:], 8)
    if _INT16.fullmatch(value):
        return int(value[2:], 16)
    if _FLOAT.fullmatch(value):
        return float(value)
    return value


def _to_json(node: yaml.Node) -> Any:
    if isinstance(node, yaml.MappingNode):
        out: dict[str, Any] = {}
        for key_node, value_node in node.value:
            if not isinstance(key_node, yaml.ScalarNode):
                raise RtoGtoRError(ErrorCode.MAP_002, "YAML mapping keys must be strings")
            key = key_node.value
            if key in out:
                raise RtoGtoRError(ErrorCode.MAP_002, f'invalid YAML: mapping key "{key}" already defined')
            out[key] = _to_json(value_node)
        return out
    if isinstance(node, yaml.SequenceNode):
        return [_to_json(n) for n in node.value]
    if isinstance(node, yaml.ScalarNode):
        return _scalar(node)
    raise RtoGtoRError(ErrorCode.MAP_002, "unsupported YAML node")
