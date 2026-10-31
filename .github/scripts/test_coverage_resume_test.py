"""Public CLI tests for resumable full coverage and early regressions."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

HERE = Path(__file__).parent
spec = importlib.util.spec_from_file_location("coverage_fixtures", HERE / "test_coverage_test.py")
fixtures = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixtures)


class ResumeCLI(unittest.TestCase):
    runner = HERE / 'test-coverage.py'

    def cli_command(self, runner):
        return [sys.executable, str(runner), '--checkpoint']

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        script = fixtures.FAKE_GO.replace("open('calls.jsonl', 'a')", "open(os.environ['CALLS_PATH'], 'a')")
        script = script.replace("mode = os.environ.get('SCENARIO', '')", """mode = os.environ.get('SCENARIO', '')
if args == ['env', '-json']:
    path = pathlib.Path('artifacts/go-env-value')
    print(json.dumps({'GOOS': path.read_text() if path.exists() else 'darwin'}))
    sys.exit(0)
""")
        script = script.replace("assert '-covermode=atomic' in args", """
if '-covermode=atomic' not in args:
    package=args[-1]
    if '-run' in args:
        pattern=args[args.index('-run')+1]
        for name in names:
            if re.search(pattern, name):
                emit('run', package, Test=name)
                emit('pass', package, Test=name)
    emit('pass', package)
    sys.exit(0)
assert '-covermode=atomic' in args
""")
        script = script.replace("package = args[-1]\nassert", """package = args[-1]
if os.environ.get('MUTATE_SOURCE'):
    pathlib.Path('source.go').write_text('package changed_during_test\\n')
if os.environ.get('FAIL_ONCE') and coverage.stem == 'ui-2':
    marker=pathlib.Path('artifacts/failed-once')
    if not marker.exists():
        marker.write_text('failed')
        emit('fail', package)
        sys.exit(1)
assert""")
        go = self.bin / "go"
        go.write_text(script, encoding="utf8")
        go.chmod(0o755)
        (self.root / "source.go").write_text("package example\n")
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'],
                        CALLS_PATH=str(self.root / "artifacts/calls.jsonl"))

    def tearDown(self):
        self.tmp.cleanup()

    def run_cli(self, *args):
        return subprocess.run([sys.executable, str(HERE / "test-coverage.py"),
            "--artifacts", "artifacts", "--quiet", *args], cwd=self.root, env=self.env,
            capture_output=True, text=True, encoding="utf8", timeout=60)

    def full_calls(self):
        rows = [json.loads(line) for line in (self.root / "artifacts/calls.jsonl").read_text().splitlines()]
        return [row for row in rows if any(a.startswith('-coverprofile=') for a in row)]

    def prepare(self):
        result = self.run_cli("--checkpoint")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_resume_revalidates_all_seven_profiles_without_rerunning(self):
        self.prepare()
        expected = (self.root / 'coverage.out').read_bytes()
        result = self.run_cli("--resume")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.full_calls()), 7)
        self.assertEqual(result.stdout.count("RESUME: validated"), 7)
        self.assertEqual((self.root / 'coverage.out').read_bytes(), expected)

    def test_failed_stage_is_rerun_and_completed_stages_are_kept(self):
        self.env['FAIL_ONCE'] = '1'
        result = self.run_cli("--checkpoint")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / 'coverage.out').exists())
        result = self.run_cli("--resume")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stdout.count("RESUME: validated"), 2)
        self.assertEqual(len(self.full_calls()), 8)

    def test_changed_source_is_not_reused(self):
        self.prepare()
        (self.root / 'source.go').write_text('package different\n')
        result = self.run_cli('--resume')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('inputs changed', result.stderr)
        self.assertFalse((self.root / 'coverage.out').exists())

    def test_changed_environment_is_not_reused(self):
        self.prepare()
        self.env['NEW_EXECUTION_INPUT'] = 'changed'
        self.assertNotEqual(self.run_cli('--resume').returncode, 0)

    def test_changed_effective_go_environment_is_not_reused(self):
        self.prepare()
        (self.root / 'artifacts/go-env-value').write_text('linux')
        self.assertNotEqual(self.run_cli('--resume').returncode, 0)

    def test_mutable_database_context_cannot_reuse_stages(self):
        self.env['TEST_DATABASE_URL'] = 'postgres://localhost/example'
        result = self.run_cli('--checkpoint')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('isolated unit environment', result.stderr)

    def test_tampered_journal_is_rejected(self):
        self.prepare()
        (self.root / 'artifacts/ui-1.jsonl').write_text('forged\n')
        result = self.run_cli('--resume')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / 'coverage.out').exists())
        self.assertEqual(json.loads((self.root / 'artifacts/progress.json').read_text())['status'], 'failed')

    def test_changed_tool_is_not_reused(self):
        self.prepare()
        with (self.bin / 'go').open('a') as stream:
            stream.write('\n# different tool binary\n')
        self.assertNotEqual(self.run_cli('--resume').returncode, 0)

    def test_source_mutation_during_stage_is_rejected(self):
        self.env['MUTATE_SOURCE'] = '1'
        result = self.run_cli('--checkpoint')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('inputs changed during', result.stderr)
        self.assertFalse((self.root / 'coverage.out').exists())

    def test_timings_and_final_status_are_saved(self):
        self.prepare()
        status = json.loads((self.root / 'artifacts/progress.json').read_text())
        self.assertEqual((status['stage'], status['status']), ('complete', 'passed'))
        timings = [json.loads(x) for x in (self.root / 'artifacts/timings.jsonl').read_text().splitlines()]
        self.assertEqual(len(timings), 11)
        self.assertTrue(all(x['elapsed_seconds'] >= 0 and x['exit'] == 0 for x in timings))

    def test_fixture_case_fails_before_go(self):
        (self.root / 'Fixtures').mkdir()
        (self.root / 'Fixtures/Exact.yaml').write_text('value: true\n')
        result = self.run_cli('--fixture', 'fixtures/exact.yaml')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('wrong case', result.stderr)
        self.assertFalse((self.root / 'artifacts/calls.jsonl').exists())

    def test_preflight_runs_before_full_suite_and_keeps_all_groups(self):
        result = self.run_cli('--preflight', fixtures.UI + '=^TestAlpha$')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        calls = [json.loads(x) for x in (self.root / 'artifacts/calls.jsonl').read_text().splitlines()]
        first_test = next(x for x in calls if x[0] == 'test')
        self.assertIn('^TestAlpha$', first_test)
        self.assertNotIn('-covermode=atomic', first_test)
        self.assertEqual(len(self.full_calls()), 7)

    def test_empty_preflight_never_counts_as_success(self):
        result = self.run_cli('--preflight', fixtures.UI + '=^TestMissing$')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('matched no tests', result.stderr)
        self.assertFalse((self.root / 'coverage.out').exists())

    def test_real_go_full_checkpoint_and_resume(self):
        # Use the existing real Test/Example/Fuzz/subtest fixture through CLI.
        fixtures.CoverageCLI.test_real_go_examples_fuzz_subtests_and_dynamic_discovery(self)
        expected = (self.root / 'coverage.out').read_bytes()
        result = subprocess.run([sys.executable, str(self.runner), '--resume'], cwd=self.root,
                                capture_output=True, text=True, encoding='utf8', timeout=180)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stdout.count('RESUME: validated'), 7)
        self.assertEqual((self.root / 'coverage.out').read_bytes(), expected)


if __name__ == '__main__':
    unittest.main()
