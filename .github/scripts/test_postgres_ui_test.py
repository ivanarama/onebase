"""Public CLI acceptance: real Go seeds plus subprocess failure/cancel controls."""

from collections import Counter
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest


RUNNER = Path(__file__).with_name("test-postgres-ui.py").resolve()
PACKAGE = "github.com/ivantit66/onebase/internal/ui"
FAKE_GO = r'''#!/usr/bin/env python3
import json, os, pathlib, re, subprocess, sys, time
args=sys.argv[1:]
assert args[:5]==['test','-race','-count=1','-timeout=20m','-json'],args
assert args[-1]=='github.com/ivantit66/onebase/internal/ui'
assert os.environ['TEST_DATABASE_URL']=='postgres://fixture/same-job'
with open('calls.jsonl','a') as out: out.write(json.dumps(args)+'\n')
package=args[-1]; mode=os.environ.get('SCENARIO','')
def emit(action, **kw):
 print(json.dumps(dict(Action=action,Package=package,**kw)),flush=True)
names=['TestRoot'+str(i) for i in range(130)]+['TestРусский','ExampleValue','FuzzSeed','TestRefComboboxLargeCatalogPublicHTTP']
if os.environ.get('NEW_ROOT'):names+=['TestAddedAfterRunner']
if '-list' in args:
 if mode=='empty':names=[]
 if mode=='duplicate-inventory':names+=[names[0]]
 if mode=='discovery-failure':sys.exit(2)
 for n in names:emit('output',Output=n+'\n')
 emit('output',Output='ok\t'+package+'\t0.01s\n');emit('pass');sys.exit(0)
if mode=='cancel':
 code="import os,pathlib,signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); pathlib.Path('descendant.pid').write_text(str(os.getpid())); time.sleep(60)"
 subprocess.Popen([sys.executable,'-c',code])
 while not pathlib.Path('descendant.pid').exists():time.sleep(0.01)
 pathlib.Path('child.pid').write_text(str(os.getpid()))
 while True:time.sleep(1)
pattern=args[args.index('-run')+1]
selected=[n for n in names if re.fullmatch(pattern,n)]
assert 0<len(selected)<=128
if mode=='missing-root':selected=selected[1:]
if mode=='duplicate-root':selected+=[selected[0]]
if mode=='foreign-root':selected+=['TestUnexpected']
for n in selected:
 emit('run',Test=n)
 emit('run',Test=n+'/sub')
 if mode!='missing-subtest-terminal':emit('pass',Test=n+'/sub')
 if mode!='interrupted':emit('pass',Test=n)
if mode=='nonzero':sys.exit(7)
if mode=='fail-event':emit('fail',Test=selected[0])
if mode!='missing-package-terminal':emit('pass')
'''


