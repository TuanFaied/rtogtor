#!/usr/bin/env bash
# Builds the rtogtor engine into src/RtoGtoR/runtimes/<rid>/native for the
# NuGet package and local tests.
#
#   ./build-engine.sh                 # the current platform only
#   ./build-engine.sh all             # every packaged platform
#   ./build-engine.sh linux-x64 win-x64
#
# Portable to bash 3.2 (macOS): no associative arrays.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
engine="$here/../../engine"
out="$here/src/RtoGtoR/runtimes"

# go_target prints "GOOS GOARCH" for a .NET runtime identifier.
go_target() {
  case "$1" in
    linux-x64) echo "linux amd64" ;;
    linux-arm64) echo "linux arm64" ;;
    osx-x64) echo "darwin amd64" ;;
    osx-arm64) echo "darwin arm64" ;;
    win-x64) echo "windows amd64" ;;
    *) return 1 ;;
  esac
}

host_rid() {
  local os arch
  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=osx ;;
    MINGW* | MSYS* | CYGWIN*) os=win ;;
    *) echo "unsupported OS $(uname -s)" >&2; return 1 ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=x64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) echo "unsupported architecture $(uname -m)" >&2; return 1 ;;
  esac
  echo "$os-$arch"
}

if [ $# -eq 0 ]; then set -- "$(host_rid)"; fi
if [ "$1" = "all" ]; then set -- linux-x64 linux-arm64 osx-x64 osx-arm64 win-x64; fi

for rid in "$@"; do
  if ! target="$(go_target "$rid")"; then
    echo "unknown runtime identifier: $rid" >&2
    exit 1
  fi
  goos="${target% *}"
  goarch="${target#* }"
  exe=rtogtor
  if [ "$goos" = windows ]; then exe=rtogtor.exe; fi
  mkdir -p "$out/$rid/native"
  echo "building $rid"
  (cd "$engine" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w" -o "$out/$rid/native/$exe" ./cmd/rtogtor)
done
