"""Public CLI acceptance with real Go/golangci-lint and a package-hiding driver.

LINT_RUNNER_TEST_LINTER must identify 2.11.4 built with Go 1.26.11.
No downloaded tool or mocked analysis is substituted by this test.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

RUNNER = Path(__file__).with_name("lint-runner.py")
DRIVER = r'''
package main
import (
 "encoding/json"
 "os"
 "path/filepath"
 "runtime"
)
func main() {
 if err:=os.WriteFile(os.Getenv("DRIVER_CALLED"), []byte("called"),0600);err!=nil {panic(err)}
 root,err:=os.Getwd();if err!=nil {panic(err)}
 id:="example.com/lint-fixture/clean"
 response:=map[string]any{"Compiler":"gc","Arch":runtime.GOARCH,"Roots":[]string{id},
 "Packages":[]any{map[string]any{"ID":id,"Name":"clean","PkgPath":id,
 "GoFiles":[]string{filepath.Join(root,"clean","value.go")},
 "CompiledGoFiles":[]string{filepath.Join(root,"clean","value.go")},"Imports":map[string]string{}}}}
 if err:=json.NewEncoder(os.Stdout).Encode(response);err!=nil {panic(err)}
}
'''

GO_PROXY = r'''
package main
import (
 "encoding/json"
 "os"
 "os/exec"
)
func main() {
 log,err:=os.OpenFile(os.Getenv("GO_PROXY_CALLED"),os.O_CREATE|os.O_WRONLY|os.O_APPEND,0600)
 if err!=nil {panic(err)}
 if err=json.NewEncoder(log).Encode(os.Args[1:]);err!=nil {panic(err)}
 if err=log.Close();err!=nil {panic(err)}
 args:=os.Args[1:]
 if len(args)>0 && args[0]=="list" {
  for i,arg:=range args {if arg=="./..." {args[i]="./clean"}}
 }
 cmd:=exec.Command(os.Getenv("REAL_TEST_GO"),args...)
 cmd.Stdin=os.Stdin;cmd.Stdout=os.Stdout;cmd.Stderr=os.Stderr
 if err:=cmd.Run();err!=nil {
  if e,ok:=err.(*exec.ExitError);ok {os.Exit(e.ExitCode())}
  panic(err)
 }
}
'''


class ExternalDriverCLI(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.linter = Path(os.environ["LINT_RUNNER_TEST_LINTER"]).resolve(strict=True)
        cls.tools = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.tools.cleanup)
        tools = Path(cls.tools.name)
        source = tools / "driver.go"
        source.write_text(DRIVER, encoding="utf8")
        cls.driver = tools / ("gopackagesdriver.exe" if os.name == "nt" else "gopackagesdriver")
        subprocess.run(["go", "build", "-o", str(cls.driver), str(source)], check=True)
        source.write_text(GO_PROXY, encoding="utf8")
        proxy_dir = tools / "shadow"
        proxy_dir.mkdir()
        cls.go_proxy = proxy_dir / ("go.exe" if os.name == "nt" else "go")
        subprocess.run(["go", "build", "-o", str(cls.go_proxy), str(source)], check=True)

    def test_absolute_go_also_controls_the_linters_go_lookup(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root = base / "source"
            root.mkdir()
            (root / "go.mod").write_text("module example.com/lint-fixture\n\ngo 1.26.0\n")
            (root / ".go-version").write_text("1.26.11\n")
            shutil.copy2(RUNNER.parents[2] / ".golangci.yml", root / ".golangci.yml")
            for package, content in [("clean", "func Value() int { return 7 }"),
                                     ("bad", "func neverUsed() {}")]:
                directory = root / package
                directory.mkdir()
                (directory / "value.go").write_text(f"package {package}\n{content}\n")
            real_go = str(Path(shutil.which("go")).resolve(strict=True))
            marker = base / "proxy-called"
            env = dict(os.environ, GOTOOLCHAIN="go1.26.11", GOENV="off", GOWORK="off",
                       GOFLAGS="", CGO_ENABLED="0", GOPACKAGESDRIVER="off",
                       REAL_TEST_GO=real_go, GO_PROXY_CALLED=str(marker))
            original_path = env["PATH"]
            env["PATH"] = str(self.go_proxy.parent) + os.pathsep + original_path
            control = subprocess.run([str(self.linter), "run", "--timeout", "60s", "./..."],
                                     cwd=root, env=env, capture_output=True, text=True, timeout=90)
            self.assertEqual(control.returncode, 0, control.stdout + control.stderr)
            self.assertTrue(marker.exists(), "real linter did not use the shadow Go")
            marker.unlink()
            artifacts = base / "artifacts"
            args = [sys.executable, str(RUNNER), "--root", str(root), "--go", real_go,
                    "--linter", str(self.linter), "--target", "linux/amd64", "--artifacts", str(artifacts)]
            inspected = subprocess.run([*args, "inspect"], env=dict(env, PATH=original_path),
                                       capture_output=True, text=True, timeout=90)
            self.assertEqual(inspected.returncode, 0, inspected.stderr)
            expected = base / "inputs.json"
            expected.write_text(inspected.stdout, encoding="utf8")
            result = subprocess.run([*args, "run", "--expect", str(expected)], env=env,
                                    capture_output=True, text=True, timeout=180)
            self.assertNotEqual(result.returncode, 0, result.stdout)
            journal = json.loads((artifacts / "result.json").read_text())
            self.assertFalse(journal["success"])
            self.assertEqual(journal["stages"][-1]["stage"], "lint")
            self.assertIn("neverUsed", (artifacts / "lint.log").read_text())
            self.assertFalse(marker.exists(), "runner used an unbound Go from PATH")
            context = json.loads(inspected.stdout)["context"]["go"]
            self.assertEqual(context["analysis_path"], context["effective_path"])
            self.assertEqual(context["analysis_sha256"], context["effective_sha256"])

    def test_inherited_and_path_drivers_cannot_omit_a_package(self):
        for mode in ["variable", "path"]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                base = Path(temporary)
                root = base / "source"
                root.mkdir()
                (root / "go.mod").write_text("module example.com/lint-fixture\n\ngo 1.26.0\n")
                (root / ".go-version").write_text("1.26.11\n")
                shutil.copy2(RUNNER.parents[2] / ".golangci.yml", root / ".golangci.yml")
                for package, content in [("clean", "func Value() int { return 7 }"),
                                         ("bad", "func neverUsed() {}")]:
                    directory = root / package
                    directory.mkdir()
                    (directory / "value.go").write_text(f"package {package}\n{content}\n")
                marker = base / "driver-called"
                env = dict(os.environ, GOTOOLCHAIN="go1.26.11", GOENV="off", GOWORK="off",
                           GOFLAGS="", CGO_ENABLED="0", DRIVER_CALLED=str(marker))
                if mode == "variable":
                    env["GOPACKAGESDRIVER"] = str(self.driver)
                else:
                    env.pop("GOPACKAGESDRIVER", None)
                    env["PATH"] = str(self.driver.parent) + os.pathsep + env["PATH"]
                # Prove this executable actually hijacks the real linter and
                # hides the bad package, rather than merely reading an env var.
                control = subprocess.run([str(self.linter), "run", "--timeout", "60s", "./..."],
                                         cwd=root, env=env, capture_output=True, text=True, timeout=90)
                self.assertEqual(control.returncode, 0, control.stdout + control.stderr)
                self.assertTrue(marker.exists(), control.stderr)
                marker.unlink()
                artifacts = base / "artifacts"
                args = [sys.executable, str(RUNNER), "--root", str(root), "--linter", str(self.linter),
                        "--target", "linux/amd64", "--artifacts", str(artifacts)]
                inspected = subprocess.run([*args, "inspect"], env=env, capture_output=True, text=True, timeout=90)
                self.assertEqual(inspected.returncode, 0, inspected.stderr)
                expected = base / "inputs.json"
                expected.write_text(inspected.stdout, encoding="utf8")
                result = subprocess.run([*args, "run", "--expect", str(expected)], env=env,
                                        capture_output=True, text=True, timeout=180)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                journal = json.loads((artifacts / "result.json").read_text())
                self.assertFalse(journal["success"])
                self.assertEqual(journal["stages"][-1]["stage"], "lint")
                self.assertIn("neverUsed", (artifacts / "lint.log").read_text())
                self.assertFalse(marker.exists(), "runner executed the external driver")
                self.assertEqual(json.loads(inspected.stdout)["context"]["profile"]["GOPACKAGESDRIVER"], "off")

    def test_older_bootstrap_cannot_redispatch_through_a_versioned_path_proxy(self):
        bootstrap = Path(os.environ["LINT_RUNNER_TEST_BOOTSTRAP_GO"]).resolve(strict=True)
        local_env = dict(os.environ, GOTOOLCHAIN="local", GOENV="off", GOWORK="off")
        version = subprocess.check_output([str(bootstrap), "version"], env=local_env, text=True)
        self.assertTrue(version.startswith("go version go1.26."), version)
        self.assertLess(tuple(map(int, version.split()[2].removeprefix("go").split("."))),
                        (1, 26, 11), "bootstrap must actually predate the pinned Go")
        selected_env = dict(local_env, GOTOOLCHAIN="go1.26.11")
        goroot = subprocess.check_output([str(bootstrap), "env", "GOROOT"], env=selected_env, text=True).strip()
        effective = Path(goroot) / "bin" / ("go.exe" if os.name == "nt" else "go")
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root = base / "source"
            root.mkdir()
            (root / "go.mod").write_text("module example.com/lint-fixture\n\ngo 1.26.0\n")
            (root / ".go-version").write_text("1.26.11\n")
            shutil.copy2(RUNNER.parents[2] / ".golangci.yml", root / ".golangci.yml")
            for package, content in [("clean", "func Value() int { return 7 }"),
                                     ("bad", "func neverUsed() {}")]:
                directory = root / package
                directory.mkdir()
                (directory / "value.go").write_text(f"package {package}\n{content}\n")
            shadow = base / "shadow"
            shadow.mkdir()
            shutil.copy2(self.go_proxy, shadow / ("go1.26.11.exe" if os.name == "nt" else "go1.26.11"))
            marker = base / "versioned-proxy-called"
            env = dict(selected_env, GOPACKAGESDRIVER="off", GOFLAGS="", CGO_ENABLED="0",
                       REAL_TEST_GO=str(effective), GO_PROXY_CALLED=str(marker))
            args = [sys.executable, str(RUNNER), "--root", str(root), "--go", str(bootstrap),
                    "--linter", str(self.linter), "--target", "linux/amd64", "--artifacts", str(base / "artifacts")]
            inspected = subprocess.run([*args, "inspect"], env=env, capture_output=True, text=True, timeout=90)
            self.assertEqual(inspected.returncode, 0, inspected.stderr)
            expected = base / "inputs.json"
            expected.write_text(inspected.stdout, encoding="utf8")
            env["PATH"] = str(shadow) + os.pathsep + env["PATH"]
            # A genuine older Go dispatches through go1.26.11 in PATH, letting
            # the proxy hide bad from the real linter without changing inputs.
            control_env = dict(env, PATH=str(bootstrap.parent) + os.pathsep + env["PATH"])
            control = subprocess.run([str(self.linter), "run", "--timeout", "60s", "./..."],
                                     cwd=root, env=control_env, capture_output=True, text=True, timeout=90)
            self.assertEqual(control.returncode, 0, control.stdout + control.stderr)
            self.assertTrue(marker.exists(), "older bootstrap did not redispatch to the versioned proxy")
            marker.unlink()
            again = subprocess.run([*args, "inspect"], env=env, capture_output=True, text=True, timeout=90)
            self.assertEqual(again.returncode, 0, again.stderr)
            self.assertEqual(json.loads(inspected.stdout), json.loads(again.stdout))
            marker.unlink(missing_ok=True)
            result = subprocess.run([*args, "run", "--expect", str(expected)], env=env,
                                    capture_output=True, text=True, timeout=180)
            self.assertNotEqual(result.returncode, 0, result.stdout)
            artifacts = base / "artifacts"
            journal = json.loads((artifacts / "result.json").read_text())
            self.assertFalse(journal["success"])
            self.assertEqual(journal["stages"][-1]["stage"], "lint")
            self.assertIn("neverUsed", (artifacts / "lint.log").read_text())
            # Bootstrap selection may dispatch once to locate GOROOT. It must
            # never be used again for version/module checks, exports or lint.
            calls = [json.loads(line) for line in marker.read_text().splitlines()]
            self.assertEqual(calls, [["env", "-json", "GOROOT", "GOVERSION"]])
            go = journal["fingerprint"]["context"]["go"]
            self.assertEqual(Path(go["analysis_path"]), effective.resolve())
            self.assertEqual(go["analysis_sha256"], go["effective_sha256"])
            self.assertTrue(all(Path(stage["command"][0]) == effective.resolve()
                                for stage in journal["stages"][:2]))
            self.assertEqual(journal["fingerprint"]["context"]["profile"]["GOTOOLCHAIN"], "local")


if __name__ == "__main__":
    unittest.main()
