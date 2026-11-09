#!/usr/bin/env python3
"""Distribute the existing seven race/atomic commands without reusing test results.

The plan SHA must come from the planning job output, independently of downloaded
artifacts. The required build validates every journal/profile again before merge.
The original sequential test-coverage.py entry point is unchanged.
"""

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys


ENGINE = Path(__file__).with_name("test-coverage.py")
spec = importlib.util.spec_from_file_location("coverage_engine", ENGINE)
engine = importlib.util.module_from_spec(spec)
spec.loader.exec_module(engine)
LABELS = ["other", "ui-1", "ui-2", "ui-3", "ui-4", "launcher-1", "launcher-2"]
FLAGS = ["-race", "-count=1", "-timeout=10m", "-json"]
require = engine.require


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def context():
    def output(*command):
        return subprocess.check_output(command, text=True, encoding="utf-8").strip()
    head, tree = output("git", "rev-parse", "HEAD", "HEAD^{tree}").splitlines()
    require(all(re.fullmatch(r"[0-9a-f]{40}", value) for value in (head, tree)), "invalid Git source")
    require(shutil.which("node") is not None, "real Node.js is required")
    return dict(checkout=head, tree=tree, engine_sha256=sha(ENGINE),
                runner_sha256=sha(Path(__file__)), node_version=output("node", "--version"),
                go=json.loads(output("go", "env", "-json", "GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED")))


def validate(plan):
    require(set(plan) == {"schema", "context", "packages", "series"}
            and plan["schema"] == "race-coverage-plan-v1", "invalid plan schema")
    packages = plan["packages"]
    require(isinstance(packages, list) and packages
            and all(isinstance(p, str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._~/-]*", p)
                    for p in packages), "invalid package inventory")
    require(len(set(packages)) == len(packages)
            and all(packages.count(p) == 1 for _, p, _ in engine.SERIES),
            "duplicate packages or missing exact UI/launcher package")
    require(set(packages) - {engine.UI, engine.LAUNCHER}, "missing ungrouped packages")
    require(set(plan["series"]) == {label for label, _, _ in engine.SERIES}, "invalid series")
    for label, package, count in engine.SERIES:
        item = plan["series"][label]
        require(set(item) == {"package", "names", "groups"} and item["package"] == package,
                "invalid package series")
        names = item["names"]
        require(isinstance(names, list) and names
                and all(isinstance(n, str) and engine.NAME.fullmatch(n) and n.isidentifier() for n in names)
                and names == sorted(set(names)), "invalid suite inventory")
        require(item["groups"] == engine.partition(names, count), "partition differs from inventory")


def load(path, expected):
    require(re.fullmatch(r"[0-9a-f]{64}", expected) is not None and sha(path) == expected,
            "plan differs from authoritative job output")
    plan = json.loads(path.read_bytes())
    validate(plan)
    require(plan["context"] == context(), "checkout/tree/engine/toolchain differs from plan")
    return plan


def selection(plan, label):
    if label == "other":
        return [p for p in plan["packages"] if p not in {engine.UI, engine.LAUNCHER}], None, None
    series, number = label.rsplit("-", 1)
    item = plan["series"][series]
    return [item["package"]], item["groups"][int(number) - 1], series


def prepare(root):
    root.mkdir(parents=True, exist_ok=False)
    current = context()
    engine.command(["go", "list", "./..."], root / "packages.log")
    packages = (root / "packages.log").read_text(encoding="utf-8").splitlines()
    series = {}
    for label, package, count in engine.SERIES:
        listing = root / (label + "-list.jsonl")
        engine.command(["go", "test", *FLAGS, "-list", "^(Test|Example|Fuzz)", package], listing)
        names = engine.inventory(listing, package)
        series[label] = dict(package=package, names=names, groups=engine.partition(names, count))
    plan = dict(schema="race-coverage-plan-v1", context=current, packages=packages, series=series)
    validate(plan)
    path = root / "plan.json"
    path.write_text(json.dumps(plan, indent=2) + "\n", encoding="utf-8")
    print("plan-sha256=" + sha(path))


