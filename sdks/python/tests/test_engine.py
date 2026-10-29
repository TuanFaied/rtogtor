"""Tests that run the real engine binary without databases."""

from __future__ import annotations

import asyncio
import os
import threading
import time
from concurrent.futures import ThreadPoolExecutor

import pytest

from rtogtor import AsyncMigration, Connection, Engine, ErrorCode, Mapping, RtoGtoRError


def test_handshake_reports_versions(engine: Engine) -> None:
    assert engine.info.spec_version == "1.0.0"
    assert engine.info.identity_algorithm == "rtg-id/v1"
    assert engine.info.protocol_version == "1.0"
    assert engine.info.engine_version.startswith("1.")


def test_engine_errors_carry_codes(engine: Engine) -> None:
    with pytest.raises(RtoGtoRError) as exc:
        engine.inspect(Connection.postgresql(url_env="RTOGTOR_TEST_SURELY_UNSET"))
    assert exc.value.code == ErrorCode.CONN_002
    assert "RTOGTOR_TEST_SURELY_UNSET" in exc.value.message
    with pytest.raises(RtoGtoRError) as exc:
        engine.canonicalize("int64", "1.5")
    assert exc.value.code == ErrorCode.VAL_001


def test_semantic_validation_happens_in_the_engine(engine: Engine) -> None:
    mapping = Mapping.from_yaml(
        """\
specVersion: "0.1.0"
name: bad
direction: relational-to-graph
source: { kind: postgresql }
destination: { kind: neo4j }
entities:
  - name: OrderItem
    table: order_items
    labels: [OrderItem]
    key: [order_id, line_no]
    properties: [{ column: order_id, property: orderId, type: int64 }]
"""
    )
    result = engine.validate_mapping(mapping)
    assert not result.valid
    assert any(d.code == ErrorCode.MAP_005 and d.path == "/entities/0/key/1" for d in result.diagnostics)


BLACKHOLE = "postgres://u:p@10.255.255.1:5432/db?connect_timeout=60"


def test_cancellation_stops_a_request_and_the_engine_stays_usable() -> None:
    with Engine(env={"RTOGTOR_TEST_BLACKHOLE": BLACKHOLE}) as engine:
        cancel = threading.Event()
        threading.Timer(0.3, cancel.set).start()
        start = time.monotonic()
        with pytest.raises(RtoGtoRError) as exc:
            engine.inspect(Connection.postgresql(url_env="RTOGTOR_TEST_BLACKHOLE"), _cancel=cancel)
        assert exc.value.code == ErrorCode.EXEC_004
        assert time.monotonic() - start < 10
        assert engine.canonicalize("decimal", "0012.500") == "12.5"


def test_asyncio_cancellation_cancels_the_engine_request() -> None:
    mapping = Mapping.from_file(
        os.path.join(os.path.dirname(__file__), "..", "..", "..", "examples", "ecommerce", "mapping.yaml")
    )
    os.environ["RTOGTOR_TEST_BLACKHOLE"] = BLACKHOLE

    async def main() -> float:
        async with AsyncMigration(Connection.postgresql(url_env="RTOGTOR_TEST_BLACKHOLE"), None, mapping) as m:
            start = time.monotonic()
            with pytest.raises((asyncio.TimeoutError, asyncio.CancelledError)):
                await asyncio.wait_for(m.plan(), timeout=0.5)
            return time.monotonic() - start

    assert asyncio.run(main()) < 10


def test_close_ends_the_engine_process() -> None:
    engine = Engine()
    session = engine._session
    engine.close()
    assert session.exited
    with pytest.raises(RtoGtoRError) as exc:
        engine.canonicalize("int64", "1")
    assert exc.value.code == ErrorCode.PROTO_008


def test_concurrent_requests_are_matched(engine: Engine) -> None:
    with ThreadPoolExecutor(8) as pool:
        results = list(pool.map(lambda i: engine.canonicalize("int64", f"+00{i}"), range(50)))
    assert results == [str(i) for i in range(50)]


def test_unknown_run_reports_a_code(engine: Engine, tmp_path: object) -> None:
    with pytest.raises(RtoGtoRError) as exc:
        engine.report("01234567-89ab-7def-8123-456789abcdef", str(tmp_path))
    assert exc.value.code == ErrorCode.RUN_001
