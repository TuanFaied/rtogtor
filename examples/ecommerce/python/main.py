"""Migrate the e-commerce example from PostgreSQL to Neo4j and back,
verifying every value both ways.

    export POSTGRES_URL='postgres://user:password@localhost:5432/ecommerce'
    export NEO4J_URL='neo4j://neo4j:password@localhost:7687'
    pip install rtogtor
    python main.py

Load ../schema.sql into the PostgreSQL database first. The run is safe to
repeat: writes are idempotent.
"""

from __future__ import annotations

import sys
from pathlib import Path

from rtogtor import Connection, Mapping, Migration, RtoGtoRError
from rtogtor.models import Progress

HERE = Path(__file__).resolve().parent
POSTGRES = Connection.postgresql(url_env="POSTGRES_URL")
NEO4J = Connection.neo4j(url_env="NEO4J_URL")


def on_progress(p: Progress) -> None:
    if p.done:
        print(f"  {p.step_id:<36} {p.written:>6} written")


def migrate(file: str, source: Connection, destination: Connection) -> None:
    mapping = Mapping.from_file(HERE.parent / file)
    with Migration(source, destination, mapping) as m:
        plan = m.plan()
        print(
            f"{mapping.document.name}: {plan.source.product} -> {plan.destination.product}, {len(plan.steps)} steps"
        )
        try:
            report = m.run(batch_size=500, on_progress=on_progress)
        except RtoGtoRError as e:
            if e.report is not None:
                raise SystemExit(
                    f"run {e.report.run_id} stopped ({e}); resume it with resume_run_id={e.report.run_id!r}"
                ) from e
            raise
        verification = m.verify(report.run_id, values=True)
        print(
            f"  run {report.run_id} {report.status}, {report.totals.written} records written; verification {verification.status}"
        )
        if report.status != "succeeded" or verification.status != "passed":
            raise SystemExit(
                f"{mapping.document.name} did not round-trip cleanly; see rtogtor report --run-id {report.run_id}"
            )


def main() -> int:
    # PostgreSQL → Neo4j, then Neo4j → PostgreSQL with the reverse mapping.
    migrate("mapping.yaml", POSTGRES, NEO4J)
    migrate("mapping-reverse.yaml", NEO4J, POSTGRES)
    return 0


if __name__ == "__main__":
    sys.exit(main())
