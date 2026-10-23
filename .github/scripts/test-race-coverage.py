#!/usr/bin/env python3
"""Run the full race/atomic suite, bounding large package process budgets."""

from collections import Counter
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

# Each process keeps the original 10m timeout. The build job still has 30m.
BATCH_SIZE = 16
BATCHED_PACKAGES = ("internal/auth", "internal/launcher", "internal/ui")
ISOLATED_PACKAGE = "internal/dsl/interpreter"
OTHER_CONCURRENCY = 4


def discover_names(output):
    names = []
    for line in output.splitlines():
        if line.startswith(("Test", "Example", "Fuzz")):
            if not re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line):
                raise ValueError(f"invalid discovered name: {line!r}")
            names.append(line)
    if not names or len(set(names)) != len(names):
        raise ValueError("empty or duplicate test inventory")
    return names


def verify_inventory(expected, actual):
    if not expected or len(set(expected)) != len(expected) or Counter(expected) != Counter(actual):
        raise ValueError(f"execution differs from discovery: expected={Counter(expected)}, actual={Counter(actual)}")


def merge_profiles(profiles, destination):
    counts, statements = {}, {}
    if not profiles:
        raise ValueError("no coverage profiles")
    for profile in profiles:
        blocks = set()
        with profile.open(encoding="utf-8") as stream:
            if stream.readline() != "mode: atomic\n":
                raise ValueError(f"not atomic coverage: {profile}")
            for line in stream:
                location, size, count = line.split()
                size, count = int(size), int(count)
                if (not re.fullmatch(r".+:\d+\.\d+,\d+\.\d+", location)
                        or location in blocks or size < 0 or count < 0
                        or (location in statements and statements[location] != size)):
                    raise ValueError(f"invalid coverage block: {line!r}")
                blocks.add(location)
                statements[location] = size
                counts[location] = counts.get(location, 0) + count
        if not blocks:
            raise ValueError(f"empty coverage: {profile}")
    temporary = destination.with_name(destination.name + ".tmp")
    with temporary.open("w", encoding="utf-8", newline="\n") as stream:
        stream.write("mode: atomic\n")
        for location in sorted(counts):
            stream.write(f"{location} {statements[location]} {counts[location]}\n")
    temporary.replace(destination)


def discover_package(package, binary, report):
    # Use the actual race/coverage build, including the current platform's tags.
    args = ["go", "test", "-c", "-race", "-covermode=atomic", "-o", str(binary), package]
    started = time.monotonic()
    entry = {"kind": "discovery", "args": args, "package": package}
    report["processes"].append(entry)
    try:
        subprocess.run(args, check=True)
        output = subprocess.check_output([str(binary), "-test.list=."], text=True, encoding="utf-8")
        names = discover_names(output)
        entry["names"] = names
        entry["success"] = True
        return names
    finally:
        entry["elapsed"] = time.monotonic() - started


def run_tests(packages, profile, names=None, *, report=None, log=None, concurrency=None):
    args = ["go", "test", "-json", "-count=1", "-race", "-timeout=10m",
            "-covermode=atomic", f"-coverprofile={profile}"]
    if concurrency is not None:
        args += ["-p", str(concurrency)]
    if names is not None:
        args += ["-run", "^(" + "|".join(re.escape(name) for name in names) + ")$"]
    args += packages
    print("Running:", *args, flush=True)
    runs, completed, outcomes = [], [], []
    entry = {"kind": "tests", "args": args, "packages": packages, "names": names, "tests": outcomes}
    if report is not None:
        report["processes"].append(entry)
    started = time.monotonic()
    stream = log.open("w", encoding="utf-8") if log else None
    try:
        with subprocess.Popen(args, stdout=subprocess.PIPE, text=True, encoding="utf-8") as proc:
            try:
                for line in proc.stdout:
                    if stream:
                        stream.write(line)
                    print(line, end="", flush=True)
                    event = json.loads(line)
                    name = event.get("Test", "")
                    if name and "/" not in name:
                        if event.get("Action") == "run":
                            runs.append(name)
                        if event.get("Action") in {"pass", "skip", "fail"}:
                            completed.append(name)
                            outcomes.append({"package": event.get("Package"), "name": name,
                                             "action": event["Action"], "elapsed": event.get("Elapsed", 0)})
            except BaseException:
                proc.kill()
                proc.wait()
                raise
            entry["exit"] = proc.wait()
            if proc.returncode:
                raise subprocess.CalledProcessError(proc.returncode, args)
        if names is not None:
            verify_inventory(names, runs)
            verify_inventory(names, completed)
        entry["success"] = True
        return runs
    finally:
        entry["elapsed"] = time.monotonic() - started
        if stream:
            stream.close()


def main(destination=None, report_directory=None):
    destination = Path(destination or "coverage.out")
    directory = Path(report_directory or str(destination) + ".report")
    directory.mkdir(parents=True, exist_ok=True)
    destination.unlink(missing_ok=True)  # Never retain an older successful run.
    report = {"success": False, "processes": [], "inventories": {}, "batch_size": BATCH_SIZE,
              "other_concurrency": OTHER_CONCURRENCY}
    started = time.monotonic()
    try:
        packages = subprocess.check_output(["go", "list", "./..."], text=True, encoding="utf-8").splitlines()
        if not packages or len(packages) != len(set(packages)):
            raise ValueError("empty or duplicate package inventory")
        report["packages"] = packages
        selected = {}
        for suffix in (*BATCHED_PACKAGES, ISOLATED_PACKAGE):
            matches = [package for package in packages if package.endswith("/" + suffix)]
            if len(matches) != 1:
                raise ValueError(f"package inventory must contain exactly one {suffix}")
            selected[suffix] = matches[0]
        other = [package for package in packages if package not in selected.values()]
        if not other:
            raise ValueError("empty remaining package inventory")
        with tempfile.TemporaryDirectory(prefix="onebase-race-") as temporary:
            temporary = Path(temporary)
            for index, suffix in enumerate(BATCHED_PACKAGES):
                package = selected[suffix]
                report["inventories"][package] = discover_package(package, temporary / f"tests-{index}", report)
            profiles = []
            # Interpreter runs alone: the XML budget checks must not compete with
            # another test process launched by this runner. Do not relax guards.
            for label, group, concurrency in [("interpreter", [selected[ISOLATED_PACKAGE]], None),
                                               ("other", other, OTHER_CONCURRENCY)]:
                profile = temporary / f"{label}.out"
                run_tests(group, profile, report=report, log=directory / f"{label}.jsonl", concurrency=concurrency)
                profiles.append(profile)
            for suffix in BATCHED_PACKAGES:
                package = selected[suffix]
                names, runs = report["inventories"][package], []
                for start in range(0, len(names), BATCH_SIZE):
                    label = suffix.rsplit("/", 1)[-1] + f"-{start // BATCH_SIZE + 1}"
                    profile = temporary / f"{label}.out"
                    runs.extend(run_tests([package], profile, names[start:start + BATCH_SIZE],
                                          report=report, log=directory / f"{label}.jsonl"))
                    profiles.append(profile)
                verify_inventory(names, runs)
            merge_profiles(profiles, destination)
        report["success"] = True
        print(f"Full package/test inventories verified; coverage: {destination}", flush=True)
    except BaseException as error:
        report["error"] = str(error)
        raise
    finally:
        report["elapsed"] = time.monotonic() - started
        (directory / "report.json").write_text(json.dumps(report, indent=2), encoding="utf-8")


if __name__ == "__main__":
    main(*sys.argv[1:])
