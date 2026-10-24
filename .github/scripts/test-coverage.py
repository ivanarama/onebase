#!/usr/bin/env python3
"""Run every Go package with race/atomic coverage, splitting UI in four and launcher in two.

The Go JSON journals, inventory, partition and individual profiles are retained
in --artifacts. A successful exit requires both test success and complete
execution evidence; a stale/partial coverage.out is never published.
"""

import argparse
from collections import Counter
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys


UI = "github.com/ivantit66/onebase/internal/ui"
LAUNCHER = "github.com/ivantit66/onebase/internal/launcher"
SERIES = [("ui", UI, 4), ("launcher", LAUNCHER, 2)]
NAME = re.compile(r"(?:Test|Example|Fuzz)\w*\Z")
BLOCK = re.compile(r"(\S+):([1-9][0-9]*)\.([1-9][0-9]*),"
                   r"([1-9][0-9]*)\.([1-9][0-9]*) ([0-9]+) ([0-9]+)\Z")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def command(args, log):
    print("+ " + " ".join(args), flush=True)
    # Go may print dependency downloads/compiler diagnostics on stderr, even
    # when -list/-json stdout is valid. Keep both streams without treating
    # those diagnostics as package names or JSON events.
    errors = log.with_name(log.name + ".stderr")
    with log.open("w", encoding="utf-8") as out, errors.open("w", encoding="utf-8") as err:
        result = subprocess.run(args, stdout=out, stderr=err)
    # Preserve the entire per-test JSON (including durations) in Actions logs.
    for stream in (log, errors):
        with stream.open(encoding="utf-8") as src:
            for line in src:
                print(line, end="", flush=True)
    require(result.returncode == 0, f"command failed ({result.returncode}): {log}")


def events(log, packages):
    result = []
    for line in log.read_text(encoding="utf-8").splitlines():
        event = json.loads(line)
        require(isinstance(event, dict) and event.get("Package") in packages,
                f"invalid JSON test event in {log}")
        require(event.get("Action") != "fail", f"failed test event in {log}")
        result.append(event)
    terminal = Counter(e["Package"] for e in result
                       if not e.get("Test") and e.get("Action") in ("pass", "skip"))
    require(terminal == Counter(packages), f"incomplete package results in {log}")
    return result


def inventory(log, package):
    names = []
    for event in events(log, [package]):
        if event.get("Action") != "output":
            continue
        for line in event.get("Output", "").splitlines():
            if NAME.fullmatch(line) and line.isidentifier():
                names.append(line)
            else:
                require(re.fullmatch(r"ok\s+" + re.escape(package) + r"\s+[^\s]+.*", line),
                        f"unexpected inventory output: {line!r}")
    require(names and len(set(names)) == len(names), f"empty/duplicate inventory: {package}")
    return sorted(names)


def partition(names, count):
    groups = [names[i::count] for i in range(count)]
    require(all(groups), f"{count} nonempty groups required")
    require(Counter(n for g in groups for n in g) == Counter(names),
            "partition loses or repeats test names")
    return groups


def execution(log, packages, names=None):
    journal = events(log, packages)
    started = Counter((e["Package"], e["Test"]) for e in journal
                      if e.get("Action") == "run" and e.get("Test"))
    finished = Counter((e["Package"], e["Test"]) for e in journal
                       if e.get("Action") in ("pass", "skip") and e.get("Test"))
    require(started == finished and all(count == 1 for count in started.values()),
            f"incomplete/duplicate test results in {log}")
    if names is not None:
        ran = Counter(e["Test"] for e in journal if e.get("Action") == "run"
                      and "/" not in e.get("Test", ""))
        done = Counter(e["Test"] for e in journal if e.get("Action") in ("pass", "skip")
                       and e.get("Test") and "/" not in e["Test"])
        require(ran == done == Counter(names), f"missing/duplicate grouped execution in {log}")


def profile(path):
    lines = path.read_text(encoding="utf-8").splitlines()
    require(lines and lines[0] == "mode: atomic", f"invalid atomic profile: {path}")
    blocks = {}
    for line in lines[1:]:
        match = BLOCK.fullmatch(line)
        require(match is not None, f"invalid coverage record: {path}: {line!r}")
        file, sl, sc, el, ec, statements, count = match.groups()
        coordinates = tuple(map(int, (sl, sc, el, ec)))
        require(coordinates[:2] <= coordinates[2:], f"reversed coverage block: {path}")
        key = (file, *coordinates)
        require(key not in blocks, f"duplicate coverage block: {path}")
        blocks[key] = (int(statements), int(count))
    require(blocks, f"empty coverage profile: {path}")
    return blocks


