# Reproducible full lint

Use a separate source export/checkout without generated files. Artifacts and
caches must live outside it. This tool always executes a new full analysis;
there is no cache of accepted lint results or resume mode.

Install Go 1.26.11 and golangci-lint 2.11.4 **built with Go 1.26.11**. Inspect
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
resource profile and artifact location.

The `--go` bootstrap locates the pinned installation once. Every analysis stage
then executes its checked effective Go directly with `GOTOOLCHAIN=local` and
that installation's `bin` first in the child PATH. This also binds go/packages'
own lookup without redispatch through a versioned `go1.26.11` in PATH. Bootstrap,
effective and analysis paths/hashes are fingerprinted and checked again after
each stage. `--go` must select a launcher named `go` (`go.exe` on Windows).

Untracked and ignored files participate;
only the root `.git` metadata is excluded. Unreadable directories fail inspection
and execution before analysis. Symlinks, external local module replacements
and oversized trees fail closed. Local replacements entirely inside the source
tree are included in its full digest (including third_party/webview_go). Go workspace, GOENV and injected GOFLAGS are
disabled. `GOPACKAGESDRIVER=off` is fixed and fingerprinted, disabling both an
inherited driver and automatic `gopackagesdriver` discovery in PATH. Changed
fingerprints require a new inspection, not editing old proof.

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

The real driver regression additionally requires the pinned linter executable:

```sh
LINT_RUNNER_TEST_LINTER=/tools/golangci-lint LINT_RUNNER_TEST_BOOTSTRAP_GO=/tools/go1.26.3/bin/go python3 .github/scripts/lint_runner_driver_test.py
```

It proves that both an inherited driver and PATH discovery can hide a package
from the actual linter, then checks that the runner finds its `unused` finding.
It also proves that a genuine older bootstrap can redispatch through a
package-hiding versioned Go proxy, while the runner still finds the package
using the same inspected fingerprint. The bootstrap must be a real older
Go 1.26 installation. CI obtains Go 1.26.3, builds the linter with Go 1.26.11
and runs all cases. These small fixture
checks do not substitute for full lint of OneBase. The unreadable-directory
case runs under POSIX permissions in Linux CI; Windows reports that case as skipped.
