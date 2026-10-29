"""The shared conformance suite (conformance/), through the public API.

When RTOGTOR_CONFORMANCE_OUT is set, results are written per
conformance/runner-protocol.md.
"""

from __future__ import annotations

import hashlib
import json
import os
import struct
from collections.abc import Iterator
from typing import Any

import pytest
from hypothesis import given, settings
from hypothesis import strategies as st

from rtogtor import Engine, Mapping, RtoGtoRError
from rtogtor._session import sdk_version
from rtogtor.models import KeyComponent, SchemaMetadata

from conftest import jcs, read_json, repo_path

RESULTS: list[dict[str, Any]] = []


@pytest.fixture(scope="module", autouse=True)
def write_results(engine: Engine) -> Iterator[None]:
    yield
    out = os.environ.get("RTOGTOR_CONFORMANCE_OUT")
    if out:
        doc = {
            "runnerProtocol": "1",
            "sdk": "python",
            "sdkVersion": sdk_version(),
            "engineVersion": engine.info.engine_version,
            "specVersion": engine.info.spec_version,
            "results": sorted(RESULTS, key=lambda r: r["case"]),
        }
        with open(out, "w", encoding="utf-8") as f:
            json.dump(doc, f, indent=2)


def record(case: str, passed: bool, detail: str | None = None) -> None:
    RESULTS.append({"case": case, "status": "passed" if passed else "failed", "detail": detail})


def key(items: list[dict[str, Any]]) -> list[KeyComponent]:
    return [KeyComponent(type=k["type"], value=k["value"]) for k in items]


def test_identity_vectors(engine: Engine) -> None:
    doc = read_json("conformance", "vectors", "identity.json")
    failures = []
    for v in doc["vectors"]:
        r = engine.identity(v["entityType"], key(v["key"]))
        ok = (
            r.rtg_id == v["rtgId"]
            and r.serialization_hex == v["serializationHex"]
            and [k.value for k in r.canonical_key] == [k["value"] for k in v["canonicalKey"]]
        )
        record("vectors/identity/" + v["name"], ok)
        if not ok:
            failures.append(v["name"])
    for v in doc["invalid"]:
        with pytest.raises(RtoGtoRError) as exc:
            engine.identity(v["entityType"], key(v["key"]))
        ok = exc.value.code == v["errorCode"]
        record("vectors/identity/invalid/" + v["name"], ok, exc.value.code)
        if not ok:
            failures.append(v["name"])
    assert not failures


def test_value_vectors(engine: Engine) -> None:
    doc = read_json("conformance", "vectors", "values.json")
    failures = []
    for i, v in enumerate(doc["valid"]):
        got = engine.canonicalize(v["type"], v["input"])
        ok = got == v["canonical"]
        record(f"vectors/values/valid/{i:03d}-{v['type']}", ok, got)
        if not ok:
            failures.append((v["type"], v["input"], got))
    for i, v in enumerate(doc["invalid"]):
        with pytest.raises(RtoGtoRError) as exc:
            engine.canonicalize(v["type"], v["input"])
        ok = exc.value.code == v["errorCode"]
        record(f"vectors/values/invalid/{i:03d}-{v['type']}", ok, exc.value.code)
        if not ok:
            failures.append((v["type"], v["input"], exc.value.code))
    assert not failures


def test_ecommerce_plan_case(engine: Engine) -> None:
    """The mapping loaded from YAML by this SDK, planned against the case's metadata,
    must serialize (through the typed model) to exactly expected-plan.json."""
    mapping = Mapping.from_file(repo_path("examples", "ecommerce", "mapping.yaml"))
    metadata = SchemaMetadata.model_validate(read_json("conformance", "cases", "ecommerce", "expected-schema-metadata.json"))
    plan = engine.plan(mapping, metadata)
    got = jcs(plan.model_dump(mode="json", by_alias=True, exclude_unset=True))
    want = repo_path("conformance", "cases", "ecommerce", "expected-plan.json").read_text(encoding="utf-8").rstrip("\n")
    record("cases/ecommerce/plan", got == want)
    assert got == want
    assert len(plan.steps) == 13


# --- Property: the engine's identities match an independent implementation -------


def reference_rtg_id(entity: str, parts: list[tuple[str, str]]) -> str:
    """rtg-id/v1 (specs/identity.md §3), written independently of the engine."""
    tags = {"int64": b"i64", "string": b"str"}

    def lp(b: bytes) -> bytes:
        return struct.pack(">I", len(b)) + b

    out = b"rtg-id/v1\x00" + lp(entity.encode()) + struct.pack(">I", len(parts))
    for t, v in parts:
        out += tags[t] + b":" + lp(v.encode())
    return hashlib.sha256(out).hexdigest()


component = st.one_of(
    st.integers(min_value=-(2**63), max_value=2**63 - 1).map(lambda n: ("int64", str(n))),
    st.text(st.characters(exclude_categories=("Cs",)), max_size=20).map(lambda s: ("string", s)),
)


@settings(max_examples=150, deadline=None)
@given(
    entity=st.text(st.characters(exclude_categories=("Cs",)), min_size=1, max_size=12),
    parts=st.lists(component, min_size=1, max_size=4),
)
def test_identity_property(engine: Engine, entity: str, parts: list[tuple[str, str]]) -> None:
    r = engine.identity(entity, [KeyComponent(type=t, value=v) for t, v in parts])
    assert r.rtg_id == reference_rtg_id(entity, parts)