def merge(profiles, output):
    all_blocks = {}
    shapes = {}
    for series, path in profiles:
        blocks = profile(path)
        # Shape must match within a package series, not between UI/launcher.
        if series is not None:
            shape = {key: value[0] for key, value in blocks.items()}
            require(series not in shapes or shape == shapes[series],
                    f"incompatible {series} coverage profiles")
            shapes[series] = shape
        for key, (statements, count) in blocks.items():
            previous_statements, previous_count = all_blocks.get(key, (statements, 0))
            require(previous_statements == statements, "incompatible statement counts")
            all_blocks[key] = (statements, previous_count + count)
    temporary = output.with_name(output.name + ".tmp")
    with temporary.open("w", encoding="utf-8", newline="\n") as out:
        out.write("mode: atomic\n")
        for (file, sl, sc, el, ec), (statements, count) in sorted(all_blocks.items()):
            out.write(f"{file}:{sl}.{sc},{el}.{ec} {statements} {count}\n")
    temporary.replace(output)


LARGE_CATALOG = "TestRefComboboxLargeCatalogPublicHTTP"


def verify_acceptance_workflow(workflow):
    job = re.search(r"^  e2e:\n(.*?)(?=^  [^ #\n]|\Z)", workflow, re.M | re.S)
    if not job or re.search(r"^    continue-on-error:", job[1], re.M):
        raise ValueError("mandatory e2e job missing or optional")
    steps = re.split(r"^      - name: ", job[1], flags=re.M)[1:]
    condition = "if: ${{ needs.classify.result != 'success' || needs.classify.outputs.mode != 'docs' }}"
    expected = [
        ["Managed reference combobox acceptance", condition, "env:",
         "ONEBASE_REQUIRE_REF_BROWSER: '1'",
         "run: go test -count=1 -timeout 3m -v ./internal/ui -run '^TestManagedRefComboboxBrowser$'"],
        ["Large catalog managed reference acceptance", condition,
         "run: go test -count=1 -timeout 3m -v ./internal/ui -run '^" + LARGE_CATALOG + "$'"],
    ]
    for required in expected:
        matches = [[line.strip() for line in step.splitlines()
                    if line.strip() and not line.lstrip().startswith("#")]
                   for step in steps if step.splitlines()[0] == required[0]]
        if matches != [required]:
            raise ValueError("missing, duplicate or modified mandatory acceptance: " + required[0])



def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifacts", type=Path, default=Path("test-results"))
    parser.add_argument("--output", type=Path, default=Path("coverage.out"))
    args = parser.parse_args(argv)
    args.output.unlink(missing_ok=True)
    args.artifacts.mkdir(parents=True, exist_ok=False)
    require(shutil.which("node") is not None, "real Node.js is required")
    command(["node", "--version"], args.artifacts / "node.log")
    command(["go", "list", "./..."], args.artifacts / "packages.log")
    packages = (args.artifacts / "packages.log").read_text(encoding="utf-8").splitlines()
    require(packages and all(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._~/-]*", p)
                             for p in packages), "invalid package inventory")
    require(len(set(packages)) == len(packages)
            and all(packages.count(package) == 1 for _, package, _ in SERIES),
            "duplicate packages or missing exact UI/launcher package")
    others = [p for p in packages if p not in {UI, LAUNCHER}]
    require(others, "missing ungrouped packages")
    flags = ["-race", "-count=1", "-timeout=10m", "-json"]
    verify_acceptance_workflow(Path(".github/workflows/ci.yml").read_text(encoding="utf-8"))
    inventories = {}
    grouped_runs = []
    for label, package, count in SERIES:
        listing = args.artifacts / (label + "-list.jsonl")
        command(["go", "test", *flags, "-list", "^(Test|Example|Fuzz)", package], listing)
        names = inventory(listing, package)
        delegated = [LARGE_CATALOG] if package == UI else []
        require(not delegated or names.count(LARGE_CATALOG) == 1, "missing exact mandatory e2e inventory")
        race_names = [name for name in names if name not in delegated]
        require(Counter(race_names + delegated) == Counter(names), "delegation loses or repeats suites")
        groups = partition(race_names, count)
        inventories[label] = {"package": package, "names": names, "groups": groups, "mandatory_e2e": delegated}
        grouped_runs.extend((f"{label}-{i+1}", [package], group, label)
                            for i, group in enumerate(groups))
    (args.artifacts / "inventory.json").write_text(
        json.dumps({"packages": packages, "series": inventories}, indent=2) + "\n",
        encoding="utf-8")
    runs = [("other", others, None, None)] + grouped_runs
    profiles = []
    for label, selected, selected_names, series in runs:
        coverage = args.artifacts / (label + ".out")
        log = args.artifacts / (label + ".jsonl")
        package_limit = ["-p=2"] if selected_names is None else []
        run_filter = [] if selected_names is None else [
            "-run", "^(?:" + "|".join(re.escape(n) for n in selected_names) + ")$"]
        command(["go", "test", *flags, *package_limit, "-covermode=atomic", f"-coverprofile={coverage}",
                 *run_filter, *selected], log)
        execution(log, selected, selected_names)
        profile(coverage)
        profiles.append((series, coverage))
    merge(profiles, args.output)
    print(f"PASS: {len(packages)} packages; " + "; ".join(
        f"{len(item['names'])} {label} suites accounted for in {len(item['groups'])} race groups + mandatory e2e {item['mandatory_e2e']}"
        for label, item in inventories.items()))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(f"test-coverage: {error}", file=sys.stderr)
        sys.exit(1)
