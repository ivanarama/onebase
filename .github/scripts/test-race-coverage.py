#!/usr/bin/env python3
"""Compatibility entry point for the full UI4/launcher2 race/atomic runner.

Keep the positional coverage filename used by CI. Both command lines execute
one engine, retaining its inventory, JSON journals and validated profiles.
"""

from collections import Counter
import importlib.util
from pathlib import Path
import sys


_spec = importlib.util.spec_from_file_location(
    "onebase_test_coverage", Path(__file__).with_name("test-coverage.py"))
_engine = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_engine)


def discover_names(output):
    """Retain the legacy discovery helper with strict name validation."""
    names = []
    for line in output.splitlines():
        if line.startswith(("Test", "Example", "Fuzz")):
            _engine.require(_engine.NAME.fullmatch(line) and line.isidentifier(),
                            f"invalid discovered name: {line!r}")
            names.append(line)
    _engine.require(names and len(set(names)) == len(names), "empty or duplicate test inventory")
    return names


def verify_inventory(expected, actual):
    _engine.require(Counter(expected) == Counter(actual), "execution differs from discovery")


def merge_profiles(profiles, destination):
    # Legacy helper has no package-series metadata. The engine still validates
    # every block and sums only compatible coordinates/statements.
    _engine.merge([(None, profile) for profile in profiles], destination)


def main(argv=None):
    arguments = list(sys.argv[1:] if argv is None else argv)
    _engine.require(len(arguments) <= 1, "expected at most one coverage filename")
    _engine.main(["--output", arguments[0] if arguments else "coverage.out"])


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(f"test-race-coverage: {error}", file=sys.stderr)
        sys.exit(1)
