"""CLI acceptance tests; synthetic failures complement a real Go fixture run."""

from collections import Counter
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


RUNNER = Path(__file__).with_name("test-coverage.py")
UI = "github.com/ivantit66/onebase/internal/ui"
LAUNCHER = "github.com/ivantit66/onebase/internal/launcher"

# This is a child process replacing only Go at the external command boundary.
# The real Go fixture below checks the actual -run/subtest/seed semantics.
FAKE_GO = r'''#!/usr/bin/env python3
import json, os, pathlib, re, sys
args = sys.argv[1:]
mode = os.environ.get('SCENARIO', '')
print('go: downloading fixture dependency', file=sys.stderr)
ui = 'github.com/ivantit66/onebase/internal/ui'
launcher = 'github.com/ivantit66/onebase/internal/launcher'
other = 'github.com/ivantit66/onebase/other'
with open('calls.jsonl', 'a') as f: f.write(json.dumps(args)+'\n')
if args == ['list', './...']:
    if mode == 'list-failure': sys.exit(2)
    print(other)
    print(ui if mode != 'missing-ui-package' else 'wrong/ui')
    print(launcher if mode != 'missing-launcher-package' else 'wrong/launcher')
    if mode == 'duplicate-package': print(ui)
    sys.exit(0)
assert args[0] == 'test'
assert all(flag in args for flag in ['-race', '-count=1', '-timeout=10m', '-json'])
def emit(action, package, **kw):
    print(json.dumps(dict(Action=action, Package=package, **kw)))
names = ['TestAlpha', 'TestBravo', 'TestCharlie', 'TestDelta', 'ExampleValue', 'FuzzSeed']
if os.environ.get('NEW_TEST'): names += ['TestNewAutomaticallyDiscovered']
if '-list' in args:
    package = args[-1]
    assert package in [ui, launcher] and args[args.index('-list')+1] == '^(Test|Example|Fuzz)'
    if mode == 'empty-launcher-inventory' and package == launcher: names=[]
    if mode == 'compile-failure': sys.exit(1)
    if mode == 'empty-inventory': names=[]
    if mode == 'duplicate-inventory': names += [names[0]]
    if mode == 'invalid-inventory': names += ['not a test']
    for n in names: emit('output', package, Output=n+'\n')
    emit('output', package, Output='ok\t'+package+'\t0.01s\n')
    emit('pass', package, Elapsed=0.01)
    sys.exit(0)
assert '-covermode=atomic' in args
coverage = pathlib.Path(next(a.split('=',1)[1] for a in args if a.startswith('-coverprofile=')))
package = args[-1]
assert ("-p=2" in args) == (package == other)
if package in [ui, launcher]:
    pattern=args[args.index('-run')+1]
    assert pattern.startswith('^(?:') and pattern.endswith(')$') and '/' not in pattern
    selected = [n for n in names if re.fullmatch(pattern, n)]
    if mode == 'missing-execution': selected = selected[1:]
    if mode == 'duplicate-execution': selected += [selected[0]]
    for n in selected:
        emit('run', package, Test=n)
        for sub in ['one', 'two']:
            emit('run', package, Test=n+'/'+sub)
            if mode != 'missing-subtest-result': emit('pass', package, Test=n+'/'+sub, Elapsed=0.001)
        if mode != 'missing-test-result': emit('pass', package, Test=n, Elapsed=0.002)
if mode in ['failed-test', 'race', 'timeout']:
    emit('output', package, Output={'failed-test':'FAIL test', 'race':'WARNING: DATA RACE',
                                  'timeout':'panic: test timed out after 10m0s'}[mode])
    emit('fail', package)
    sys.exit(1)
if mode != 'missing-package-result': emit('pass', package, Elapsed=0.01)
if mode == 'event-failure': emit('fail', package)
if mode == 'missing-profile': sys.exit(0)
if mode == 'corrupt-profile': coverage.write_text('garbage\n'); sys.exit(0)
count = int(coverage.stem[-1]) if package != other else 7
statements = 2
file = package+'/sample.go'
if package == ui and coverage.stem == 'ui-4':
    if mode == 'incompatible-profile': statements=3
    if mode == 'missing-block': file=ui+'/different.go'
if package == launcher and coverage.stem == 'launcher-2':
    if mode == 'incompatible-launcher-profile': statements=3
    if mode == 'missing-launcher-block': file=launcher+'/different.go'
record = file+':1.1,2.2 '+str(statements)+' '+str(count)+'\n'
content = 'mode: atomic\n'+record
if mode == 'wrong-mode': content = content.replace('atomic', 'set')
if mode == 'duplicate-block': content += record
if mode == 'negative-counter': content = content.replace(' 7\n',' -1\n')
if mode == 'empty-profile': content='mode: atomic\n'
coverage.write_text(content)
'''


