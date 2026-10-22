"""RtoGtoR errors: every failure carries a stable code from specs/error-codes.json."""

from __future__ import annotations

from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

from ._generated.error_codes import ERROR_CODES, Severity

if TYPE_CHECKING:
    from ._generated.models import Report


@dataclass(frozen=True)
class Diagnostic:
    """One problem reported by the engine or the SDK."""

    code: str
    severity: Severity
    retryable: bool
    message: str
    path: str | None = None


class RtoGtoRError(Exception):
    """An RtoGtoR failure with a stable error code.

    The SDK never invents codes: they come from the engine or from the
    specification.

    Attributes:
        code: The stable code, for example ``"RTG-MAP-005"``.
        severity: ``"info"``, ``"warning"``, ``"error"`` or ``"fatal"``.
        retryable: Whether retrying the same operation unchanged may succeed.
        path: Location of the problem (for example a JSON Pointer), if known.
        diagnostics: Every problem, when the error stands for several.
        report: For a failed or interrupted run, its report; pass
            ``report.run_id`` to ``Migration.run(resume_run_id=...)``.
    """

    def __init__(
        self,
        code: str,
        message: str,
        *,
        path: str | None = None,
        diagnostics: list[Diagnostic] | None = None,
        retryable: bool | None = None,
        report: Report | None = None,
    ) -> None:
        super().__init__(f"{code}: {message}")
        self.code = code
        self.message = message
        info = ERROR_CODES.get(code)
        self.severity: Severity = info.severity if info else "fatal"
        self.retryable = retryable if retryable is not None else bool(info and info.retryable)
        self.path = path
        self.diagnostics: list[Diagnostic] = diagnostics or []
        self.report = report

    @classmethod
    def _from_wire(cls, error: dict[str, Any]) -> RtoGtoRError:
        from ._generated.models import Report

        diagnostics = [
            Diagnostic(d["code"], d["severity"], d["retryable"], d["message"], d.get("path"))
            for d in error.get("diagnostics") or []
        ]
        report = Report.model_validate(error["report"]) if error.get("report") else None
        return cls(
            error["code"],
            error["message"],
            path=error.get("path"),
            diagnostics=diagnostics,
            retryable=error.get("retryable"),
            report=report,
        )
