package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/spf13/cobra"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	var buf bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&buf, r)
		copyDone <- err
	}()

	runErr := fn()
	os.Stdout = old
	closeErr := w.Close()
	copyErr := <-copyDone
	if copyErr != nil && runErr == nil {
		runErr = copyErr
	} else if closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	if err := r.Close(); err != nil && runErr == nil {
		runErr = err
	}
	return buf.String(), runErr
}

func TestInstallWindowsServicePrintUsesSQLite(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return installWindowsService(
			`C:\Program Files\OneBase\onebase.exe`,
			"onebase-docflow",
			"docflow",
			"",
			`C:\onebase\data\docflow.db`,
			"sqlite",
			"file",
			`C:\onebase\project`,
			"0.0.0.0",
			8080,
			true,
			true,
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `--sqlite \"C:\onebase\data\docflow.db\"`) {
		t.Fatalf("windows service command must use --sqlite, got:\n%s", out)
	}
	if strings.Contains(out, `--db ""`) {
		t.Fatalf("windows service command must not include empty --db, got:\n%s", out)
	}
	if !strings.Contains(out, `--project \"C:\onebase\project\"`) || !strings.Contains(out, "--watch") {
		t.Fatalf("windows service command lost project/watch args:\n%s", out)
	}
	if !strings.Contains(out, "--host 0.0.0.0") {
		t.Fatalf("windows service command lost host arg:\n%s", out)
	}
	if !strings.Contains(out, `binPath= "\"C:\Program Files\OneBase\onebase.exe\" run`) {
		t.Fatalf("binPath must preserve quotes around executable with spaces, got:\n%s", out)
	}
}

func TestFindMappedNetworkPaths(t *testing.T) {
	detect := func(path string) (bool, error) {
		return strings.HasPrefix(strings.ToUpper(path), `Z:`), nil
	}
	mapped, err := findMappedNetworkPaths([]namedPath{
		{Label: "SQLite", Path: `Z:\DocFlow\app.db`},
		{Label: "проект", Path: `C:\DocFlow`},
		{Label: "UNC", Path: `\\server\share\DocFlow`},
	}, detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapped) != 1 || mapped[0].Path != `Z:\DocFlow\app.db` {
		t.Fatalf("mapped paths = %+v, want only Z:", mapped)
	}
	if advice := mappedDriveAdvice(mapped); !strings.Contains(advice, "LocalSystem") || !strings.Contains(advice, "UNC") {
		t.Fatalf("неинформативная подсказка: %s", advice)
	}
}

func TestInstallWindowsServiceRejectsMappedDrive(t *testing.T) {
	old := detectMappedNetworkDrive
	detectMappedNetworkDrive = func(path string) (bool, error) {
		return strings.HasPrefix(strings.ToUpper(path), `Z:`), nil
	}
	t.Cleanup(func() { detectMappedNetworkDrive = old })

	err := installWindowsService(
		`C:\Program Files\OneBase\onebase.exe`, "onebase-docflow", "docflow", "",
		`Z:\DocFlow\app.db`, "sqlite", "file", `Z:\DocFlow`, "127.0.0.1", 8080, false, false,
	)
	if err == nil || !strings.Contains(err.Error(), "LocalSystem") || !strings.Contains(err.Error(), "UNC") {
		t.Fatalf("mapped drive должен остановить установку с подсказкой, got %v", err)
	}
}