class CoverageCLI(unittest.TestCase):
    runner = RUNNER

    def cli_command(self, runner):
        return [sys.executable, str(runner)]

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        go = self.bin / "go"
        go.write_text(FAKE_GO, encoding="utf-8")
        go.chmod(0o755)

    def tearDown(self):
        self.tmp.cleanup()

    def run_cli(self, scenario="", runner=None, new_test=False):
        env = dict(os.environ, SCENARIO=scenario, NEW_TEST="1" if new_test else "")
        env["PATH"] = str(self.bin) + os.pathsep + env["PATH"]
        result = subprocess.run(self.cli_command(runner or self.runner), cwd=self.root, env=env,
                                capture_output=True, text=True, encoding="utf-8", timeout=60)
        return result

    def test_full_inventory_and_exact_atomic_sum(self):
        result = self.run_cli(new_test=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        inventory = json.loads((self.root / "test-results/inventory.json").read_text())
        for label, count in [("ui", 4), ("launcher", 2)]:
            item = inventory["series"][label]
            names = item["names"]
            self.assertIn("ExampleValue", names)
            self.assertIn("FuzzSeed", names)
            self.assertIn("TestNewAutomaticallyDiscovered", names)
            self.assertEqual(item["groups"], [sorted(names)[i::count] for i in range(count)])
            self.assertEqual(Counter(n for g in item["groups"] for n in g), Counter(names))
            self.assertEqual(len(list((self.root / "test-results").glob(label+"-?.jsonl"))), count)
        lines = (self.root / "coverage.out").read_text().splitlines()
        self.assertEqual(lines, ["mode: atomic",
                                LAUNCHER+"/sample.go:1.1,2.2 2 3",
                                UI+"/sample.go:1.1,2.2 2 10",
                                "github.com/ivantit66/onebase/other/sample.go:1.1,2.2 2 7"])
        calls = [json.loads(s) for s in (self.root / "calls.jsonl").read_text().splitlines()]
        self.assertEqual(len(calls), 10)  # list, two inventories, other, UI4, launcher2
        self.assertNotIn(UI, calls[3])
        self.assertNotIn(LAUNCHER, calls[3])
        self.assertIn("-p=2", calls[3])
        self.assertNotIn("-run", calls[3])
        self.assertEqual(calls[3][-1:], ["github.com/ivantit66/onebase/other"])
        self.assertTrue(all("-p=2" not in call for call in calls[4:]))
        self.assertEqual([c[-1] for c in calls[4:]], [UI]*4+[LAUNCHER]*2)

    def test_failures_never_publish_partial_or_stale_coverage(self):
        for mode in ["list-failure", "missing-ui-package", "missing-launcher-package",
                     "duplicate-package", "compile-failure", "empty-launcher-inventory",
                     "empty-inventory", "duplicate-inventory", "invalid-inventory",
                     "missing-execution", "duplicate-execution", "missing-test-result", "missing-subtest-result", "failed-test", "race", "timeout",
                     "missing-package-result", "event-failure", "missing-profile", "corrupt-profile",
                     "incompatible-profile", "missing-block", "incompatible-launcher-profile",
                     "missing-launcher-block", "wrong-mode", "duplicate-block",
                     "negative-counter", "empty-profile"]:
            with self.subTest(mode=mode):
                shutil.rmtree(self.root / "test-results", ignore_errors=True)
                (self.root / "coverage.out").write_text("stale success\n")
                result = self.run_cli(mode)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertFalse((self.root / "coverage.out").exists())

    def test_partition_omission_and_duplicate_fail_before_tests(self):
        for count in [4, 2]:
            for expression in ["[names[:-1][i::count] for i in range(count)]",
                               "[names for i in range(count)]"]:
                with self.subTest(count=count, expression=expression):
                    shutil.rmtree(self.root / "test-results", ignore_errors=True)
                    source = RUNNER.read_text(encoding="utf-8")
                    source = source.replace("groups = [names[i::count] for i in range(count)]",
                                            f"groups = ({expression} if count == {count} else "
                                            "[names[i::count] for i in range(count)])")
                    modified = self.root / "damaged-runner.py"
                    modified.write_text(source, encoding="utf-8")
                    result = self.run_cli(runner=modified)
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertIn("partition loses or repeats", result.stderr)
                    self.assertFalse((self.root / "coverage.out").exists())

    def test_real_go_examples_fuzz_subtests_and_dynamic_discovery(self):
        (self.root / "go.mod").write_text("module github.com/ivantit66/onebase\n\ngo 1.26.0\n")
        ui = self.root / "internal/ui"
        ui.mkdir(parents=True)
        (ui / "value.go").write_text("package ui\nfunc Value() int { return 42 }\n")
        (ui / "value_test.go").write_text('''package ui
import ("fmt"; "testing")
func TestAlpha(t *testing.T) { for _, n := range []string{"a", "b"} {
 t.Run(n, func(t *testing.T) { if Value()!=42 { t.Fatal("value") } }) } }
func TestBravo(t *testing.T) { if Value()!=42 { t.Fatal("value") } }
func TestCharlie(t *testing.T) { if Value()!=42 { t.Fatal("value") } }
func ExampleValue() { fmt.Println(Value()); // Output: 42
}
func FuzzValue(f *testing.F) { f.Add(1); f.Add(2); f.Fuzz(func(t *testing.T, x int) {
 if Value()!=42 { t.Fatal("value") } }) }
''')
        # Added after the original fixture: no runner/allowlist update.
        (ui / "new_test.go").write_text('''package ui
import "testing"
func TestNew(t *testing.T) { if Value()!=42 { t.Fatal("new") } }
''')
        other = self.root / "other"
        other.mkdir()
        (other / "value.go").write_text("package other\nfunc Value() int { return 7 }\n")
        (other / "value_test.go").write_text('''package other
import "testing"
func TestOther(t *testing.T) { if Value()!=7 { t.Fatal("other") } }
''')
        launcher = self.root / "internal/launcher"
        launcher.mkdir(parents=True)
        (launcher / "launch.go").write_text("package launcher\nfunc Value() int { return 42 }\n")
        for name in ["value_test.go", "new_test.go"]:
            text = (ui / name).read_text().replace("package ui", "package launcher", 1)
            (launcher / name).write_text(text)
        result = subprocess.run(self.cli_command(self.runner), cwd=self.root,
                                capture_output=True, text=True, encoding="utf-8", timeout=180)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        expected = Counter(["TestAlpha", "TestAlpha/a", "TestAlpha/b", "TestBravo",
                            "TestCharlie", "ExampleValue", "FuzzValue",
                            "FuzzValue/seed#0", "FuzzValue/seed#1", "TestNew"])
        for label in ["ui", "launcher"]:
            journals = [json.loads(line)
                        for p in (self.root / "test-results").glob(label+"-?.jsonl")
                        for line in p.read_text().splitlines()]
            ran = Counter(e["Test"] for e in journals if e["Action"] == "run")
            self.assertEqual(ran, expected)
        lines = (self.root / "coverage.out").read_text().splitlines()
        self.assertTrue(any("/launcher/launch.go:" in line for line in lines))
        self.assertTrue(any("/ui/value.go:" in line for line in lines))
        self.assertTrue(any("/other/value.go:" in line for line in lines))
        cover = subprocess.run(["go", "tool", "cover", "-func=coverage.out"], cwd=self.root,
                               capture_output=True, text=True, timeout=60)
        self.assertEqual(cover.returncode, 0, cover.stderr)
        self.assertIn("100.0%", cover.stdout)


if __name__ == "__main__":
    unittest.main()