def run(plan, plan_sha, label, root):
    root.mkdir(parents=True, exist_ok=False)
    packages, names, _ = selection(plan, label)
    package_limit = ["-p=2"] if names is None else []
    run_filter = [] if names is None else ["-run", "^(?:" + "|".join(re.escape(n) for n in names) + ")$"]
    journal, coverage = root / "tests.jsonl", root / "coverage.out"
    engine.command(["go", "test", *FLAGS, *package_limit, "-covermode=atomic",
                    f"-coverprofile={coverage}", *run_filter, *packages], journal)
    engine.execution(journal, packages, names)
    engine.profile(coverage)
    receipt = dict(schema="race-coverage-shard-v1", label=label, plan_sha256=plan_sha,
                   context=plan["context"], journal_sha256=sha(journal),
                   stderr_sha256=sha(root / "tests.jsonl.stderr"), profile_sha256=sha(coverage))
    (root / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")


def aggregate(plan, plan_sha, root, output, prefix):
    require(re.fullmatch(r"[A-Za-z0-9-]*", prefix) is not None, "invalid artifact prefix")
    require({p.name for p in root.iterdir()} == {prefix + label for label in LABELS},
            "missing/extra/duplicate shard artifacts")
    profiles = []
    for label in LABELS:
        directory = root / (prefix + label)
        require(directory.is_dir() and not directory.is_symlink(), "invalid shard directory")
        require({p.name for p in directory.iterdir()} == {
            "tests.jsonl", "tests.jsonl.stderr", "coverage.out", "receipt.json"}, "invalid shard files")
        require(all(p.is_file() and not p.is_symlink() for p in directory.iterdir()), "invalid shard file")
        receipt = json.loads((directory / "receipt.json").read_bytes())
        require(receipt == dict(schema="race-coverage-shard-v1", label=label, plan_sha256=plan_sha,
                               context=plan["context"], journal_sha256=sha(directory / "tests.jsonl"),
                               stderr_sha256=sha(directory / "tests.jsonl.stderr"),
                               profile_sha256=sha(directory / "coverage.out")), "shard receipt mismatch")
        packages, names, series = selection(plan, label)
        engine.execution(directory / "tests.jsonl", packages, names)
        engine.profile(directory / "coverage.out")
        profiles.append((series, directory / "coverage.out"))
    engine.merge(profiles, output)
    print(f"PASS: all {len(LABELS)} shards; {len(plan['packages'])} packages; complete atomic coverage")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="mode", required=True)
    plan_parser = sub.add_parser("plan")
    plan_parser.add_argument("--artifacts", type=Path, default=Path("coverage-plan"))
    for mode in ("run", "merge"):
        child = sub.add_parser(mode)
        child.add_argument("--plan", type=Path, required=True)
        child.add_argument("--plan-sha256", required=True)
        child.add_argument("--artifacts", type=Path, required=True)
        if mode == "run":
            child.add_argument("--shard", choices=LABELS, required=True)
        else:
            child.add_argument("--output", type=Path, default=Path("coverage.out"))
            child.add_argument("--artifact-prefix", default="")
    args = parser.parse_args()
    if args.mode == "plan":
        prepare(args.artifacts)
        return
    if args.mode == "merge":
        args.output.unlink(missing_ok=True)
    plan = load(args.plan, args.plan_sha256)
    if args.mode == "run":
        run(plan, args.plan_sha256, args.shard, args.artifacts)
    else:
        aggregate(plan, args.plan_sha256, args.artifacts, args.output, args.artifact_prefix)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, TypeError, KeyError, subprocess.CalledProcessError) as error:
        print(f"test-coverage-shards: {error}", file=sys.stderr)
        sys.exit(1)
