"""Run the full pinned lint profile; never reuse an earlier success."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import signal
import subprocess
import sys
import time

GO = "go1.26.11"
LINTER = "2.11.4"


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode()


def tree_digest(root):
    rows, size = [], 0
    def walk_error(error):
        raise error
    for directory, dirs, files in os.walk(root, followlinks=False, onerror=walk_error):
        directory = Path(directory)
        if directory == root:
            dirs[:] = [name for name in dirs if name != ".git"]
            files = [name for name in files if name != ".git"]
        for name in dirs + files:
            if (directory / name).is_symlink():
                raise ValueError("symlinks are unsupported: " + str(directory / name))
        for name in sorted(files):
            path = directory / name
            if not path.is_file():
                raise ValueError("non-regular input: " + str(path))
            size += path.stat().st_size
            if len(rows) >= 100000 or size > 2 * 1024**3:
                raise ValueError("input tree exceeds 100000 files or 2 GiB; use a separate export")
            rows.append([path.relative_to(root).as_posix(), path.stat().st_mode & 0o777, digest(path)])
        dirs.sort()
    return {"sha256": hashlib.sha256(canonical(sorted(rows))).hexdigest(), "files": len(rows), "bytes": size}


def environment(args):
    target_os, target_arch = args.target.split("/")
    env = dict(os.environ)
    go = Path(shutil.which(args.go) or args.go).resolve(strict=True)
    # Use the bootstrap only to locate the pinned installation. An older
    # bootstrap may dispatch through a versioned executable in PATH.
    env["PATH"] = str(go.parent) + os.pathsep + env.get("PATH", "")
    analysis_go = shutil.which("go", path=env["PATH"])
    if analysis_go is None or Path(analysis_go).resolve(strict=True) != go:
        raise ValueError("--go must select the go/go.exe launcher in its directory")
    env.update(GOTOOLCHAIN=GO, GOENV="off", GOOS=target_os, GOARCH=target_arch,
               CGO_ENABLED="0", GOMAXPROCS="4", GOMEMLIMIT="7GiB", GOGC="100",
               GOWORK="off", GOFLAGS="", GOEXPERIMENT="", GOAMD64="v1", GOCACHEPROG="", GOPACKAGESDRIVER="off",
               GOCACHE=str(args.artifacts / "go-build"), GOTMPDIR=str(args.artifacts / "go-tmp"),
               GOLANGCI_LINT_CACHE=str(args.artifacts / "lint-cache"),
               TMP=str(args.artifacts / "tmp"), TEMP=str(args.artifacts / "tmp"))
    selected = json.loads(subprocess.check_output(
        [str(go), "env", "-json", "GOROOT", "GOVERSION"],
        cwd=args.root, env=env, timeout=60, text=True))
    if selected["GOVERSION"] != GO:
        raise ValueError("effective Go version differs")
    effective_go = (Path(selected["GOROOT"]) / "bin" / ("go.exe" if os.name == "nt" else "go")).resolve(strict=True)
    # Every actual stage, including go/packages' own lookup, executes this
    # binary directly with toolchain switching disabled.
    env["PATH"] = str(effective_go.parent) + os.pathsep + env["PATH"]
    env["GOTOOLCHAIN"] = "local"
    return env, go, effective_go


def describe(args, env, go, effective_go):
    analysis_go = Path(shutil.which("go", path=env["PATH"]) or "").resolve(strict=True)
    if analysis_go != effective_go:
        raise ValueError("linter Go lookup differs from the checked effective Go")
    linter = args.linter.resolve(strict=True)
    def output(command):
        return subprocess.check_output(command, cwd=args.root, env=env, timeout=60, text=True).strip()
    go_version = output([str(effective_go), "version"])
    lint_version = output([str(linter), "version"])
    if not go_version.startswith("go version " + GO + " "):
        raise ValueError("expected " + GO + ": " + go_version)
    if "version " + LINTER + " " not in lint_version or "built with " + GO not in lint_version:
        raise ValueError("expected golangci-lint " + LINTER + " built with " + GO)
    go_env = json.loads(output([str(effective_go), "env", "-json", "GOROOT", "GOVERSION", "GOHOSTOS", "GOHOSTARCH"]))
    if go_env["GOVERSION"] != GO:
        raise ValueError("effective Go version differs")
    reported_go = (Path(go_env["GOROOT"]) / "bin" / effective_go.name).resolve(strict=True)
    if reported_go != effective_go:
        raise ValueError("effective Go root differs from its checked executable")
    module = json.loads(output([str(effective_go), "mod", "edit", "-json"]))
    for item in module.get("Replace") or []:
        if not item["New"].get("Version"):
            local = (args.root / item["New"]["Path"]).resolve(strict=True)
            if not local.is_relative_to(args.root) or any(part.lower() == ".git" for part in local.relative_to(args.root).parts):
                raise ValueError("external or .git local replace cannot be bound")
    if (args.root / ".go-version").read_text().strip().removeprefix("go") != GO.removeprefix("go"):
        raise ValueError("project .go-version differs from pinned profile")
    context = {"schema": 1, "root": str(args.root), "tree": tree_digest(args.root),
               "config_sha256": digest(args.root / ".golangci.yml"),
               "runner_sha256": digest(Path(__file__)), "python_sha256": digest(Path(sys.executable)),
               "host": {"platform": platform.platform(), "go": go_env},
               "go": {"path": str(go), "launcher_sha256": digest(go), "effective_path": str(effective_go), "effective_sha256": digest(effective_go), "version": go_version,
                      "analysis_path": str(analysis_go), "analysis_sha256": digest(analysis_go)},
               "linter": {"path": str(linter), "sha256": digest(linter), "version": lint_version},
               "profile": {key: env[key] for key in ["GOTOOLCHAIN", "GOENV", "GOOS", "GOARCH", "CGO_ENABLED", "GOMAXPROCS", "GOMEMLIMIT", "GOGC", "GOWORK", "GOFLAGS", "GOEXPERIMENT", "GOAMD64", "GOCACHEPROG", "GOPACKAGESDRIVER"]},
               "artifacts": str(args.artifacts), "export_deadline_seconds": args.stage_timeout,
               "lint_deadline": str(args.stage_timeout) + "s", "outer_lint_deadline_seconds": args.stage_timeout + 30}
    return {"sha256": hashlib.sha256(canonical(context)).hexdigest(), "context": context}


def execute(command, root, env, log, timeout):
    started = time.monotonic()
    options = {"creationflags": subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.CREATE_NO_WINDOW} if os.name == "nt" else {"start_new_session": True}
    with log.open("xb") as stream:
        process = subprocess.Popen(command, cwd=root, env=env, stdout=stream, stderr=subprocess.STDOUT, **options)
        try:
            code = process.wait(timeout=timeout)
        except (subprocess.TimeoutExpired, KeyboardInterrupt):
            if os.name == "nt":
                subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], capture_output=True, timeout=30, check=False)
            else:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            process.wait(timeout=30)
            code = 124
    return {"command": command, "exit": code, "seconds": time.monotonic() - started, "log_sha256": digest(log)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["inspect", "run"])
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--linter", type=Path, required=True)
    parser.add_argument("--go", default="go")
    parser.add_argument("--target", choices=["linux/amd64", "windows/amd64"], required=True)
    parser.add_argument("--artifacts", type=Path, required=True)
    parser.add_argument("--expect", type=Path)
    parser.add_argument("--stage-timeout", type=int, choices=range(1, 601), default=600, metavar="1..600")
    args = parser.parse_args()
    args.root = args.root.resolve(strict=True)
    args.artifacts = args.artifacts.resolve()
    if args.artifacts.is_relative_to(args.root) or args.root.is_relative_to(args.artifacts):
        parser.error("artifacts must be outside and separate from the source tree")
    env, bootstrap_go, effective_go = environment(args)
    if args.action == "inspect":
        print(json.dumps(describe(args, env, bootstrap_go, effective_go), indent=2))
        return 0
    if args.expect is None:
        parser.error("run requires --expect with the reviewed inspect JSON")
    expected = json.loads(args.expect.read_text(encoding="utf-8"))
    if expected != describe(args, env, bootstrap_go, effective_go):
        raise ValueError("input/tool/profile fingerprint differs; full new inspection required")
    args.artifacts.mkdir(parents=True, exist_ok=False)
    for name in ["go-build", "go-tmp", "lint-cache", "tmp"]:
        (args.artifacts / name).mkdir()
    journal = {"fingerprint": expected, "stages": [], "success": False}
    try:
        go = expected["context"]["go"]["effective_path"]
        stages = [("modules", [go, "mod", "verify"], args.stage_timeout),
                  ("exports", [go, "list", "-deps", "-test", "-export", "-p=4", "./..."], args.stage_timeout),
                  ("lint", [str(args.linter.resolve()), "run", "-v", "-j", "4", "--timeout", str(args.stage_timeout) + "s", "--config", ".golangci.yml", "--cpu-profile-path", str(args.artifacts / "lint.cpu.pprof"), "./..."], args.stage_timeout + 30)]
        for name, command, timeout in stages:
            row = execute(command, args.root, env, args.artifacts / (name + ".log"), timeout)
            row["stage"] = name
            journal["stages"].append(row)
            if row["exit"]:
                raise ValueError(name + " failed with exit " + str(row["exit"]))
            if describe(args, env, bootstrap_go, effective_go) != expected:
                raise ValueError("inputs changed during " + name)
        journal["success"] = True
    except Exception as error:
        journal["error"] = str(error)
    finally:
        profile = args.artifacts / "lint.cpu.pprof"
        if profile.exists():
            journal["cpu_profile_sha256"] = digest(profile)
        (args.artifacts / "result.json").write_text(json.dumps(journal, indent=2), encoding="utf-8")
    print(json.dumps({"success": journal["success"], "result": str(args.artifacts / "result.json"), "error": journal.get("error")}))
    return 0 if journal["success"] else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print("lint-runner: " + str(error), file=sys.stderr)
        raise SystemExit(1)
