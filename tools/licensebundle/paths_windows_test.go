package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setup-go exposes C:\hostedtoolcache through a junction to D:. Also test a
// nested alias and reject a junction that escapes a declared source root.
func TestSourceFileWindowsJunction(t *testing.T) {
	junction := func(link, target string) {
		t.Helper()
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", "$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path $env:LICENSE_TEST_LINK -Target $env:LICENSE_TEST_TARGET | Out-Null")
		cmd.Env = append(os.Environ(), "LICENSE_TEST_LINK="+link, "LICENSE_TEST_TARGET="+target)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create test junction: %v\n%s", err, output)
		}
	}
	rootOutput, err := goOutput(".", "env", "GOROOT")
	if err != nil {
		t.Fatal(err)
	}
	goRoot := strings.TrimSpace(string(rootOutput))
	want, err := os.ReadFile(filepath.Join(goRoot, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "go-junction")
	junction(alias, goRoot)
	for _, root := range []string{goRoot, alias} {
		got, err := sourceFile(root, "LICENSE")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("junction changed licensing bytes")
		}
	}
	confined := t.TempDir()
	junction(filepath.Join(confined, "outside"), goRoot)
	if _, err := sourceFile(confined, "outside/LICENSE"); err == nil {
		t.Fatal("source root allowed a junction escape")
	}
}
