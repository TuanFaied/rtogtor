from __future__ import annotations

import json
import os
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from rtogtor import Engine

REPO = Path(__file__).resolve().parents[3]


def repo_path(*parts: str) -> Path:
    return REPO.joinpath(*parts)


def read_json(*parts: str) -> Any:
    return json.loads(repo_path(*parts).read_text(encoding="utf-8"))


@pytest.fixture(scope="session")
def engine() -> Iterator[Engine]:
    with Engine() as e:
        yield e


def integration_configured() -> bool:
    return bool(os.environ.get("RTOGTOR_TEST_POSTGRES_URL") and os.environ.get("RTOGTOR_TEST_NEO4J_URL"))


def jcs(value: Any) -> str:
    """RFC 8785 canonical JSON for RtoGtoR documents (integers only)."""
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, str):
        return json.dumps(value, ensure_ascii=False)
    if isinstance(value, list):
        return "[" + ",".join(jcs(v) for v in value) + "]"
    if isinstance(value, dict):
        # UTF-16 code unit order, as RFC 8785 requires.
        keys = sorted(value, key=lambda k: k.encode("utf-16-be"))
        return "{" + ",".join(json.dumps(k, ensure_ascii=False) + ":" + jcs(value[k]) for k in keys) + "}"
    raise TypeError(f"unsupported value {value!r}")
