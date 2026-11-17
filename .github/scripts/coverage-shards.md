# Parallel race/coverage CI

`test-coverage.py` and the positional `test-race-coverage.py coverage.out` remain
the sequential local entry points. CI uses the same discovery, partition,
execution and atomic-profile validators through `test-coverage-shards.py`.

The planning job discovers **all** packages plus every top-level Test, Example
and Fuzz seed suite in UI and launcher. Sorted round-robin partitioning remains
UI4/launcher2. The seventh shard runs every other package with the existing
`-p=2`. All seven retain `-race -count=1 -timeout=10m -json -covermode=atomic`.
Fuzz seeds and subtests execute through their parent suite as before.

```sh
python3 .github/scripts/test-coverage-shards.py plan
# In CI this digest comes from unit-plan.outputs, independently of artifacts.
plan_sha=$(sha256sum coverage-plan/plan.json | cut -d' ' -f1)
for shard in other ui-1 ui-2 ui-3 ui-4 launcher-1 launcher-2; do
  python3 .github/scripts/test-coverage-shards.py run \
    --plan coverage-plan/plan.json --plan-sha256 "$plan_sha" \
    --shard "$shard" --artifacts "race-results/$shard"
done
python3 .github/scripts/test-coverage-shards.py merge \
  --plan coverage-plan/plan.json --plan-sha256 "$plan_sha" \
  --artifacts race-results --output coverage.out
```

The loop above is a local, sequential reproduction; Actions distributes those
seven commands to separate machines. The plan binds the actual checkout commit
and tree, runner/engine bytes, Go version/OS/architecture/CGO and Node version.
On PR events the checkout is the synthetic merge commit, **not** the PR head.
Each consumer checks the externally supplied plan digest and its own context.
The aggregate rejects missing/extra shards, receipt mismatches, incomplete or
duplicate test/package results and incompatible atomic block shapes. It sums
all seven profiles only after validation; a stale output is removed first.

The stable required `build` context runs with `always()` and explicitly requires
successful planning and the entire matrix. Cancelled, failed, skipped or absent
full-suite shards cannot produce green `build`. Successful docs classification
alone expects skipped unit jobs; classification failure runs the full path.
All other build checks, both complete timezone reruns and the required-context
list remain unchanged. The manual emergency workflow dispatch is retained.

The race jobs cache only module downloads and compiled Go objects in private
runner directories. They do not cache journals, profiles or test verdicts.
An exact cache key with no restore-key fallback has a separate namespace;
cache misses assert empty directories before Go and record resources/timings.
No existing cache is deleted. More machines trade additional compilation and
runner-minutes for a shorter critical path; the slowest shard still bounds it.

This is independent of PR #2447's opt-in local checkpoint/resume. There is no
completed-test reuse here; the existing engine and wrapper are unchanged.
