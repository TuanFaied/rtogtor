"""Installs the wheel for this platform into a fresh virtual environment, like a user
would, and exercises the bundled engine (no RTOGTOR_ENGINE_PATH).

    python smoke.py     # after build_wheels.py
"""

from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import venv
from pathlib import Path

from build_wheels import OUT, PLATFORMS, host_rid

REPO = Path(__file__).resolve().parents[2]

SCRIPT = """
import json, pathlib, sys
import rtogtor
from rtogtor import Engine, Mapping
from rtogtor.models import SchemaMetadata
assert "site-packages" in rtogtor.__file__, rtogtor.__file__
repo = pathlib.Path(sys.argv[1])
with Engine() as e:
    print("installed package engine", e.info.engine_version, "protocol", e.info.protocol_version)
    m = Mapping.from_file(repo / "examples/ecommerce/mapping.yaml")
    case = repo / "conformance/cases/ecommerce"
    meta = SchemaMetadata.model_validate(json.loads((case / "expected-schema-metadata.json").read_text()))
    p = e.plan(m, meta)
    print("planned", len(p.steps), "steps, mapping hash", p.mapping.hash[:12])
    sys.exit(0 if len(p.steps) == 13 else 1)
"""


def main() -> int:
    tag = PLATFORMS[host_rid()][2].split(".")[0]
    wheels = [w for w in OUT.glob("*.whl") if tag in w.name]
    if len(wheels) != 1:
        print(f"expected one wheel for {tag} in {OUT}, found {wheels}", file=sys.stderr)
        return 1
    env = {k: v for k, v in os.environ.items() if k != "RTOGTOR_ENGINE_PATH"}
    with tempfile.TemporaryDirectory() as tmp:
        venv.create(tmp, with_pip=True)
        bindir = Path(tmp) / ("Scripts" if sys.platform == "win32" else "bin")
        python = bindir / ("python.exe" if sys.platform == "win32" else "python")
        subprocess.run([python, "-m", "pip", "install", "-q", str(wheels[0])], check=True, env=env)  # noqa: S603
        subprocess.run([python, "-c", SCRIPT, str(REPO)], check=True, env=env, cwd=tmp)  # noqa: S603
        subprocess.run([python, "-m", "rtogtor", "version"], check=True, env=env, cwd=tmp)  # noqa: S603
    print("smoke test passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