class PostgresUIRunnerCLI(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        go = self.bin / "go"
        go.write_text(FAKE_GO, encoding="utf-8")
        go.chmod(0o755)
        self.env = dict(os.environ, TEST_DATABASE_URL="postgres://fixture/same-job")
        self.env["PATH"] = str(self.bin) + os.pathsep + self.env["PATH"]

    def tearDown(self):
        self.tmp.cleanup()

    def cli(self, scenario="", **env):
        return subprocess.run([sys.executable, str(RUNNER)], cwd=self.root,
                              env=dict(self.env, SCENARIO=scenario, **env),
                              capture_output=True, text=True, encoding="utf-8", timeout=120)

    def test_dynamic_complete_inventory_and_pg_environment(self):
        result = self.cli(NEW_ROOT="1")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        inventory = json.loads((self.root / "postgres-ui-results/inventory.json").read_text())
        names, groups = inventory["names"], inventory["groups"]
        self.assertEqual(list(map(len, groups)), [128, 7])
        self.assertIn("TestAddedAfterRunner", names)
        self.assertIn("TestРусский", names)
        self.assertIn("TestRefComboboxLargeCatalogPublicHTTP", names)
        self.assertEqual(Counter(n for g in groups for n in g), Counter(names))
        completion = json.loads((self.root / "postgres-ui-results/completion.json").read_text())
        self.assertEqual(completion["runs"], dict.fromkeys(names, 1))
        self.assertEqual(completion["terminal"], completion["runs"])
        calls = [json.loads(l) for l in (self.root / "calls.jsonl").read_text().splitlines()]
        self.assertEqual(len(calls), 3)
        self.assertIn("-list", calls[0])
        # A stale successful directory cannot be reused on a partial retry.
        self.assertNotEqual(self.cli("interrupted").returncode, 0)

    def test_missing_duplicate_foreign_or_incomplete_execution_fails(self):
        for scenario in ["empty", "duplicate-inventory", "discovery-failure", "missing-root",
                         "duplicate-root", "foreign-root", "interrupted", "nonzero",
                         "missing-subtest-terminal", "missing-package-terminal", "fail-event"]:
            with self.subTest(scenario=scenario):
                shutil.rmtree(self.root / "postgres-ui-results", ignore_errors=True)
                result = self.cli(scenario)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertFalse((self.root / "postgres-ui-results/completion.json").exists())

    def test_pg_environment_is_required_before_discovery(self):
        result = self.cli(TEST_DATABASE_URL="")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / "calls.jsonl").exists())

    def test_cancellation_stops_subprocess_and_never_commits_completion(self):
        child = subprocess.Popen([sys.executable, str(RUNNER)], cwd=self.root,
                                 env=dict(self.env, SCENARIO="cancel"),
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            deadline = time.monotonic() + 15
            pidfile = self.root / "child.pid"
            while not pidfile.exists() and child.poll() is None and time.monotonic() < deadline:
                time.sleep(0.02)
            self.assertTrue(pidfile.exists(), "execution subprocess did not start")
            pid = int(pidfile.read_text())
            child.send_signal(signal.SIGTERM)
            out, err = child.communicate(timeout=10)
            self.assertEqual(child.returncode, 128 + signal.SIGTERM, out + err)
            with self.assertRaises(ProcessLookupError):
                os.kill(pid, 0)
            descendant = int((self.root / "descendant.pid").read_text())
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                state = subprocess.run(["ps", "-p", str(descendant), "-o", "stat="],
                                       capture_output=True, text=True, check=False).stdout.strip()
                if not state or state.startswith("Z"):
                    break
                time.sleep(0.02)
            self.assertTrue(not state or state.startswith("Z"), "descendant survived cancellation")
            self.assertFalse((self.root / "postgres-ui-results/completion.json").exists())
        finally:
            pidfile = self.root / "descendant.pid"
            if pidfile.exists():
                try:
                    os.kill(int(pidfile.read_text()), signal.SIGKILL)
                except ProcessLookupError:
                    pass
            if child.poll() is None:
                child.kill()
            child.communicate(timeout=10)

    def test_real_go_examples_fuzz_seeds_russian_names_and_new_roots(self):
        (self.root / "go.mod").write_text("module github.com/ivantit66/onebase\n\ngo 1.26.0\n")
        package = self.root / "internal/ui"
        package.mkdir(parents=True)
        (package / "value.go").write_text("package ui\nfunc Value() int { return 42 }\n")
        (package / "value_test.go").write_text('''package ui
import ("fmt"; "os"; "testing")
func TestРусский(t *testing.T) {
 if os.Getenv("TEST_DATABASE_URL")!="postgres://fixture/same-job" { t.Fatal("missing PG environment") }
 t.Run("вложенный",func(t *testing.T){})
}
func TestRefComboboxLargeCatalogPublicHTTP(t *testing.T) {}
func ExampleValue() { fmt.Println(42); // Output: 42
}
func FuzzValue(f *testing.F) { f.Add(1); f.Add(2); f.Fuzz(func(t *testing.T, n int){}) }
''', encoding="utf-8")
        # Added dynamically after the original fixture, enough to cross the
        # group boundary without any runner allowlist changes.
        (package / "new_test.go").write_text("package ui\nimport \"testing\"\n" + "\n".join(
            f"func TestAdded{i}(t *testing.T) {{}}" for i in range(130)), encoding="utf-8")
        env = dict(os.environ, TEST_DATABASE_URL="postgres://fixture/same-job")
        result = subprocess.run([sys.executable, str(RUNNER)], cwd=self.root, env=env,
                                capture_output=True, text=True, encoding="utf-8", timeout=180)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        logs = [json.loads(line) for file in (self.root / "postgres-ui-results").glob("group-*.jsonl")
                for line in file.read_text(encoding="utf-8").splitlines()]
        ran = Counter(e["Test"] for e in logs if e["Action"] == "run")
        expected = [f"TestAdded{i}" for i in range(130)] + ["TestРусский", "TestРусский/вложенный",
                    "ExampleValue", "FuzzValue", "FuzzValue/seed#0", "FuzzValue/seed#1",
                    "TestRefComboboxLargeCatalogPublicHTTP"]
        self.assertEqual(ran, Counter(expected))
        self.assertEqual(sum(e["Action"] == "pass" and not e.get("Test") for e in logs), 2)


if __name__ == "__main__":
    unittest.main()