func TestQuoteWindowsCommandArg(t *testing.T) {
	binPath := `"C:\Program Files\OneBase\onebase.exe" run --sqlite "C:\My Data\app.db"`
	got := quoteWindowsCommandArg(binPath)
	want := `"\"C:\Program Files\OneBase\onebase.exe\" run --sqlite \"C:\My Data\app.db\""`
	if got != want {
		t.Fatalf("quoteWindowsCommandArg:\n got: %s\nwant: %s", got, want)
	}
	if got := quoteWindowsCommandArgAlways(`C:\My Data\`); got != `"C:\My Data\\"` {
		t.Fatalf("trailing slash before closing quote was not escaped: %s", got)
	}
}

func TestInstallSystemdPrintUsesSQLite(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("user", "svc", "")
	out, err := captureStdout(t, func() error {
		return installSystemd(
			"/opt/onebase/onebase",
			"onebase-docflow",
			"docflow",
			"",
			"/var/lib/onebase/docflow.db",
			"sqlite",
			"file",
			"/srv/onebase/project",
			"0.0.0.0",
			8080,
			true,
			cmd,
			true,
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `--sqlite "/var/lib/onebase/docflow.db"`) {
		t.Fatalf("systemd unit must use --sqlite, got:\n%s", out)
	}
	if strings.Contains(out, `--db ""`) {
		t.Fatalf("systemd unit must not include empty --db, got:\n%s", out)
	}
	if !strings.Contains(out, `--project "/srv/onebase/project"`) || !strings.Contains(out, "--watch") {
		t.Fatalf("systemd unit lost project/watch args:\n%s", out)
	}
	if !strings.Contains(out, `--host "0.0.0.0"`) {
		t.Fatalf("systemd unit lost host arg:\n%s", out)
	}
}

func TestRunServiceInstallPrintCarriesExplicitHost(t *testing.T) {
	cmd := &cobra.Command{}
	addServiceInstallFlags(cmd)
	for name, value := range map[string]string{
		"sqlite": filepath.Join(t.TempDir(), "base.db"),
		"name":   "onebase-host-test",
		"host":   "0.0.0.0",
		"print":  "true",
	} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}

	out, err := captureStdout(t, func() error { return runServiceInstall(cmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !serviceOutputHasHost(out, "0.0.0.0") {
		t.Fatalf("service install --host was not carried into generated command:\n%s", out)
	}
}

func TestRunServiceInstallPrintDefaultsToLoopback(t *testing.T) {
	cmd := &cobra.Command{}
	addServiceInstallFlags(cmd)
	for name, value := range map[string]string{
		"sqlite": filepath.Join(t.TempDir(), "base.db"),
		"name":   "onebase-loopback-test",
		"print":  "true",
	} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}

	out, err := captureStdout(t, func() error { return runServiceInstall(cmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !serviceOutputHasHost(out, "127.0.0.1") {
		t.Fatalf("service install default host must stay loopback:\n%s", out)
	}
}

func TestRunServiceInstallPrintInheritsRegistryHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	store, err := launcher.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	base := &launcher.Base{
		ID: "service-host-test", Name: "host-test", DBType: "sqlite",
		DBPath: filepath.Join(home, "base.db"), Port: 18080,
		ConfigSource: "database", Host: "0.0.0.0",
	}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	addServiceInstallFlags(cmd)
	if err := cmd.Flags().Set("id", base.ID); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("print", "true"); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return runServiceInstall(cmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !serviceOutputHasHost(out, "0.0.0.0") {
		t.Fatalf("service install --id did not inherit registered host:\n%s", out)
	}

	override := &cobra.Command{}
	addServiceInstallFlags(override)
	for name, value := range map[string]string{
		"id":    base.ID,
		"host":  "127.0.0.1",
		"print": "true",
	} {
		if err := override.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	out, err = captureStdout(t, func() error { return runServiceInstall(override, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !serviceOutputHasHost(out, "127.0.0.1") || serviceOutputHasHost(out, "0.0.0.0") {
		t.Fatalf("explicit --host must override registered host:\n%s", out)
	}

	base.Host = "unexpected.example"
	if err := store.Update(base); err != nil {
		t.Fatal(err)
	}
	corrupt := &cobra.Command{}
	addServiceInstallFlags(corrupt)
	if err := corrupt.Flags().Set("id", base.ID); err != nil {
		t.Fatal(err)
	}
	if err := corrupt.Flags().Set("print", "true"); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return runServiceInstall(corrupt, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !serviceOutputHasHost(out, "127.0.0.1") || strings.Contains(out, "unexpected.example") {
		t.Fatalf("unknown registered host must fail safely to loopback:\n%s", out)
	}
}

func serviceOutputHasHost(out, host string) bool {
	return strings.Contains(out, "--host "+host) || strings.Contains(out, `--host "`+host+`"`)
}

func TestSystemdQuoteEscapesSpecialCharacters(t *testing.T) {
	got, err := systemdQuote("/srv/a b/onebase\\bin\"x%")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"/srv/a b/onebase\\bin\"x%%"` {
		t.Fatalf("systemdQuote=%q", got)
	}
	if _, err := systemdQuote("bad\narg"); err == nil {
		t.Fatal("newline must be rejected")
	}
}

func TestValidServiceNameRejectsPath(t *testing.T) {
	if validServiceName("../../evil") || validServiceName("bad name") {
		t.Fatal("unsafe service name accepted")
	}
	if !validServiceName("onebase-app_1@blue") {
		t.Fatal("valid service name rejected")
	}
}
