"""Migrations: the main entry point, with a sync API and an asyncio variant."""

from __future__ import annotations

import asyncio
import threading
from collections.abc import Callable
from types import TracebackType
from typing import Any

from ._generated.models import Failure, Plan, Progress, Report, Verification
from .connection import Connection
from .engine import Engine
from .mapping import Mapping


class Migration:
    """Migrates a source to a destination with a mapping.

    It starts its own engine process on first use; leaving the ``with`` block
    (or ``close()``) stops it. Press Ctrl-C during a call to cancel it: the
    engine stops the request, and a run can then be resumed.

    Example::

        with Migration(source=source, destination=destination, mapping=mapping) as m:
            plan = m.plan()
            report = m.run(dry_run=False)
    """

    def __init__(
        self,
        source: Connection,
        destination: Connection | None,
        mapping: Mapping,
        *,
        engine_path: str | None = None,
    ) -> None:
        self._source = source
        self._destination = destination
        self._mapping = mapping
        self._engine_path = engine_path
        self._engine: Engine | None = None
        self._lock = threading.Lock()
        self._wire_source: dict[str, Any] = {}
        self._wire_destination: dict[str, Any] | None = None

    def __enter__(self) -> Migration:
        return self

    def __exit__(self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None) -> None:
        self.close()

    def close(self) -> None:
        """Stops the engine process."""
        with self._lock:
            if self._engine is not None:
                self._engine.close()
                self._engine = None

    def _engine_started(self) -> Engine:
        with self._lock:
            if self._engine is None:
                # Literal credentials reach the engine as private environment variables.
                env: dict[str, str] = {}
                self._wire_source = self._source._to_wire(env)
                self._wire_destination = self._destination._to_wire(env) if self._destination else None
                self._engine = Engine(engine_path=self._engine_path, env=env)
            return self._engine

    def plan(self, *, _cancel: threading.Event | None = None) -> Plan:
        """Inspects the source and plans the migration. Nothing is written."""
        engine = self._engine_started()
        return Plan.model_validate(engine._call("plan", {"mapping": self._mapping._wire(), "source": self._wire_source}, _cancel))

    def run(
        self,
        *,
        dry_run: bool = False,
        batch_size: int | None = None,
        resume_run_id: str | None = None,
        state_dir: str | None = None,
        on_progress: Callable[[Progress], None] | None = None,
        _cancel: threading.Event | None = None,
    ) -> Report:
        """Runs the migration (or a dry run) and returns its report.

        If the run fails or is interrupted, the ``RtoGtoRError`` carries the
        report; resume with ``resume_run_id=error.report.run_id``.
        ``on_progress`` is called (from a background thread) after every
        committed batch.
        """
        if self._destination is None and not dry_run:
            raise ValueError("a destination connection is required unless dry_run is set")
        engine = self._engine_started()

        def on_event(name: str, data: Any) -> None:
            if name == "progress" and on_progress is not None:
                on_progress(Progress.model_validate(data))

        result = engine._call(
            "migrate",
            {
                "mapping": self._mapping._wire(),
                "source": self._wire_source,
                "destination": self._wire_destination,
                "dryRun": dry_run,
                "batchSize": batch_size,
                "resumeRunId": resume_run_id,
                "stateDir": state_dir,
            },
            _cancel,
            on_event,
        )
        return Report.model_validate(result)

    def verify(self, run_id: str, state_dir: str | None = None) -> Verification:
        """Compares a run's source and destination; the result is added to the run's report."""
        if self._destination is None:
            raise ValueError("verification needs a destination connection")
        engine = self._engine_started()
        result = engine._call(
            "verify",
            {"runId": run_id, "source": self._wire_source, "destination": self._wire_destination, "stateDir": state_dir},
        )
        return Verification.model_validate(result)

    def report(self, run_id: str, state_dir: str | None = None) -> Report:
        """Reads a run's report."""
        return self._engine_started().report(run_id, state_dir)

    def failures(self, run_id: str, state_dir: str | None = None) -> list[Failure]:
        """Lists a run's failed records."""
        return self._engine_started().failures(run_id, state_dir)


class AsyncMigration:
    """The asyncio variant of ``Migration``. Cancelling a task cancels the engine request.

    Example::

        async with AsyncMigration(source=source, destination=destination, mapping=mapping) as m:
            report = await m.run()
    """

    def __init__(
        self, source: Connection, destination: Connection | None, mapping: Mapping, *, engine_path: str | None = None
    ) -> None:
        self._sync = Migration(source, destination, mapping, engine_path=engine_path)

    async def __aenter__(self) -> AsyncMigration:
        return self

    async def __aexit__(self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None) -> None:
        await self.aclose()

    async def aclose(self) -> None:
        """Stops the engine process."""
        await asyncio.to_thread(self._sync.close)

    @staticmethod
    async def _cancellable(fn: Callable[[threading.Event], Any]) -> Any:
        cancel = threading.Event()
        task = asyncio.ensure_future(asyncio.to_thread(fn, cancel))
        try:
            return await asyncio.shield(task)
        except asyncio.CancelledError:
            cancel.set()
            # Wait for the engine to stop the request; its error (with any run
            # report) becomes the cancellation's cause.
            try:
                await task
            except BaseException as e:
                raise asyncio.CancelledError() from e
            raise

    async def plan(self) -> Plan:
        """See ``Migration.plan``."""
        result: Plan = await self._cancellable(lambda c: self._sync.plan(_cancel=c))
        return result

    async def run(
        self,
        *,
        dry_run: bool = False,
        batch_size: int | None = None,
        resume_run_id: str | None = None,
        state_dir: str | None = None,
        on_progress: Callable[[Progress], None] | None = None,
    ) -> Report:
        """See ``Migration.run``."""
        result: Report = await self._cancellable(
            lambda c: self._sync.run(
                dry_run=dry_run,
                batch_size=batch_size,
                resume_run_id=resume_run_id,
                state_dir=state_dir,
                on_progress=on_progress,
                _cancel=c,
            )
        )
        return result

    async def verify(self, run_id: str, state_dir: str | None = None) -> Verification:
        """See ``Migration.verify``."""
        return await asyncio.to_thread(self._sync.verify, run_id, state_dir)
