#!/usr/bin/env python3
"""Run every Go package with race/atomic coverage, splitting UI in four and launcher in two.

The Go JSON journals, inventory, partition and individual profiles are retained
in --artifacts. A successful exit requires both test success and complete
execution evidence; a stale/partial coverage.out is never published.
"""

import argparse
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time


UI = "github.com/ivantit66/onebase/internal/ui"
LAUNCHER = "github.com/ivantit66/onebase/internal/launcher"
SERIES = [("ui", UI, 4), ("launcher", LAUNCHER, 2)]
NAME = re.compile(r"(?:Test|Example|Fuzz)\w*\Z")
BLOCK = re.compile(r"(\S+):([1-9][0-9]*)\.([1-9][0-9]*),"
                   r"([1-9][0-9]*)\.([1-9][0-9]*) ([0-9]+) ([0-9]+)\Z")
STATUS_FILE = None


def require(condition, message):
    if not condition:
        raise ValueError(message)


def save_json(path, value):
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(json.dumps(value, sort_keys=True, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)


def digest(path):
    result = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def check_fixture_path(value):
    parts = value.replace("\\", "/").split("/")
    require(parts and all(p not in ("", ".", "..") for p in parts),
            f"fixture path must be project-relative: {value}")
    root = Path.cwd().resolve()
    current = root
    for part in parts:
        require(part in {p.name for p in current.iterdir()},
                f"fixture path missing or has wrong case: {value} (component {part})")
        current = current / part
        require(current.resolve().is_relative_to(root), "fixture points outside project")


def input_fingerprint(artifacts, output):
    root = Path.cwd().resolve()
    artifacts, output = artifacts.resolve(), output.resolve()
    require(not root.is_relative_to(artifacts), "artifacts must not contain the project root")
    files = []
    size = 0
    for directory, directories, names in os.walk(root, followlinks=False):
        parent = Path(directory)
        directories[:] = sorted(d for d in directories if d != ".git"
                                and not (parent / d).resolve().is_relative_to(artifacts))
        require(not any((parent / d).is_symlink() for d in directories),
                "checkpoint source directory symlinks are unsupported")
        for name in sorted(names):
            p = parent / name
            if (name == ".git" or p.resolve().is_relative_to(artifacts)
                    or p.resolve() in (output, output.with_name(output.name + ".tmp"))):
                continue
            require(not p.is_symlink(), "checkpoint source symlinks are unsupported")
            size += p.stat().st_size
            require(len(files) < 100000 and size <= 512 * 1024 * 1024,
                    "checkpoint source too large; use an isolated checkout")
            files.append((str(p.relative_to(root)), bool(p.stat().st_mode & 0o111), digest(p)))
    tools = {}
    for tool in ("go", "node"):
        binary = shutil.which(tool)
        require(binary is not None, f"{tool} is required")
        tools[tool] = (str(Path(binary).resolve()), digest(Path(binary)))
    # Store only the digest: environment and Go proxy values can contain secrets.
    effective_go = json.loads(subprocess.check_output(["go", "env", "-json"]))
    # Go creates a fresh temporary work directory even for `go env`. Its
    # generated debug-prefix-map is not a changed compiler option. Normalize
    # only this exact generated path; retain every other flag and Go value.
    gcc_flags = effective_go.get("GOGCCFLAGS", "")
    effective_go["GOGCCFLAGS"] = re.sub(
        r"(?<!\S)(-f(?:file|debug)-prefix-map=)" + re.escape(tempfile.gettempdir())
        + r"/go-build[0-9]+=(/tmp/go-build)(?=\s|$)",
        r"\1<go-work>=\2", gcc_flags)
    data = {"root": str(root), "files": sorted(files), "tools": tools,
            "environment": sorted(os.environ.items()), "go_env": effective_go,
            "python": sys.version, "platform": sys.platform,
            "runner": digest(Path(__file__).resolve())}
    return hashlib.sha256(json.dumps(data, sort_keys=True).encode()).hexdigest()


def command(args, log, quiet=False):
    print("+ " + " ".join(args), flush=True)
    # Go may print dependency downloads/compiler diagnostics on stderr, even
    # when -list/-json stdout is valid. Keep both streams without treating
    # those diagnostics as package names or JSON events.
    errors = log.with_name(log.name + ".stderr")
    started = time.monotonic()
    status_file = log.parent / "progress.json"
    status = {"stage": log.stem, "status": "running", "command": args,
              "started_at": datetime.now(timezone.utc).isoformat(), "elapsed_seconds": 0}
    save_json(status_file, status)
    with log.open("w", encoding="utf-8") as out, errors.open("w", encoding="utf-8") as err:
        process = subprocess.Popen(args, stdout=out, stderr=err)
        while True:
            try:
                code = process.wait(timeout=10)
                break
            except subprocess.TimeoutExpired:
                status["elapsed_seconds"] = round(time.monotonic() - started, 3)
                save_json(status_file, status)
    status.update(status="command_complete" if code == 0 else "failed", exit=code,
                  elapsed_seconds=round(time.monotonic() - started, 3))
    save_json(status_file, status)
    with (log.parent / "timings.jsonl").open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(status) + "\n")
    # Preserve the entire per-test JSON (including durations) in Actions logs.
    for stream in (() if quiet and code == 0 else (log, errors)):
        with stream.open(encoding="utf-8") as src:
            for line in src:
                print(line, end="", flush=True)
    require(code == 0, f"command failed ({code}): {log}")
    return status["elapsed_seconds"]


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


