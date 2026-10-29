"""A running RtoGtoR engine (child process). All migration logic runs in the engine."""

from __future__ import annotations

import threading
from collections.abc import Iterable, Mapping
from types import TracebackType
from typing import Any

from ._generated.models import (
    Failure as RunFailure,
)
from ._generated.models import (
    HandshakeResult,
    IdentityResult,
    KeyComponent,
    Plan,
    Report,
    RunState,
    SchemaMetadata,
    ValidateMappingResult,
)
from ._session import SUPPORTED_ENGINE_RANGE, Session
from .connection import Connection
from .mapping import Mapping as RtgMapping


class Engine:
    """A running engine. Use it as a context manager, or call ``close()``.

    Connections with literal credentials can only be used through
    ``Migration``; with ``Engine``, use connections that name environment
    variables (``url_env``, ``auth_env``).
    """

    supported_engine_versions = SUPPORTED_ENGINE_RANGE

    def __init__(
        self,
        *,
        engine_path: str | None = None,
        env: Mapping[str, str] | None = None,
        shutdown_timeout: float = 5.0,
    ) -> None:
        """Starts the engine and checks its version (RTG-PROTO-007) and presence (RTG-PROTO-008)."""
        self._session = Session(engine_path, dict(env or {}), shutdown_timeout)
        self.info = HandshakeResult.model_validate(self._session.info)

    def __enter__(self) -> Engine:
        return self

    def __exit__(self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None) -> None:
        self.close()

    def close(self) -> None:
        """Stops the engine process."""
        self._session.close()

    def _call(self, method: str, params: dict[str, Any], cancel: threading.Event | None = None, on_event: Any = None) -> Any:
        return self._session.call(method, {k: v for k, v in params.items() if v is not None}, on_event, cancel)

    def validate_mapping(self, mapping: RtgMapping) -> ValidateMappingResult:
        """Runs every validation, including semantic checks the schema cannot express."""
        return ValidateMappingResult.model_validate(self._call("validateMapping", {"mapping": mapping._wire()}))

    def inspect(
        self, source: Connection, schemas: Iterable[str] | None = None, *, _cancel: threading.Event | None = None
    ) -> SchemaMetadata:
        """Inspects a PostgreSQL or Neo4j database's schema (read-only). ``schemas`` apply to PostgreSQL."""
        params = {"source": source._to_wire(None), "schemas": list(schemas) if schemas is not None else None}
        return SchemaMetadata.model_validate(self._call("inspect", params, _cancel))

    def plan(
        self,
        mapping: RtgMapping,
        source_metadata: SchemaMetadata,
        destination_metadata: SchemaMetadata | None = None,
    ) -> Plan:
        """Plans a mapping against inspected metadata, without connecting to any database.

        Graph-to-relational mappings also need ``destination_metadata`` (the existing tables).
        """
        params: dict[str, Any] = {
            "mapping": mapping._wire(),
            "sourceMetadata": source_metadata.model_dump(mode="json", by_alias=True, exclude_unset=True),
        }
        if destination_metadata is not None:
            params["destinationMetadata"] = destination_metadata.model_dump(mode="json", by_alias=True, exclude_unset=True)
        return Plan.model_validate(self._call("plan", params))

    def report(self, run_id: str, state_dir: str | None = None) -> Report:
        """Reads a run's report."""
        return Report.model_validate(self._call("report", {"runId": run_id, "stateDir": state_dir}))

    def run_state(self, run_id: str, state_dir: str | None = None) -> RunState:
        """Reads a run's state."""
        return RunState.model_validate(self._call("runState", {"runId": run_id, "stateDir": state_dir}))

    def failures(self, run_id: str, state_dir: str | None = None) -> list[RunFailure]:
        """Lists a run's failed records."""
        result = self._call("failures", {"runId": run_id, "stateDir": state_dir})
        return [RunFailure.model_validate(f) for f in result["failures"]]

    def identity(self, entity_type: str, key: Iterable[KeyComponent]) -> IdentityResult:
        """Derives an ``rtg_id`` (specs/identity.md) with the engine's rules."""
        wire = [{"type": k.type, "value": k.value} for k in key]  # explicit None stays null
        return IdentityResult.model_validate(self._session.call("identity", {"entityType": entity_type, "key": wire}))

    def canonicalize(self, type_: str, value: str) -> str:
        """Returns the canonical text form of a value (specs/values.md)."""
        result: str = self._call("canonicalize", {"type": type_, "value": value})["canonical"]
        return result
