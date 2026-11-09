"""Public plan/run/merge CLI and fail-closed workflow contracts."""

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest

import test_coverage_test as legacy


RUNNER = Path(__file__).with_name("test-coverage-shards.py")
LABELS = ["other", "ui-1", "ui-2", "ui-3", "ui-4", "launcher-1", "launcher-2"]
FAKE = legacy.FAKE_GO.replace("assert args[0] == 'test'", """
if args == ['env', '-json', 'GOVERSION', 'GOOS', 'GOARCH', 'CGO_ENABLED']:
    print(json.dumps(dict(GOVERSION='go1.26.10', GOOS='linux', GOARCH='amd64', CGO_ENABLED='1')))
    sys.exit(0)
assert args[0] == 'test'
""").replace("int(coverage.stem[-1])", "int(coverage.parent.name[-1])").replace(
    "coverage.stem ==", "coverage.parent.name ==")


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


@unittest.skipIf(os.name == "nt", "POSIX executable command fixture; actual Go fixture also runs on CI")
class ShardCLI(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="coverage-shards-public-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        go = self.bin / "go"
        go.write_text(FAKE, encoding="utf-8")
        go.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"])
        self.git("init", "-q")
        (self.root / "source.txt").write_text("source\n")
        self.git("add", "source.txt")
        self.git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-qm", "fixture")
        self.prepare()

    def tearDown(self):
        self.tmp.cleanup()

    def git(self, *args):
        subprocess.run(["git", *args], cwd=self.root, check=True, capture_output=True)

    def invoke(self, *args, ok=True, env=None):
        result = subprocess.run([sys.executable, str(RUNNER), *map(str, args)], cwd=self.root,
                                env=env or self.env, capture_output=True, text=True, timeout=60)
        if ok:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def prepare(self):
        self.invoke("plan")
        self.plan = self.root / "coverage-plan/plan.json"
        self.plan_sha = digest(self.plan)
        self.results = self.root / "results"
        self.results.mkdir()
        for label in LABELS:
            self.invoke("run", "--plan", self.plan, "--plan-sha256", self.plan_sha,
                        "--shard", label, "--artifacts", self.results / label)

    def merge(self, ok=True, plan_sha=None):
        (self.root / "coverage.out").write_text("stale success\n")
        result = self.invoke("merge", "--plan", self.plan, "--plan-sha256", plan_sha or self.plan_sha,
                             "--artifacts", self.results, ok=ok)
        self.assertEqual((self.root / "coverage.out").exists(), ok)
        return result

    def reseal(self, label, field, name):
        root = self.results / label
        receipt = json.loads((root / "receipt.json").read_bytes())
        receipt[field] = digest(root / name)
        (root / "receipt.json").write_text(json.dumps(receipt))

    def test_exact_complete_atomic_sum_and_same_commands(self):
        self.merge()
        self.assertEqual((self.root / "coverage.out").read_text().splitlines(), [
            "mode: atomic", legacy.LAUNCHER + "/sample.go:1.1,2.2 2 3",
            legacy.UI + "/sample.go:1.1,2.2 2 10",
            "github.com/ivantit66/onebase/other/sample.go:1.1,2.2 2 7"])
        calls = [json.loads(line) for line in (self.root / "calls.jsonl").read_text().splitlines()]
        tests = [call for call in calls if call[0] == "test" and "-list" not in call]
        self.assertEqual(len(tests), 7)
        self.assertEqual([call[-1] for call in tests], ["github.com/ivantit66/onebase/other"] + [legacy.UI]*4 + [legacy.LAUNCHER]*2)
        self.assertTrue(all(all(flag in call for flag in ["-race", "-count=1", "-timeout=10m", "-json", "-covermode=atomic"]) for call in tests))
        self.assertEqual(sum("-p=2" in call for call in tests), 1)

    def test_authoritative_hash_and_source(self):
        result = self.merge(ok=False, plan_sha="0"*64)
        self.assertIn("authoritative", result.stderr)
        (self.root / "source.txt").write_text("changed\n")
        self.git("add", "source.txt")
        self.git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-qm", "changed")
        self.assertIn("checkout/tree", self.merge(ok=False).stderr)

    def test_missing_extra_and_duplicate_shard(self):
        saved = self.root / "saved"
        shutil.copytree(self.results, saved)
        for mode in ("missing", "extra", "duplicate-label"):
            with self.subTest(mode=mode):
                shutil.rmtree(self.results)
                shutil.copytree(saved, self.results)
                if mode == "missing":
                    shutil.rmtree(self.results / "ui-1")
                elif mode == "extra":
                    shutil.copytree(self.results / "ui-1", self.results / "ui-5")
                else:
                    shutil.rmtree(self.results / "ui-2")
                    shutil.copytree(self.results / "ui-1", self.results / "ui-2")
                self.merge(ok=False)

    def test_changed_plan_cannot_self_authorize(self):
        plan = json.loads(self.plan.read_bytes())
        plan["series"]["ui"]["groups"][0].pop()
        self.plan.write_text(json.dumps(plan))
        self.merge(ok=False)
        self.assertIn("partition", self.merge(ok=False, plan_sha=digest(self.plan)).stderr)

    def test_receipt_context_and_profile_shape(self):
        receipt = self.results / "other/receipt.json"
        original = receipt.read_bytes()
        data = json.loads(original)
        data["context"]["go"]["CGO_ENABLED"] = "0"
        receipt.write_text(json.dumps(data))
        self.merge(ok=False)
        receipt.write_bytes(original)
        profile = self.results / "ui-4/coverage.out"
        profile.write_text(profile.read_text().replace(" 2 4", " 3 4"))
        self.reseal("ui-4", "profile_sha256", "coverage.out")
        self.assertIn("incompatible", self.merge(ok=False).stderr)

    def test_truncated_or_duplicate_journal_even_with_updated_receipt(self):
        journal = self.results / "ui-1/tests.jsonl"
        original = journal.read_text()
        rows = original.splitlines()
        for mode in ("truncated", "duplicate", "failed"):
            with self.subTest(mode=mode):
                changed = rows[:-1] if mode == "truncated" else rows + [rows[0]]
                if mode == "failed":
                    changed = rows + [json.dumps(dict(Action="fail", Package=legacy.UI))]
                journal.write_text("\n".join(changed) + "\n")
                self.reseal("ui-1", "journal_sha256", "tests.jsonl")
                self.merge(ok=False)
        journal.write_text(original)

    def test_failed_race_timeout_and_partial_runs_never_receipt(self):
        for mode in ("failed-test", "race", "timeout", "missing-execution", "missing-package-result", "missing-profile", "wrong-mode"):
            with self.subTest(mode=mode):
                root = self.root / ("failure-" + mode) / "ui-1"
                env = dict(self.env, SCENARIO=mode)
                self.invoke("run", "--plan", self.plan, "--plan-sha256", self.plan_sha,
                            "--shard", "ui-1", "--artifacts", root, ok=False, env=env)
                self.assertFalse((root / "receipt.json").exists())


class WorkflowContract(unittest.TestCase):
    def test_required_build_fails_closed_and_preserves_old_gates(self):
        text = RUNNER.parents[1].joinpath("workflows/ci.yml").read_text(encoding="utf-8")
        jobs = text.split("\n  build:\n", 1)
        self.assertEqual(len(jobs), 2)
        build = jobs[1].split("\n  launcher-webview-build:", 1)[0]
        self.assertIn("needs: [classify, unit-plan, unit-race]", build)
        self.assertIn("if: ${{ always() }}", build)
        self.assertIn('test "$PLAN_RESULT" = success && test "$RACE_RESULT" = success', build)
        self.assertIn("--plan-sha256 \"$PLAN_SHA\"", build)
        self.assertIn("merge-multiple: false", build)
        self.assertIn("shard: [" + ", ".join(LABELS) + "]", text)
        self.assertIn("fail-fast: false", text)
        race = text.split("\n  unit-race:\n", 1)[1].split("\n  build:\n", 1)[0]
        self.assertLess(race.index("uses: actions/setup-go@v5"), race.index("name: Prepare private cache paths"))
        self.assertLess(race.index("name: Prepare private cache paths"), race.index("uses: actions/cache@v4"))
        for job in ("unit-plan", "unit-race"):
            section = re.split(r"\n  [A-Za-z][\w-]*:\n", text.split("\n  " + job + ":\n", 1)[1], maxsplit=1)[0]
            # The first unindented job boundary, not nested step indentation.
            self.assertIn("always() && !cancelled()", section)
            self.assertIn("needs.classify.result != 'success'", section)
        for zone in ("Asia/Kolkata", "America/New_York"):
            self.assertIn("TZ: " + zone, build)
        for old in ("go vet ./...", "go build ./...", "CGO_ENABLED: 0", "GOOS: windows",
                    "ci_metadata_test.py", "classify-ci.sh", "workflow_dispatch:"):
            self.assertIn(old, text)
        self.assertNotIn("continue-on-error:", text)

    @unittest.skipIf(os.name == "nt", "actual POSIX workflow shell")
    def test_actual_required_gate_rejects_skips_cancellation_and_missing_results(self):
        text = RUNNER.parents[1].joinpath("workflows/ci.yml").read_text(encoding="utf-8")
        block = text.split("      - name: Require every race shard\n", 1)[1].split("      - name: Docs-only success", 1)[0]
        shell = textwrap.dedent(block.split("        run: |\n", 1)[1])
        for docs in ("true", "false"):
            for plan in ("success", "failure", "cancelled", "skipped", ""):
                for race in ("success", "failure", "cancelled", "skipped", ""):
                    with self.subTest(docs=docs, plan=plan, race=race):
                        result = subprocess.run(["/bin/sh", "-e", "-c", shell], capture_output=True,
                                                env=dict(os.environ, DOCS=docs, PLAN_RESULT=plan, RACE_RESULT=race))
                        expected = (plan == race == ("skipped" if docs == "true" else "success"))
                        self.assertEqual(result.returncode == 0, expected, result.stderr)


@unittest.skipIf(os.environ.get("COVERAGE_SHARDS_SKIP_GO") == "1", "explicit fast Python-only controls")
class RealGoCLI(unittest.TestCase):
    git = ShardCLI.git
    invoke = ShardCLI.invoke
    prepare = ShardCLI.prepare
    merge = ShardCLI.merge
    tearDown = ShardCLI.tearDown

    def setUp(self):
        # Retain the real source fixture from the existing public CLI acceptance.
        # Its setup exercises examples, fuzz seeds, subtests and newly discovered suites.
        self.tmp = tempfile.TemporaryDirectory(prefix="coverage-shards-real-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.env = dict(os.environ, GOWORK="off", GOPROXY="off", GOSUMDB="off")
        self.git("init", "-q")
        self.root.joinpath("go.mod").write_text("module github.com/ivantit66/onebase\n\ngo 1.26.0\n")
        for package in ("internal/ui", "internal/launcher", "other"):
            directory = self.root / package
            directory.mkdir(parents=True)
            name = directory.name
            (directory / "value.go").write_text("package " + name + "\nfunc Value() int { return 42 }\n")
            (directory / "value_test.go").write_text("package " + name + '''
import ("fmt"; "testing")
func TestAlpha(t *testing.T) { t.Run("one", func(t *testing.T) { if Value()!=42 { t.Fatal("value") } }) }
func TestBravo(t *testing.T) { if Value()!=42 { t.Fatal("value") } }
func TestCharlie(t *testing.T) { if Value()!=42 { t.Fatal("value") } }
func TestNewAutomaticallyDiscovered(t *testing.T) { if Value()!=42 { t.Fatal("value") } }
func ExampleValue() { fmt.Println(Value()); // Output: 42
}
func FuzzSeed(f *testing.F) { f.Add(1); f.Fuzz(func(t *testing.T, n int) { if Value()!=42 { t.Fatal("value") } }) }
''')
        self.git("add", ".")
        self.git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-qm", "fixture")
        self.prepare()

    # This class runs only the real end-to-end test, not the synthetic corruption cases.
    def test_actual_go_suites_seeds_subtests_and_atomic_merge(self):
        self.merge()
        for label in ("ui", "launcher"):
            rows = [json.loads(line) for shard in self.results.glob(label + "-*")
                    for line in (shard / "tests.jsonl").read_text().splitlines()]
            ran = [r["Test"] for r in rows if r.get("Action") == "run"]
            self.assertEqual(len(ran), len(set(ran)))
            for name in ("TestAlpha", "TestAlpha/one", "ExampleValue", "FuzzSeed", "FuzzSeed/seed#0", "TestNewAutomaticallyDiscovered"):
                self.assertIn(name, ran)
        result = subprocess.run(["go", "tool", "cover", "-func=coverage.out"], cwd=self.root,
                                env=self.env, capture_output=True, text=True, timeout=60)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("100.0%", result.stdout)


if __name__ == "__main__":
    unittest.main()
