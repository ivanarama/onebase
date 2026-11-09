package bootstraptest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ivantit66/onebase/internal/installtest"
)

// Exercise the actual shipped scripts and real native processes with release
// assets, rather than testing helpers or matching script text.
func TestBootstrap(t *testing.T) {
	shell := "sh"
	if runtime.GOOS == "windows" {
		shell = "powershell.exe"
	}
	if _, err := exec.LookPath(shell); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, tool := range []string{"curl", "jq", "tar"} {
			if _, err := exec.LookPath(tool); err != nil {
				t.Fatal(err)
			}
		}
	}
	fixturePath := filepath.Join(t.TempDir(), "release-fixture")
	if runtime.GOOS == "windows" {
		fixturePath += ".exe"
	}
	build := exec.Command("go", "build", "-o", fixturePath, "testdata/fixture.go") //nolint:gosec // G204: fixed Go source and output in t.TempDir, no external command text.
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	binaryName := "onebase"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	ambientDir := t.TempDir()
	write(t, filepath.Join(ambientDir, binaryName), fixture, 0700)
	t.Setenv("PATH", ambientDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	projectName := "проект & literal; $(touch injected)"
	if runtime.GOOS == "windows" {
		projectName = "проект & literal; (no shell)"
	}
	for _, scenario := range []string{
		"new", "new-project", "existing", "existing-path", "existing-old", "new-old",
		"bad-checksum", "bad-archive", "wrong-sum-name", "missing-asset", "duplicate-asset",
		"missing-binary", "duplicate-binary", "version-fail", "command-fail", "concurrent-winner",
		"dry-run", "dry-run-existing", "invalid-agents", "dry-run-invalid-agents",
		"new-auto", "linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-arm64",
	} {
		t.Run(scenario, func(t *testing.T) {
			if strings.HasPrefix(scenario, "windows-") && runtime.GOOS != "windows" ||
				(strings.HasPrefix(scenario, "linux-") || strings.HasPrefix(scenario, "darwin-")) && runtime.GOOS == "windows" {
				t.Skip("asset mapping for another script")
			}
			platform := runtime.GOOS
			arch := runtime.GOARCH
			if parts := strings.Split(scenario, "-"); len(parts) == 2 && (parts[0] == "windows" || parts[0] == "linux" || parts[0] == "darwin") {
				platform, arch = parts[0], parts[1]
			}
			testBundle := "onebase-" + platform + "-" + arch
			testAsset := testBundle + ".tar.gz"
			if runtime.GOOS == "windows" {
				testAsset = testBundle + ".zip"
			}
			root := installtest.CanonicalTempDir(t)
			installDir := filepath.Join(root, "install with spaces")
			project := filepath.Join(root, projectName)
			sentinel := filepath.Join(project, ".mcp.json")
			mkdir(t, project)
			original := []byte("{\"existing\":\"keep побайтово\"}\n")
			write(t, sentinel, original, 0600)
			argsFile := filepath.Join(root, "args.json")
			staging := filepath.Join(root, "staging")
			mkdir(t, staging)
			existing := strings.HasPrefix(scenario, "existing") || scenario == "dry-run-existing"
			existingDir := installDir
			if scenario == "existing-path" {
				existingDir = filepath.Join(root, "path-bin")
			}
			if existing {
				mkdir(t, existingDir)
				write(t, filepath.Join(existingDir, binaryName), fixture, 0700)
			}
			var requests atomic.Int64
			archive := makeArchive(t, fixture, testBundle, scenario)
			if scenario == "bad-archive" {
				archive = []byte("not an archive")
			}
			sum := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), testAsset)
			if scenario == "bad-checksum" {
				sum = strings.Repeat("0", 64) + "  " + testAsset + "\n"
			}
			if scenario == "wrong-sum-name" {
				sum = fmt.Sprintf("%x  other.tar.gz\n", sha256.Sum256(archive))
			}
			server := httptest.NewUnstartedServer(nil)
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch r.URL.Path {
				case "/latest":
					assets := []map[string]string{
						{"name": testAsset, "browser_download_url": server.URL + "/archive"},
						{"name": testAsset + ".sha256", "browser_download_url": server.URL + "/sum"},
						{"name": testAsset + ".sig", "browser_download_url": server.URL + "/unexpected"},
					}
					if scenario == "missing-asset" {
						assets = assets[1:]
					}
					if scenario == "duplicate-asset" {
						assets = append(assets, assets[0])
					}
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(map[string]any{"tag_name": "fixture", "assets": assets}); err != nil {
						t.Error(err)
					}
				case "/archive":
					if _, err := w.Write(archive); err != nil {
						t.Error(err)
					}
				case "/sum":
					if _, err := w.Write([]byte(sum)); err != nil {
						t.Error(err)
					}
				default:
					t.Errorf("unexpected download: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			server.Start()
			defer server.Close()
			withProject := scenario != "new"
			agents := "claude,cursor,opencode,zcode"
			if strings.Contains(scenario, "invalid-agents") {
				agents = "unknown"
			}
			var argv []string
			if runtime.GOOS == "windows" {
				argv = []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", "../../install.ps1", "-InstallDir", installDir, "-ReleaseApi", server.URL + "/latest"}
				if withProject {
					argv = append(argv, "-Project", project)
				}
				if withProject && scenario != "new-auto" {
					argv = append(argv, "-Agents", agents)
				}
				if strings.HasPrefix(scenario, "dry-run") {
					argv = append(argv, "-DryRun")
				}
			} else {
				argv = []string{"../../install.sh", "--install-dir", installDir, "--release-api", server.URL + "/latest"}
				if withProject {
					argv = append(argv, "--project", project)
				}
				if withProject && scenario != "new-auto" {
					argv = append(argv, "--agents", agents)
				}
				if strings.HasPrefix(scenario, "dry-run") {
					argv = append(argv, "--dry-run")
				}
			}
			toolDir := filepath.Join(root, "bootstrap-tools")
			mkdir(t, toolDir)
			if runtime.GOOS != "windows" {
				for _, name := range []string{"curl", "jq", "tar", "gzip", "mktemp", "uname", "awk", "chmod", "cp", "ln", "mkdir", "rm", "tr", "sha256sum", "shasum"} {
					target, err := exec.LookPath(name)
					if err != nil && (name == "sha256sum" || name == "shasum") {
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
					target, err = filepath.Abs(target)
					if err != nil {
						t.Fatal(err)
					}
					// macOS shasum dispatches by its original script path; wrappers
					// preserve it while keeping the child's PATH controlled.
					quoted := "'" + strings.ReplaceAll(target, "'", "'\\''") + "'"
					write(t, filepath.Join(toolDir, name), []byte("#!/bin/sh\nexec "+quoted+" \"$@\"\n"), 0700)
				}
			}
			cmd := exec.Command(shell, argv...) //nolint:gosec // G204: fixed OS-selected interpreter and repo script; fixture paths are separate arguments.
			mode := ""
			switch scenario {
			case "existing-old", "new-old":
				mode = "old"
			case "version-fail", "dry-run-existing":
				mode = "version-fail"
			case "command-fail":
				mode = "command-fail"
			}
			env := map[string]string{"BOOTSTRAP_FIXTURE_MODE": mode, "BOOTSTRAP_FIXTURE_ARGS": argsFile, "TMPDIR": staging, "TEMP": staging, "TMP": staging, "PATH": toolDir}
			if scenario == "existing-path" {
				env["PATH"] = existingDir + string(os.PathListSeparator) + toolDir
			}
			if scenario == "concurrent-winner" {
				env["BOOTSTRAP_FIXTURE_WINNER"] = filepath.Join(installDir, binaryName)
			}
			if strings.HasPrefix(scenario, "windows-") {
				env["PROCESSOR_ARCHITECTURE"] = "x86"
				env["PROCESSOR_ARCHITEW6432"] = "ARM64"
			}
			if strings.HasPrefix(scenario, "linux-") || strings.HasPrefix(scenario, "darwin-") {
				fakeBin := filepath.Join(root, "tools")
				mkdir(t, fakeBin)
				kernel, machine := "Linux", "x86_64"
				if platform == "darwin" {
					kernel = "Darwin"
				}
				if arch == "arm64" {
					machine = "arm64"
				}
				write(t, filepath.Join(fakeBin, "uname"), []byte("#!/bin/sh\ncase \"$1\" in -s) echo "+kernel+";; -m) echo "+machine+";; *) exit 1;; esac\n"), 0700)
				env["PATH"] = fakeBin + string(os.PathListSeparator) + toolDir
			}
			cmd.Env = replaceEnv(os.Environ(), env)
			output, runErr := cmd.CombinedOutput()
			platformCase := strings.HasPrefix(scenario, "linux-") || strings.HasPrefix(scenario, "darwin-") || strings.HasPrefix(scenario, "windows-")
			success := platformCase || scenario == "new-auto" || scenario == "new" || scenario == "new-project" || scenario == "existing" || scenario == "existing-path" || scenario == "dry-run" || scenario == "dry-run-existing"
			if (runErr == nil) != success {
				t.Fatalf("exit=%v want success=%v\n%s", runErr, success, output)
			}
			if existing || strings.HasPrefix(scenario, "dry-run") || scenario == "invalid-agents" {
				if requests.Load() != 0 {
					t.Errorf("unexpected network requests: %d", requests.Load())
				}
			}
			if existing {
				got, err := os.ReadFile(filepath.Join(existingDir, binaryName))
				if err != nil || !bytes.Equal(got, fixture) {
					t.Errorf("existing binary changed: %v", err)
				}
			}
			installed := platformCase || scenario == "new-auto" || scenario == "new" || scenario == "new-project" || scenario == "command-fail" || (scenario == "existing" || scenario == "existing-old" || scenario == "dry-run-existing")
			if scenario == "concurrent-winner" {
				got, err := os.ReadFile(filepath.Join(installDir, binaryName))
				if err != nil || string(got) != "concurrent winner" {
					t.Fatalf("winner replaced: %v", err)
				}
			} else if got, err := os.ReadFile(filepath.Join(installDir, binaryName)); installed {
				if err != nil || !bytes.Equal(got, fixture) {
					t.Errorf("installed binary mismatch: %v", err)
				}
			} else if !os.IsNotExist(err) {
				t.Errorf("unexpected installed binary (%d bytes): %v", len(got), err)
			}
			invoked := success && withProject && !strings.HasPrefix(scenario, "dry-run")
			gotArgs, err := os.ReadFile(argsFile)
			if invoked {
				var args []string
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(gotArgs, &args); err != nil {
					t.Fatal(err)
				}
				want := []string{"vibe-init", project}
				if scenario != "new-auto" {
					want = append(want, "--agents", agents)
				}
				if !reflect.DeepEqual(args, want) {
					t.Errorf("args = %#v want %#v", args, want)
				}
			} else if !os.IsNotExist(err) {
				t.Errorf("unexpected command invocation: %s (%v)", gotArgs, err)
			}
			if got, err := os.ReadFile(sentinel); err != nil || !bytes.Equal(got, original) {
				t.Errorf("project config changed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
				t.Errorf("archive traversal wrote outside staging: %v", err)
			}
			entries, err := os.ReadDir(staging)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("staging leaked: %v", entries)
			}
			if entries, err := os.ReadDir(installDir); err == nil {
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".onebase-publish") {
						t.Errorf("publication file leaked: %s", entry.Name())
					}
				}
			}
		})
	}
}

