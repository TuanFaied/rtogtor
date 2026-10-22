"""Builds one platform wheel per supported platform, each bundling its engine.

    python build_wheels.py            # every platform -> ../../dist/python
    python build_wheels.py win-x64    # selected platforms
    python build_wheels.py --dev      # engine for this machine into src/ (for tests)

Each wheel holds rtogtor/_bin/rtogtor[.exe] for exactly one platform and is
tagged py3-none-<platform>, so pip installs the right one.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
ENGINE = HERE.parent.parent / "engine"
BIN = HERE / "src" / "rtogtor" / "_bin"
OUT = HERE.parent.parent / "dist" / "python"

# rid -> (GOOS, GOARCH, wheel platform tag). Go 1.25 binaries need macOS 12+.
PLATFORMS = {
    "linux-x64": ("linux", "amd64", "manylinux_2_17_x86_64.manylinux2014_x86_64.musllinux_1_1_x86_64"),
    "linux-arm64": ("linux", "arm64", "manylinux_2_17_aarch64.manylinux2014_aarch64.musllinux_1_1_aarch64"),
    "osx-x64": ("darwin", "amd64", "macosx_12_0_x86_64"),
    "osx-arm64": ("darwin", "arm64", "macosx_12_0_arm64"),
    "win-x64": ("windows", "amd64", "win_amd64"),
}


def run(cmd: list[str], **kwargs: object) -> None:
    print("+", " ".join(cmd), flush=True)
    subprocess.run(cmd, check=True, **kwargs)  # type: ignore[call-overload]  # noqa: S603


def build(rid: str) -> Path:
    goos, goarch, tag = PLATFORMS[rid]
    shutil.rmtree(BIN, ignore_errors=True)
    BIN.mkdir(parents=True)
    exe = "rtogtor.exe" if goos == "windows" else "rtogtor"
    env = {**os.environ, "CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch}
    run(["go", "build", "-trimpath", "-ldflags", "-s -w", "-o", str(BIN / exe), "./cmd/rtogtor"], cwd=ENGINE, env=env)
    if goos != "windows":
        (BIN / exe).chmod(0o755)

    with tempfile.TemporaryDirectory() as tmp:
        run([sys.executable, "-m", "build", "--wheel", "--outdir", tmp], cwd=HERE)
        (wheel,) = Path(tmp).glob("*.whl")
        retag = ["--remove", "--python-tag", "py3", "--abi-tag", "none", "--platform-tag", tag]
        run([sys.executable, "-m", "wheel", "tags", *retag, str(wheel)])
        (tagged,) = Path(tmp).glob("*.whl")
        OUT.mkdir(parents=True, exist_ok=True)
        dest = OUT / tagged.name
        shutil.move(str(tagged), dest)
    shutil.rmtree(HERE / "build", ignore_errors=True)
    return dest


def host_rid() -> str:
    import platform

    machine = platform.machine().lower()
    arch = "arm64" if machine in ("arm64", "aarch64") else "x64"
    osname = {"win32": "win", "darwin": "osx"}.get(sys.platform, "linux")
    return f"{osname}-{arch}"


def dev() -> int:
    """Builds the engine for this machine into src/rtogtor/_bin for local tests."""
    goos = PLATFORMS[host_rid()][0]
    BIN.mkdir(parents=True, exist_ok=True)
    exe = "rtogtor.exe" if goos == "windows" else "rtogtor"
    run(["go", "build", "-o", str(BIN / exe), "./cmd/rtogtor"], cwd=ENGINE, env={**os.environ, "CGO_ENABLED": "0"})
    return 0


def main() -> int:
    if sys.argv[1:] == ["--dev"]:
        return dev()
    rids = sys.argv[1:] or list(PLATFORMS)
    unknown = [r for r in rids if r not in PLATFORMS]
    if unknown:
        print(f"unknown platforms: {unknown}; choose from {list(PLATFORMS)}", file=sys.stderr)
        return 2
    try:
        for rid in rids:
            print("built", build(rid))
    finally:
        shutil.rmtree(BIN, ignore_errors=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
