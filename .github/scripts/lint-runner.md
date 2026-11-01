# Reproducible full lint

Use a separate source export/checkout without generated files. Artifacts and
caches must live outside it. This tool always executes a new full analysis;
there is no cache of accepted lint results or resume mode.

Install Go 1.26.10 and golangci-lint 2.11.4 **built with Go 1.26.10**. Inspect
inputs before admitting their exact fingerprint for the intended operation:

```sh
python3 .github/scripts/lint-runner.py inspect --root /work/source --linter /tools/golangci-lint --target linux/amd64 --artifacts /work/lint-attempt > /work/inputs.json
python3 .github/scripts/lint-runner.py run --root /work/source --linter /tools/golangci-lint --target linux/amd64 --artifacts /work/lint-attempt --expect /work/inputs.json
```

PowerShell equivalents use `python` and absolute Windows paths. Save inspection
stdout as UTF-8 without BOM. A successful inspection is **not** a lint pass.
`--target windows/amd64` selects native Windows analysis when hosted on Windows.
Linux-target analysis hosted on Windows is recorded explicitly and never counts
as native Linux execution or as passing Windows lint.

The fingerprint binds the full file tree, file modes, config, runner, Python,
Go launcher/effective binary and versions, linter binary/version, host, target,
resource profile and artifact location. Untracked and ignored files participate;
only the root `.git` metadata is excluded. Symlinks, local module replacements
and oversized trees fail closed. Go workspace, GOENV and injected GOFLAGS are
disabled. Changed fingerprints require a new inspection, not editing old proof.

The fixed full profile uses CGO=0, four analysis CPUs, 7 GiB Go heap soft limit,
the unchanged project config, `go mod verify`, and full test-inclusive package
export preparation before lint. All configured linters run. Default stage
limits are 600 seconds; lint retains a 600-second internal limit and a separate
630-second outer deadline. `--stage-timeout` can only **reduce** these limits,
and its value changes the fingerprint. A timeout kills the process group/tree
and is an unsuccessful result. Setup success alone never counts as lint success.

`result.json` records all command exits, elapsed times, input fingerprint, log
hashes and the CPU profile hash. Inputs are checked again after every stage;
changed files or tools invalidate the attempt. Outputs use a new artifact
directory; old results are never overwritten. Cache and dependency files are
not portable accepted proof: module verification runs on every new attempt.
The hash identifies the bytes inspected, not their trustworthiness. OWNER must
still authorize the actual source/profile; independent REVIEW, CI8 and ship
remain mandatory. This utility does not install or modify active procedures.

Runner regression checks use instrumented executables through the public CLI:

```sh
python3 .github/scripts/lint_runner_test.py
```
