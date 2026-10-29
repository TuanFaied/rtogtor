"""One engine process speaking the JSON Lines protocol (specs/protocol.md)."""

from __future__ import annotations

import collections
import contextlib
import itertools
import json
import os
import stat
import subprocess
import sys
import threading
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from importlib import resources
from pathlib import Path
from typing import Any

from ._generated.error_codes import ErrorCode
from .errors import RtoGtoRError

PROTOCOL_VERSION = "1.0"
SUPPORTED_ENGINE_RANGE = ">= 0.1.0-0, < 0.2.0"
ENGINE_PATH_VARIABLE = "RTOGTOR_ENGINE_PATH"


def sdk_version() -> str:
    try:
        from importlib.metadata import version

        return version("rtogtor")
    except Exception:  # noqa: BLE001 - running from a source tree
        return "0.0.0"


def is_supported_engine(version: str) -> bool:
    """Engine versions this SDK supports; before 1.0 a minor version may break compatibility."""
    core = version.split("-")[0].split("+")[0].split(".")
    if len(core) != 3 or not all(p.isdigit() for p in core):
        return False
    return int(core[0]) == 0 and int(core[1]) == 1


def locate_engine(explicit: str | None = None) -> str:
    """Returns the engine binary: explicit path, RTOGTOR_ENGINE_PATH, or the bundled one."""
    exe = "rtogtor.exe" if sys.platform == "win32" else "rtogtor"
    candidates: list[str] = []
    if explicit:
        candidates.append(explicit)
    elif os.environ.get(ENGINE_PATH_VARIABLE):
        candidates.append(os.environ[ENGINE_PATH_VARIABLE])
    else:
        candidates.append(str(resources.files("rtogtor") / "_bin" / exe))
    for c in candidates:
        path = Path(c)
        if path.is_file():
            if sys.platform != "win32" and not os.access(path, os.X_OK):
                try:
                    path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
                except OSError as e:
                    raise RtoGtoRError(ErrorCode.PROTO_008, f"the engine at {path} is not executable: {e}") from e
            return str(path)
    raise RtoGtoRError(
        ErrorCode.PROTO_008,
        f"the RtoGtoR engine was not found (looked in: {', '.join(candidates)}). "
        f"Set {ENGINE_PATH_VARIABLE} to an rtogtor binary, or install the wheel for your platform.",
    )


@dataclass
class _Pending:
    on_event: Callable[[str, Any], None] | None
    done: threading.Event = field(default_factory=threading.Event)
    result: Any = None
    error: dict[str, Any] | None = None


