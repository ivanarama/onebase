package main

import (
	"bytes"
	"context"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/userdata"
	"gopkg.in/yaml.v3"
)

// Exercise argv and startup, not just the path helper: an early startup.log or
// launcher child writing into the regular profile would break this scenario.
func TestPortableBinaryMovesBundle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	workspace := t.TempDir()
	first, second := filepath.Join(workspace, "first bundle"), filepath.Join(workspace, "moved bundle")
	caller, profile := filepath.Join(workspace, "unrelated cwd"), filepath.Join(workspace, "regular profile")
	for _, dir := range []string{first, caller, profile, filepath.Join(first, "data")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	name := "onebase"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(first, name)
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".") //nolint:gosec // G204: fixed Go compiler invocation; output belongs to t.TempDir, no shell
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	env := append(os.Environ(), "HOME="+profile, "USERPROFILE="+profile,
		"APPDATA="+filepath.Join(profile, "Roaming"), "LOCALAPPDATA="+filepath.Join(profile, "Local"),
		userdata.PortableRootEnv+"=", "ONEBASE_WEBVIEW_PROFILE=", "ONEBASE_NO_GUI=1", "ONEBASE_LOG_LEVEL=warn")
	run := func(exe string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, exe, append([]string{"--portable"}, args...)...) //nolint:gosec // test-built executable; no shell
		cmd.Dir, cmd.Env = caller, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run(binary, "init", "project")
	run(binary, "migrate", "--project", "project", "--sqlite", "data/main.db")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().String()
	_, portNumber, _ := net.SplitHostPort(port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	run(binary, "ibases", "add", "--name", "Portable example", "--source", "file", "--path", "project", "--sqlite", "data/main.db", "--port", portNumber)
	registry, err := os.ReadFile(filepath.Join(first, ".onebase", "ibases.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Bases []struct {
			ID     string `yaml:"id"`
			Path   string `yaml:"path"`
			DBPath string `yaml:"db_path"`
		} `yaml:"bases"`
	}
	if err := yaml.Unmarshal(registry, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Bases) != 1 || document.Bases[0].Path != "project" || document.Bases[0].DBPath != "data/main.db" {
		t.Fatalf("registry did not keep bundle paths relative: %+v", document)
	}
	if err := filepath.WalkDir(first, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(first, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(second, rel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		data, err := os.ReadFile(path) //nolint:gosec // G122: walking this test's private, closed bundle; no concurrent or untrusted writers
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o700) //nolint:gosec // G703: dst is relative to this test's own destination; source paths come from its private WalkDir
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(first, first+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	binary = filepath.Join(second, name)
	list := run(binary, "ibases", "list")
	if !strings.Contains(list, filepath.Join(second, "data", "main.db")) || strings.Contains(list, first) {
		t.Fatalf("relocated registry: %s", list)
	}
	// Like a launcher-managed child, this process inherits the bundle root and
	// receives no --portable flag. It must resolve the moved registry and files.
	cmd := exec.CommandContext(ctx, binary, "--no-gui", "run", "--id", document.Bases[0].ID) //nolint:gosec // same test-built executable
	cmd.Dir = caller
	cmd.Env = append(env, userdata.PortableRootEnv+"="+second)
	log, err := os.Create(filepath.Join(workspace, "runtime.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := log.Close(); err != nil {
			t.Error(err)
		}
	}()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	client := &http.Client{Timeout: time.Second}
	ready := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get("http://" + port + "/healthz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(filepath.Join(workspace, "runtime.log"))
		t.Fatalf("moved runtime did not become ready:\n%s", data)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if _, err := os.Stat(filepath.Join(profile, ".onebase")); !os.IsNotExist(err) {
		t.Fatalf("portable commands touched regular profile: %v", err)
	}
	if err := os.Rename(filepath.Join(second, ".onebase"), filepath.Join(second, ".saved-data")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, ".onebase"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	failure := exec.CommandContext(ctx, binary, "--portable", "ibases", "list") //nolint:gosec // same test-built executable
	failure.Dir, failure.Env = caller, env
	if output, err := failure.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("portable")) {
		t.Fatalf("blocked portable directory did not fail clearly: %v %s", err, output)
	}
}
