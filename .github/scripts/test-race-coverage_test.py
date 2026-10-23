import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("race_coverage", Path(__file__).with_name("test-race-coverage.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class RaceCoverageTest(unittest.TestCase):
    def test_command_covers_multiple_inventories_and_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake = root / "go"
            fake.write_text("#!" + sys.executable + "\n" + '''
import json, os, re, sys
from pathlib import Path
a = sys.argv[1:]
root = Path(os.environ['ONEBASE_RUNNER_TEST_ROOT'])
with (root / 'calls.jsonl').open('a') as stream:
    stream.write(json.dumps(a) + '\\n')
packages = ['example/' + p for p in ['other', 'internal/auth', 'internal/launcher', 'internal/ui', 'internal/dsl/interpreter']]
names = ['TestCase' + str(i) for i in range(83)] + ['Example', 'FuzzSeed']
mode = os.environ.get('ONEBASE_RUNNER_TEST_MODE', '')
if a == ['list', './...']:
    print('\\n'.join(packages[:-1] if mode == 'missing-package' else packages))
elif '-c' in a:
    assert '-race' in a and '-covermode=atomic' in a
    if mode == 'discovery-error': sys.exit(1)
    binary = Path(a[a.index('-o') + 1])
    inventory = [] if mode == 'empty' else names
    binary.write_text('#!' + sys.executable + '\\nprint(' + repr('\\n'.join(inventory)) + ')\\n')
    binary.chmod(0o755)
else:
    assert all(flag in a for flag in ['-race', '-count=1', '-timeout=10m', '-covermode=atomic'])
    selected = []
    if '-run' in a:
        pattern = a[a.index('-run') + 1]
        selected = [name for name in names if re.fullmatch(pattern, name)]
        if mode == 'drop': selected = selected[:-1]
        if mode == 'duplicate': selected += selected[:1]
    if mode in ['error', 'timeout']:
        print(json.dumps({'Action':'output', 'Output':'panic: test timed out after 10m0s' if mode == 'timeout' else 'failure'}))
        sys.exit(1)
    for name in selected:
        print(json.dumps({'Action': 'run', 'Test': name, 'Package': a[-1]}))
        print(json.dumps({'Action': 'run', 'Test': name + '/nested', 'Package': a[-1]}))
        print(json.dumps({'Action': 'pass', 'Test': name + '/nested', 'Package': a[-1]}))
        if mode != 'incomplete':
            print(json.dumps({'Action': 'pass', 'Test': name, 'Package': a[-1], 'Elapsed': 0.01}))
    profile = Path(next(arg.split('=', 1)[1] for arg in a if arg.startswith('-coverprofile=')))
    location = a[-1] + '.go:1.1,2.2'
    profile.write_text('mode: atomic\\n' + location + ' 1 ' + str(len(selected)) + '\\n')
    if mode == 'bad-coverage': profile.write_text('mode: set\\n')
''', encoding="utf-8")
            fake.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"], ONEBASE_RUNNER_TEST_ROOT=str(root))
            out, report = root / "coverage.out", root / "report"
            script = Path(__file__).with_name("test-race-coverage.py")
            result = subprocess.run([sys.executable, str(script), str(out), str(report)], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            evidence = json.loads((report / "report.json").read_text())
            self.assertTrue(evidence["success"])
            self.assertEqual(len(evidence["inventories"]), 3)
            calls = [json.loads(line) for line in (root / "calls.jsonl").read_text().splitlines()]
            executions = [call for call in calls if '-json' in call]
            self.assertEqual(len(executions), 2 + 3 * ((85 + runner.BATCH_SIZE - 1) // runner.BATCH_SIZE))
            self.assertEqual(executions[0][-1], 'example/internal/dsl/interpreter')
            self.assertEqual(executions[1][-1], 'example/other')
            self.assertEqual(executions[1][executions[1].index('-p') + 1], str(runner.OTHER_CONCURRENCY))
            for package in evidence['inventories']:
                selected = [p for p in evidence['processes'] if p['kind'] == 'tests' and p['packages'] == [package]]
                self.assertEqual(sum(len(p['names']) for p in selected), 85)
                self.assertTrue(all(len(p['names']) <= runner.BATCH_SIZE for p in selected))
                self.assertIn(package + '.go:1.1,2.2 1 85\n', out.read_text())
            self.assertEqual(out.read_text().count('mode: atomic'), 1)
            # A previous successful result must not survive any failed phase.
            for mode in ['missing-package', 'discovery-error', 'empty', 'drop', 'duplicate',
                         'incomplete', 'error', 'timeout', 'bad-coverage']:
                with self.subTest(mode=mode):
                    out.write_text('old success')
                    env['ONEBASE_RUNNER_TEST_MODE'] = mode
                    result = subprocess.run([sys.executable, str(script), str(out), str(report)], env=env, capture_output=True, text=True)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertFalse(out.exists())
                    self.assertFalse(json.loads((report / 'report.json').read_text())['success'])
                    if mode in {'error', 'timeout'}:
                        self.assertEqual(json.loads((report / 'report.json').read_text())['processes'][-1]['exit'], 1)

    def test_inventory_includes_examples_and_fuzz_and_rejects_wrong_execution(self):
        names = runner.discover_names("TestA\nExample\nFuzzSeed\nok module 0.1s\n")
        runner.verify_inventory(names, list(reversed(names)))
        for actual in [names[:-1], names + [names[0]], names + ["TestUnexpected"]]:
            with self.assertRaises(ValueError):
                runner.verify_inventory(names, actual)
        for output in ["ok module 0.1s\n", "TestA\nTestA\n", "TestInvalid[\n"]:
            with self.assertRaises(ValueError):
                runner.discover_names(output)

    def test_merge_sums_atomic_counters_and_rejects_corruption(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profiles = [root / "other.out", root / "ui1.out", root / "ui2.out"]
            profiles[0].write_text("mode: atomic\nmodule/other.go:1.1,2.2 2 0\n", encoding="utf-8")
            profiles[1].write_text("mode: atomic\nmodule/ui.go:1.1,2.2 1 3\nmodule/ui.go:3.1,4.2 2 0\n", encoding="utf-8")
            profiles[2].write_text("mode: atomic\nmodule/ui.go:1.1,2.2 1 4\nmodule/ui.go:3.1,4.2 2 5\n", encoding="utf-8")
            out = root / "coverage.out"
            runner.merge_profiles(profiles, out)
            self.assertEqual(out.read_text(), "mode: atomic\nmodule/other.go:1.1,2.2 2 0\nmodule/ui.go:1.1,2.2 1 7\nmodule/ui.go:3.1,4.2 2 5\n")
            for invalid in ["mode: set\n", "mode: atomic\n", "mode: atomic\nmodule/other.go:1.1,2.2 3 1\n", "mode: atomic\nmodule/other.go:1.1,2.2 2 -1\n", "mode: atomic\nbad 1 1\n"]:
                profiles[1].write_text(invalid)
                selected = profiles[:2] if "other.go" in invalid else [profiles[1]]
                before = out.read_bytes()
                with self.assertRaises(ValueError):
                    runner.merge_profiles(selected, out)
                self.assertEqual(out.read_bytes(), before)

    def test_real_go_module_auto_includes_new_tests_examples_and_fuzz_seeds(self):
        # Exercise discovery, real test JSON and coverage end to end. All
        # package roles exist so this also checks the full partition in main.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'go.mod').write_text('module example\n\ngo 1.26.0\n')
            for package in ['other', *runner.BATCHED_PACKAGES, runner.ISOLATED_PACKAGE]:
                folder = root / package
                folder.mkdir(parents=True)
                (folder / 'value.go').write_text('package fixture\nfunc Value() int { return 1 }\n')
                (folder / 'value_test.go').write_text('''package fixture
import ("fmt"; "testing")
func TestOriginal(t *testing.T) { t.Run("nested", func(t *testing.T) { if Value()!=1 {t.Fatal("value")} }) }
func ExampleValue() { fmt.Println(Value()); /* Output: 1 */ }
func FuzzValue(f *testing.F) { f.Add(1); f.Fuzz(func(t *testing.T, v int) { _ = Value() }) }
''')
            # This was absent when the runner was written; no name allowlist.
            (root / 'internal/auth/new_test.go').write_text('package fixture\nimport "testing"\nfunc TestNewAutomaticallyIncluded(t *testing.T) { _ = Value() }\n')
            script = Path(__file__).with_name('test-race-coverage.py')
            result = subprocess.run([sys.executable, str(script), str(root / 'coverage.out'), str(root / 'report')], cwd=root, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            evidence = json.loads((root / 'report/report.json').read_text())
            self.assertTrue(evidence['success'])
            auth = evidence['inventories']['example/internal/auth']
            self.assertEqual(set(auth), {'TestOriginal', 'TestNewAutomaticallyIncluded', 'ExampleValue', 'FuzzValue'})
            executed = [test for process in evidence['processes'] for test in process.get('tests', []) if test['package'] == 'example/internal/auth']
            self.assertEqual({test['name'] for test in executed}, set(auth))
            self.assertTrue(all(test['action'] == 'pass' for test in executed))
            cover = subprocess.run(['go', 'tool', 'cover', '-func=' + str(root / 'coverage.out')], cwd=root, capture_output=True, text=True)
            self.assertEqual(cover.returncode, 0, cover.stderr)
            self.assertIn('100.0%', cover.stdout)


if __name__ == "__main__":
    unittest.main()
