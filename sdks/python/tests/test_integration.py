"""End-to-end tests against disposable databases.

PostgreSQL must hold the e-commerce example in schema public; Neo4j nodes are
written under labels with a random suffix and deleted afterwards (cleanup
failures fail the test).
"""

from __future__ import annotations

import asyncio
import base64
import json
import os
import threading
import urllib.request
import uuid
from collections.abc import Iterator
from pathlib import Path
from urllib.parse import unquote, urlsplit

import pytest

from rtogtor import AsyncMigration, Connection, ErrorCode, Mapping, Migration, RtoGtoRError
from rtogtor.models import Progress, RetryPolicy

from conftest import integration_configured, repo_path

pytestmark = pytest.mark.skipif(
    not integration_configured(),
    reason="set RTOGTOR_TEST_POSTGRES_URL and RTOGTOR_TEST_NEO4J_URL (disposable databases)",
)

SOURCE = Connection.postgresql(url_env="RTOGTOR_TEST_POSTGRES_URL")
DESTINATION = Connection.neo4j(url_env="RTOGTOR_TEST_NEO4J_URL")


def cypher(statements: list[str]) -> None:
    """Neo4j HTTP API (cleanup only). It answers 200 even when a statement fails."""
    bolt = urlsplit(os.environ["RTOGTOR_TEST_NEO4J_URL"])
    http = os.environ.get("RTOGTOR_TEST_NEO4J_HTTP", f"http://{bolt.hostname}:7474")
    auth = base64.b64encode(f"{unquote(bolt.username or '')}:{unquote(bolt.password or '')}".encode()).decode()
    body = json.dumps({"statements": [{"statement": s} for s in statements]}).encode()
    req = urllib.request.Request(
        http + "/db/neo4j/tx/commit", data=body, headers={"Content-Type": "application/json", "Authorization": "Basic " + auth}
    )
    with urllib.request.urlopen(req) as resp:
        errors = json.loads(resp.read())["errors"]
    if errors:
        raise RuntimeError(f"Neo4j cleanup failed: {errors}")


@pytest.fixture
def mapping() -> Iterator[Mapping]:
    suffix = uuid.uuid4().hex[:8]
    m = Mapping.from_file(repo_path("examples", "ecommerce", "mapping.yaml"))
    labels: list[str] = []
    entities = []
    for e in m.document.entities:
        new = [f"{label}_{suffix}" for label in e.labels]
        labels.extend(new)
        entities.append(e.model_copy(update={"labels": new}))
    yield Mapping.from_document(m.document.model_copy(update={"entities": entities}))
    # Data and schema changes cannot share a transaction.
    cypher([f"MATCH (n:`{label}`) DETACH DELETE n" for label in labels])
    cypher([f"DROP CONSTRAINT `rtg_{label}_rtg_id` IF EXISTS" for label in labels])


def test_plan_dry_run_migrate_verify(mapping: Mapping, tmp_path: Path) -> None:
    state = str(tmp_path)
    with Migration(SOURCE, DESTINATION, mapping) as m:
        assert len(m.plan().steps) == 13

        dry = m.run(dry_run=True, state_dir=state)
        assert dry.dry_run and dry.status == "succeeded"

        progress: list[Progress] = []
        report = m.run(state_dir=state, batch_size=2, on_progress=progress.append)
        assert report.status == "succeeded"
        assert report.totals.written == 24
        assert any(p.step_id == "nodes:Customer" and p.done for p in progress)

        verification = m.verify(report.run_id, state)
        assert verification.status == "passed"
        assert m.report(report.run_id, state).verification is not None
        assert m.failures(report.run_id, state) == []


def test_cancelled_run_can_be_resumed(mapping: Mapping, tmp_path: Path) -> None:
    state = str(tmp_path)
    cancel = threading.Event()
    with Migration(SOURCE, DESTINATION, mapping) as m, pytest.raises(RtoGtoRError) as exc:
        m.run(state_dir=state, batch_size=1, on_progress=lambda _: cancel.set(), _cancel=cancel)
    assert exc.value.code == ErrorCode.EXEC_004
    assert exc.value.report is not None and exc.value.report.status == "interrupted"
    run_id = exc.value.report.run_id

    # A new session (a new engine process) resumes from the checkpoint.
    with Migration(SOURCE, DESTINATION, mapping) as m:
        report = m.run(state_dir=state, resume_run_id=run_id)
        assert report.status == "succeeded" and report.attempts == 2
        assert m.verify(run_id, state).status == "passed"


def test_async_migration(mapping: Mapping, tmp_path: Path) -> None:
    async def main() -> None:
        async with AsyncMigration(SOURCE, DESTINATION, mapping) as m:
            report = await m.run(state_dir=str(tmp_path))
            assert report.status == "succeeded"
            assert (await m.verify(report.run_id, str(tmp_path))).status == "passed"

    asyncio.run(main())


def test_resume_after_injected_fault(mapping: Mapping, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """The engine stops after a batch commits but before its checkpoint, the
    worst crash point; resuming replays that batch without duplicates."""
    monkeypatch.setenv("RTOGTOR_TEST_FAULT", "step=relationships:OrderContainsItem;batch=1;point=before-checkpoint")
    state = str(tmp_path)
    with Migration(SOURCE, DESTINATION, mapping) as m:
        with pytest.raises(RtoGtoRError) as exc:
            m.run(state_dir=state, batch_size=1)
        assert exc.value.code == ErrorCode.EXEC_004
        assert exc.value.report is not None and exc.value.report.status == "interrupted"
        run_id = exc.value.report.run_id

        # The same engine process: the fault fired once and does not fire again.
        report = m.run(state_dir=state, resume_run_id=run_id)
        assert report.status == "succeeded" and report.attempts == 2
        assert report.totals.written == 24
        verification = m.verify(run_id, state, values=True)
        assert verification.status == "passed"
        assert all(e.value_mismatches == 0 for e in verification.entities)


def test_retry_policy_absorbs_transient_faults(mapping: Mapping, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("RTOGTOR_TEST_FAULT", "step=nodes:Customer;point=write;times=2")
    retry = RetryPolicy(max_attempts=3, initial_backoff_ms=1, max_backoff_ms=5)
    with Migration(SOURCE, DESTINATION, mapping) as m:
        report = m.run(state_dir=str(tmp_path), retry=retry)
    assert report.status == "succeeded"
    assert report.retries == 2
    assert report.retry == retry


def test_exhausted_retries_interrupt_the_run(mapping: Mapping, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("RTOGTOR_TEST_FAULT", "step=nodes:Customer;point=write;times=5")
    with Migration(SOURCE, DESTINATION, mapping) as m, pytest.raises(RtoGtoRError) as exc:
        m.run(state_dir=str(tmp_path), retry=RetryPolicy(max_attempts=2, initial_backoff_ms=1, max_backoff_ms=1))
    assert exc.value.code == ErrorCode.EXEC_003
    assert exc.value.report is not None and exc.value.report.status == "interrupted"
    assert exc.value.report.retries == 1