def run(argv=None):
    global STATUS_FILE
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifacts", type=Path, default=Path("test-results"))
    parser.add_argument("--output", type=Path, default=Path("coverage.out"))
    parser.add_argument("--checkpoint", action="store_true", help="save validated stage results for this exact checkout")
    parser.add_argument("--resume", action="store_true", help="reuse only verified stages with unchanged inputs")
    parser.add_argument("--quiet", action="store_true", help="retain full journals but print only command summaries on success")
    parser.add_argument("--fixture", action="append", default=[], help="check an exact project-relative fixture path before Go")
    parser.add_argument("--preflight", action="append", default=[], metavar="PACKAGE=REGEXP",
                        help="run a targeted regression before the full suite; does not replace any group")
    args = parser.parse_args(argv)
    started = time.monotonic()
    args.output.unlink(missing_ok=True)
    args.artifacts.mkdir(parents=True, exist_ok=args.resume)
    STATUS_FILE = args.artifacts / "progress.json"
    save_json(STATUS_FILE, {"stage": "prepare", "status": "running"})
    for value in args.fixture:
        check_fixture_path(value)
    checkpoint = args.checkpoint or args.resume
    state_file = args.artifacts / "checkpoints.json"
    state = {"version": 1, "fingerprint": None, "stages": {}}
    if checkpoint:
        require(not any(os.environ.get(key) for key in (
            "TEST_DATABASE_URL", "TEST_DSN", "DATABASE_URL", "PGHOST", "ONEBASE_DATABASE_URL",
            "TEST_BROWSER_URL", "ONEBASE_TEST_BASE_URL", "ONEBASE_PDF_CORPUS")),
            "checkpoint requires an isolated unit environment; run external-service checks without reuse")
        state["fingerprint"] = input_fingerprint(args.artifacts, args.output)
        if args.resume:
            saved = json.loads(state_file.read_text(encoding="utf-8"))
            require(saved.get("version") == 1 and saved.get("fingerprint") == state["fingerprint"]
                    and isinstance(saved.get("stages"), dict), "checkpoint inputs changed or record invalid")
            state = saved
        else:
            save_json(state_file, state)
    require(shutil.which("node") is not None, "real Node.js is required")
    command(["node", "--version"], args.artifacts / "node.log", args.quiet)
    command(["go", "list", "./..."], args.artifacts / "packages.log", args.quiet)
    packages = (args.artifacts / "packages.log").read_text(encoding="utf-8").splitlines()
    require(packages and all(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._~/-]*", p)
                             for p in packages), "invalid package inventory")
    require(len(set(packages)) == len(packages)
            and all(packages.count(package) == 1 for _, package, _ in SERIES),
            "duplicate packages or missing exact UI/launcher package")
    others = [p for p in packages if p not in {UI, LAUNCHER}]
    require(others, "missing ungrouped packages")
    flags = ["-race", "-count=1", "-timeout=10m", "-json"]
    for index, value in enumerate(args.preflight):
        package, separator, pattern = value.partition("=")
        require(separator and package in packages and pattern, "invalid preflight PACKAGE=REGEXP")
        log = args.artifacts / f"preflight-{index}.jsonl"
        command(["go", "test", *flags, "-run", pattern, package], log, args.quiet)
        execution(log, [package])
        require(any(e.get("Action") == "run" and e.get("Test") for e in events(log, [package])),
                "preflight matched no tests")
    inventories = {}
    grouped_runs = []
    for label, package, count in SERIES:
        listing = args.artifacts / (label + "-list.jsonl")
        command(["go", "test", *flags, "-list", "^(Test|Example|Fuzz)", package], listing, args.quiet)
        names = inventory(listing, package)
        groups = partition(names, count)
        inventories[label] = {"package": package, "names": names, "groups": groups}
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
        argv = ["go", "test", *flags, *package_limit, "-covermode=atomic", f"-coverprofile={coverage}",
                *run_filter, *selected]
        saved = state["stages"].get(label) if checkpoint else None
        if saved is not None:
            require(isinstance(saved, dict) and saved.get("argv") == argv
                    and saved.get("log_sha256") == digest(log)
                    and saved.get("coverage_sha256") == digest(coverage),
                    f"checkpoint artifact changed: {label}")
            print(f"RESUME: validated {label}", flush=True)
        else:
            seconds = command(argv, log, args.quiet)
        execution(log, selected, selected_names)
        profile(coverage)
        if checkpoint:
            require(input_fingerprint(args.artifacts, args.output) == state["fingerprint"],
                    f"checkpoint inputs changed during {label}")
            if saved is None:
                state["stages"][label] = {"argv": argv, "seconds": seconds,
                    "log_sha256": digest(log), "coverage_sha256": digest(coverage)}
                save_json(state_file, state)
        profiles.append((series, coverage))
    if checkpoint:
        require(input_fingerprint(args.artifacts, args.output) == state["fingerprint"],
                "checkpoint inputs changed before coverage publication")
    merge(profiles, args.output)
    save_json(args.artifacts / "progress.json", {"stage": "complete", "status": "passed",
              "elapsed_seconds": round(time.monotonic() - started, 3), "packages": len(packages)})
    print(f"PASS: {len(packages)} packages; " + "; ".join(
        f"{len(item['names'])} {label} suites exactly once in {len(item['groups'])} groups"
        for label, item in inventories.items()))


def main(argv=None):
    global STATUS_FILE
    STATUS_FILE = None
    try:
        run(argv)
    except (OSError, ValueError) as error:
        if STATUS_FILE is not None:
            save_json(STATUS_FILE, {"stage": "validation", "status": "failed", "error": str(error)})
        raise


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(f"test-coverage: {error}", file=sys.stderr)
        sys.exit(1)