class Session:
    """An engine process. Requests may come from several threads."""

    def __init__(self, engine_path: str | None, env: Mapping[str, str], shutdown_timeout: float) -> None:
        path = locate_engine(engine_path)
        full_env = {**os.environ, **env}
        try:
            self._proc = subprocess.Popen(  # noqa: S603 - fixed binary and arguments
                [path, "serve", "--stdio"],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env=full_env,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
            )
        except OSError as e:
            raise RtoGtoRError(ErrorCode.PROTO_008, f"cannot start the engine at {path}: {e}") from e
        assert self._proc.stdin and self._proc.stdout and self._proc.stderr
        self._stdin = self._proc.stdin
        self._shutdown_timeout = shutdown_timeout
        self._write_lock = threading.Lock()
        self._pending: dict[str, _Pending] = {}
        self._pending_lock = threading.Lock()
        self._ids = itertools.count(1)
        self._stderr_tail: collections.deque[str] = collections.deque(maxlen=20)
        self._closed = False
        self._terminal: RtoGtoRError | None = None
        self._reader = threading.Thread(target=self._read_stdout, name="rtogtor-stdout", daemon=True)
        self._stderr_reader = threading.Thread(target=self._read_stderr, name="rtogtor-stderr", daemon=True)
        self._reader.start()
        self._stderr_reader.start()

        try:
            self.info: dict[str, Any] = self.call(
                "handshake",
                {"client": {"name": "rtogtor-python", "version": sdk_version()}, "protocolVersions": [PROTOCOL_VERSION]},
            )
            if not is_supported_engine(self.info["engineVersion"]) or not self.info["protocolVersion"].startswith("1."):
                raise RtoGtoRError(
                    ErrorCode.PROTO_007,
                    f"engine {self.info['engineVersion']} (protocol {self.info['protocolVersion']}) "
                    f"is not supported by this SDK, which needs {SUPPORTED_ENGINE_RANGE}",
                )
        except BaseException:
            self.close()
            raise

    @property
    def pid(self) -> int:
        return self._proc.pid

    @property
    def exited(self) -> bool:
        return self._proc.poll() is not None

    def _write(self, message: dict[str, Any]) -> None:
        line = (json.dumps(message, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
        with self._write_lock:
            self._stdin.write(line)
            self._stdin.flush()

    def _send_cancel(self, request_id: str) -> None:
        with contextlib.suppress(OSError):
            self._write(
                {
                    "protocolVersion": PROTOCOL_VERSION,
                    "id": "cancel-" + request_id,
                    "method": "cancel",
                    "params": {"requestId": request_id},
                }
            )

    def call(
        self,
        method: str,
        params: dict[str, Any],
        on_event: Callable[[str, Any], None] | None = None,
        cancel: threading.Event | None = None,
    ) -> Any:
        """Sends a request and waits for its result.

        If ``cancel`` is set, or the user presses Ctrl-C (KeyboardInterrupt),
        the engine is asked to cancel the request; the call then raises after
        the engine has stopped it.
        """
        if self._closed:
            raise RtoGtoRError(ErrorCode.PROTO_008, "the engine session is closed")
        if self._terminal is not None:
            raise self._terminal
        request_id = str(next(self._ids))
        pending = _Pending(on_event)
        with self._pending_lock:
            self._pending[request_id] = pending
        try:
            self._write({"protocolVersion": PROTOCOL_VERSION, "id": request_id, "method": method, "params": params})
        except OSError as e:
            with self._pending_lock:
                self._pending.pop(request_id, None)
            raise self._gone() from e

        interrupted = False
        cancel_sent = False
        while not pending.done.is_set():
            try:
                if pending.done.wait(0.05):
                    break
                if cancel is not None and cancel.is_set() and not cancel_sent:
                    self._send_cancel(request_id)
                    cancel_sent = True
            except KeyboardInterrupt:
                interrupted = True
                if not cancel_sent:
                    self._send_cancel(request_id)
                    cancel_sent = True
        if pending.error is not None:
            error = RtoGtoRError._from_wire(pending.error)
            if interrupted:
                raise KeyboardInterrupt from error
            raise error
        if interrupted:
            raise KeyboardInterrupt
        return pending.result

    def _read_stdout(self) -> None:
        assert self._proc.stdout
        for raw in self._proc.stdout:
            line = raw.decode("utf-8").strip()
            if not line:
                continue
            message = json.loads(line)
            request_id = message.get("id")
            if "event" in message:
                with self._pending_lock:
                    p = self._pending.get(request_id)
                if p is not None and p.on_event is not None:
                    p.on_event(message["event"], message.get("data"))
                continue
            with self._pending_lock:
                p = self._pending.pop(request_id, None)
            if p is None:
                continue  # for example the answer to a cancel request
            if "error" in message:
                p.error = message["error"]
            else:
                p.result = message.get("result")
            p.done.set()
        self._fail_all()

    def _read_stderr(self) -> None:
        assert self._proc.stderr
        for raw in self._proc.stderr:
            self._stderr_tail.append(raw.decode("utf-8", "replace").rstrip())

    def _gone(self) -> RtoGtoRError:
        code = self._proc.poll()
        exit_text = f" with exit code {code}" if code is not None else ""
        tail = " | ".join(self._stderr_tail)
        detail = f"; last engine output: {tail}" if tail else ""
        return RtoGtoRError(ErrorCode.PROTO_008, f"the engine process ended{exit_text}{detail}")

    def _fail_all(self) -> None:
        error = self._gone() if not self._closed else RtoGtoRError(ErrorCode.PROTO_008, "the engine session was closed")
        self._terminal = error
        with self._pending_lock:
            pending = list(self._pending.values())
            self._pending.clear()
        for p in pending:
            p.error = {"code": error.code, "severity": error.severity, "retryable": False, "message": error.message}
            p.done.set()

    def close(self) -> None:
        """Asks the engine to shut down, then kills it if it does not exit in time."""
        if self._closed:
            return
        self._closed = True
        if self._proc.poll() is None:
            try:
                self._write({"protocolVersion": PROTOCOL_VERSION, "id": "shutdown", "method": "shutdown", "params": {}})
                self._stdin.close()
            except OSError:
                pass
            try:
                self._proc.wait(self._shutdown_timeout)
            except subprocess.TimeoutExpired:
                self._proc.kill()
                self._proc.wait()
        self._reader.join(5)
        self._stderr_reader.join(5)
        for stream in (self._proc.stdout, self._proc.stderr):
            if stream:
                stream.close()