func makeArchive(t *testing.T, fixture []byte, bundle, scenario string) []byte {
	t.Helper()
	member := "onebase.exe"
	if runtime.GOOS != "windows" {
		member = bundle + "/onebase"
	}
	if scenario == "missing-binary" {
		member = "wrong/onebase"
	}
	names := []string{member, "../outside", "examples/readme.txt"}
	if scenario == "duplicate-binary" {
		names = append(names, member)
	}
	var b bytes.Buffer
	if runtime.GOOS == "windows" {
		z := zip.NewWriter(&b)
		for _, name := range names {
			content := []byte("unrelated")
			if name == member {
				content = fixture
			}
			w, err := z.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		g := gzip.NewWriter(&b)
		z := tar.NewWriter(g)
		for _, name := range names {
			content := []byte("unrelated")
			if name == member {
				content = fixture
			}
			if err := z.WriteHeader(&tar.Header{Name: name, Mode: 0700, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := z.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := g.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}
func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
}
func write(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil { //nolint:gosec // G703: helper only writes fixture paths beneath t.TempDir; no HTTP/request data.
		t.Fatal(err)
	}
}
func replaceEnv(base []string, values map[string]string) []string {
	var result []string
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		found := false
		for k := range values {
			if strings.EqualFold(k, key) {
				found = true
				break
			}
		}
		if !found {
			result = append(result, entry)
		}
	}
	for k, v := range values {
		result = append(result, k+"="+v)
	}
	return result
}
