"""RtoGtoR: relational ↔ graph migration with explicit mappings.

    from rtogtor import Connection, Mapping, Migration

    source = Connection.postgresql(url_env="SOURCE_URL")
    destination = Connection.neo4j(uri="bolt://localhost:7687", auth_env="NEO4J")
    mapping = Mapping.from_file("mapping.yaml")

    with Migration(source=source, destination=destination, mapping=mapping) as m:
        plan = m.plan()
        report = m.run(dry_run=False)

All migration logic runs in the bundled RtoGtoR engine; this package is a thin,
typed client. Typed documents live in ``rtogtor.models``.
"""

from . import _generated, models
from ._generated.error_codes import ERROR_CODES, ErrorCode, ErrorCodeInfo
from .connection import Connection
from .engine import Engine
from .errors import Diagnostic, RtoGtoRError
from .mapping import Mapping
from .migration import AsyncMigration, Migration

__all__ = [
    "ERROR_CODES",
    "AsyncMigration",
    "Connection",
    "Diagnostic",
    "Engine",
    "ErrorCode",
    "ErrorCodeInfo",
    "Mapping",
    "Migration",
    "RtoGtoRError",
    "_generated",
    "models",
]
