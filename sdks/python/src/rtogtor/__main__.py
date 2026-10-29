"""``python -m rtogtor ...`` runs the bundled engine's CLI."""

from __future__ import annotations

import subprocess
import sys

from ._session import locate_engine


def main() -> int:
    return subprocess.call([locate_engine(), *sys.argv[1:]])  # noqa: S603 - the bundled engine


if __name__ == "__main__":
    sys.exit(main())
