#!/usr/bin/env python3
"""Run the complete PostgreSQL/race UI inventory in sequential groups of at most 128.

Retain discovery and JSON test journals. Publish completion only after every
root and subtest has exactly one run and terminal outcome; no suites are
excluded or delegated to another job.
"""

import argparse
from collections import Counter
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys


_spec = importlib.util.spec_from_file_location(
    "onebase_test_coverage", Path(__file__).with_name("test-coverage.py"))
_engine = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_engine)
PACKAGE = _engine.UI
MAX_GROUP = 128
FLAGS = ["-race", "-count=1", "-timeout=20m", "-json"]


def command(args, log):
    print("+ " + " ".join(args), flush=True)
    errors = log.with_name(log.name + ".stderr")
    with log.open("w", encoding="utf-8") as out, errors.open("w", encoding="utf-8") as err:
        child = subprocess.Popen(args, stdout=out, stderr=err, start_new_session=True)

        def cancel(signum, _frame):
            # GitHub cancels the Python step, which must also stop its Go test
            # process tree before a subsequent step can use the same database.
            # Do not wait inside a signal handler: Popen.wait may already hold
            # its waitpid lock. Unwind it before terminating/reaping the tree.
            raise SystemExit(128 + signum)

        previous = {s: signal.signal(s, cancel) for s in (signal.SIGINT, signal.SIGTERM)}
        try:
            result = child.wait()
        finally:
            for signum, handler in previous.items():
                signal.signal(signum, handler)
            # Also stop the tree on unexpected runner errors.
            if child.poll() is None:
                try:
                    os.killpg(child.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    child.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    pass
                # The Go driver may exit before a descendant that ignores TERM.
                # Kill any remaining members of our private process group too.
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                child.wait()
            for stream in (log, errors):
                with stream.open(encoding="utf-8") as src:
                    for line in src:
                        print(line, end="", flush=True)
    _engine.require(result == 0, f"command failed ({result}): {log}")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifacts", type=Path, default=Path("postgres-ui-results"))
    args = parser.parse_args(argv)
    _engine.require(bool(os.environ.get("TEST_DATABASE_URL", "").strip()),
                    "TEST_DATABASE_URL is required for PostgreSQL UI tests")
    # A previous success cannot mask a partial retry; preserve old evidence by
    # requiring a new artifacts directory instead of overwriting it.
    args.artifacts.mkdir(parents=True, exist_ok=False)
    listing = args.artifacts / "list.jsonl"
    command(["go", "test", *FLAGS, "-list", "^(Test|Example|Fuzz)", PACKAGE], listing)
    names = _engine.inventory(listing, PACKAGE)
    groups = [names[i:i + MAX_GROUP] for i in range(0, len(names), MAX_GROUP)]
    _engine.require(Counter(n for g in groups for n in g) == Counter(names),
                    "partition loses or repeats test names")
    (args.artifacts / "inventory.json").write_text(
        json.dumps({"package": PACKAGE, "names": names, "groups": groups},
                   ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    ran, done = Counter(), Counter()
    for index, group in enumerate(groups, 1):
        log = args.artifacts / f"group-{index}.jsonl"
        pattern = "^(?:" + "|".join(re.escape(n) for n in group) + ")$"
        command(["go", "test", *FLAGS, "-run", pattern, PACKAGE], log)
        _engine.execution(log, [PACKAGE], group)
        for event in _engine.events(log, [PACKAGE]):
            name = event.get("Test")
            if name and "/" not in name:
                if event["Action"] == "run":
                    ran[name] += 1
                if event["Action"] in ("pass", "skip"):
                    done[name] += 1
    _engine.require(ran == done == Counter(names), "incomplete full UI execution")
    (args.artifacts / "completion.json").write_text(
        json.dumps({"package": PACKAGE, "roots": len(names), "groups": len(groups),
                    "runs": ran, "terminal": done}, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8")
    print(f"PASS: all {len(names)} UI roots executed once in {len(groups)} PostgreSQL/race groups")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(f"test-postgres-ui: {error}", file=sys.stderr)
        sys.exit(1)
