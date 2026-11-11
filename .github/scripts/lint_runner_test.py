"""Public CLI acceptance for input pinning, failed analysis and tree cleanup."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

RUNNER = Path(__file__).with_name("lint-runner.py")
HELPER = r'''
package main
import (
 "encoding/json"
 "fmt"
 "os"
 "os/exec"
 "path/filepath"
 "strings"
 "time"
)
func main() {
 exe, _ := os.Executable()
 if len(os.Args)>1 && os.Args[1]=="orphan" {
  for start:=time.Now();time.Since(start)<15*time.Second; {
   _=os.WriteFile(filepath.Join(os.Getenv("TEMP"),"heartbeat"),[]byte(time.Now().String()),0600)
   time.Sleep(50*time.Millisecond)
  }
  return
 }
 control,_:=os.ReadFile("control")
 if strings.Contains(filepath.Base(exe),"golangci") {
  if os.Args[1]=="version" { fmt.Println("golangci-lint has version 2.11.4 built with go1.26.11");return }
  if string(control)=="lint-fail" { fmt.Println("actual lint finding");os.Exit(3) }
  fmt.Println("full lint completed")
  return
 }
 switch os.Args[1] {
 case "version":fmt.Println("go version go1.26.11 windows/amd64")
 case "env":
  value:=map[string]string{"GOROOT":filepath.Dir(filepath.Dir(exe)),"GOVERSION":"go1.26.11","GOHOSTOS":"fixture","GOHOSTARCH":"amd64"}
  _=json.NewEncoder(os.Stdout).Encode(value)
 case "mod":
  if os.Args[2]=="edit" {
   if string(control)=="internal-replace" {fmt.Println(`{"Replace":[{"New":{"Path":"./local"}}]}`)} else if string(control)=="local-replace" { fmt.Println(`{"Replace":[{"New":{"Path":"../outside"}}]}`) } else {fmt.Println(`{"Replace":null}`)}
  } else {fmt.Println("all modules verified")}
 case "list":
  if string(control)=="mutate" { _=os.WriteFile("changed-during-export",[]byte("changed"),0600) }
  if string(control)=="hang" {
   child:=exec.Command(exe,"orphan")
   if err:=child.Start();err!=nil {panic(err)}
   time.Sleep(15*time.Second)
  }
  fmt.Println("test-inclusive exports completed")
 }
}
'''


class LintRunnerCLI(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tools = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.tools.cleanup)
        root = Path(cls.tools.name)
        source = root / "helper.go"
        source.write_text(HELPER, encoding="utf-8")
        cls.template = root / ("fixture.exe" if os.name == "nt" else "fixture")
        subprocess.run(["go", "build", "-o", str(cls.template), str(source)], check=True)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.root = self.base / "source"
        self.root.mkdir()
        (self.root / ".go-version").write_text("1.26.11\n")
        (self.root / ".golangci.yml").write_text('version: "2"\n')
        (self.root / "go.mod").write_text("module fixture\n")
        (self.root / "control").write_text("normal")
        tools = self.base / "bin"
        tools.mkdir()
        suffix = ".exe" if os.name == "nt" else ""
        self.go = tools / ("go" + suffix)
        self.linter = tools / ("golangci-lint" + suffix)
        shutil.copy2(self.template, self.go)
        shutil.copy2(self.template, self.linter)
        self.artifacts = self.base / "artifacts"
        self.expect = self.base / "inputs.json"

    def cli(self, action, *extra):
        cmd = [sys.executable, str(RUNNER), action, "--root", str(self.root), "--go", str(self.go),
               "--linter", str(self.linter), "--target", "linux/amd64", "--artifacts", str(self.artifacts), *extra]
        return subprocess.run(cmd, capture_output=True, text=True, timeout=45)

    def inspect(self, *extra):
        result = self.cli("inspect", *extra)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.expect.write_text(result.stdout, encoding="utf-8")
        return json.loads(result.stdout)

    def run_lint(self, *extra):
        return self.cli("run", "--expect", str(self.expect), *extra)

    def test_full_run_requires_exports_and_success(self):
        info = self.inspect()
        self.assertEqual(info["context"]["profile"]["GOFLAGS"], "")
        self.assertEqual(info["context"]["profile"]["GOWORK"], "off")
        result = self.run_lint()
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        journal = json.loads((self.artifacts / "result.json").read_text())
        self.assertTrue(journal["success"])
        self.assertEqual([x["stage"] for x in journal["stages"]], ["modules", "exports", "lint"])
        self.assertTrue(all(x["exit"] == 0 and len(x["log_sha256"]) == 64 for x in journal["stages"]))
        self.assertNotEqual(self.run_lint().returncode, 0, "must not overwrite/reuse old result")

    @unittest.skipIf(os.name == "nt", "POSIX directory permissions; exercised by Linux CI")
    def test_unreadable_directory_rejects_inspect_and_run(self):
        self.inspect()
        hidden = self.root / ".unreadable"
        hidden.mkdir()
        (hidden / "input.txt").write_text("untracked input")
        hidden.chmod(0)
        self.addCleanup(hidden.chmod, 0o700)
        # A privileged test process cannot reproduce an access denial.
        with self.assertRaises(PermissionError):
            list(hidden.iterdir())
        for action in ["inspect", "run"]:
            with self.subTest(action=action):
                result = self.cli("inspect") if action == "inspect" else self.run_lint()
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn("Permission denied", result.stderr)
                self.assertFalse(self.artifacts.exists(), "analysis started with unreadable inputs")

    def test_changed_source_config_tool_and_profile_fail_before_execution(self):
        for change in ["source", "config", "tool", "profile"]:
            with self.subTest(change=change):
                self.inspect()
                extra = []
                changed = None
                if change == "source": changed = self.root / "untracked.go"
                if change == "config": changed = self.root / ".golangci.yml"
                if change == "tool": changed = self.linter
                before = changed.read_bytes() if changed and changed.exists() else None
                if changed:
                    with changed.open("ab") as stream: stream.write(b"\nchanged\n")
                if change == "profile": extra = ["--target", "windows/amd64"]
                result = self.run_lint(*extra)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertFalse(self.artifacts.exists(), result.stderr)
                if changed:
                    if before is None: changed.unlink()
                    else: changed.write_bytes(before)

    def test_lint_failure_is_not_export_success(self):
        (self.root / "control").write_text("lint-fail")
        self.inspect()
        self.assertNotEqual(self.run_lint().returncode, 0)
        journal = json.loads((self.artifacts / "result.json").read_text())
        self.assertFalse(journal["success"])
        self.assertEqual(journal["stages"][-1]["exit"], 3)

    def test_source_mutation_during_export_stops_before_lint(self):
        (self.root / "control").write_text("mutate")
        self.inspect()
        self.assertNotEqual(self.run_lint().returncode, 0)
        journal = json.loads((self.artifacts / "result.json").read_text())
        self.assertFalse(journal["success"])
        self.assertEqual([x["stage"] for x in journal["stages"]], ["modules", "exports"])
        self.assertIn("inputs changed", journal["error"])

    def test_timeout_kills_export_descendants(self):
        (self.root / "control").write_text("hang")
        self.inspect("--stage-timeout", "1")
        result = self.run_lint("--stage-timeout", "1")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        journal = json.loads((self.artifacts / "result.json").read_text())
        self.assertFalse(journal["success"])
        self.assertEqual(journal["stages"][-1]["exit"], 124)
        heartbeat = self.artifacts / "tmp" / "heartbeat"
        self.assertTrue(heartbeat.exists(), result.stderr)
        before = heartbeat.read_bytes()
        time.sleep(0.3)
        self.assertEqual(heartbeat.read_bytes(), before, "export descendant survived timeout")

    def test_internal_local_replace_is_bound_to_the_full_tree(self):
        (self.root / "control").write_text("internal-replace")
        local = self.root / "local"
        local.mkdir()
        (local / "go.mod").write_text("module local\n")
        data = local / "source.go"
        data.write_text("package local\n")
        self.inspect()
        data.write_text("package changed\n")
        self.assertNotEqual(self.run_lint().returncode, 0)
        self.assertFalse(self.artifacts.exists())
        self.inspect()
        self.assertEqual(self.run_lint().returncode, 0)

    def test_unbound_local_replace_and_in_tree_artifacts_rejected(self):
        # The outside module must really exist: a missing directory would fail
        # even if the containment guard were accidentally removed.
        outside = self.base / "outside"
        outside.mkdir()
        (outside / "go.mod").write_text("module outside\n")
        (outside / "source.go").write_text("package outside\n")
        (self.root / "control").write_text("local-replace")
        self.assertNotEqual(self.cli("inspect").returncode, 0)
        (self.root / "control").write_text("normal")
        result = self.cli("inspect", "--artifacts", str(self.root / "outputs"))
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
