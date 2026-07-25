//go:build windows && webview

package launcher

// Тесты нативных изолированных окон компилируются только в GUI-сборке
// (go test -tags webview ./internal/launcher/).

import (
	"strings"
	"testing"
)

func TestNativeIsolatedCommand(t *testing.T) {
	t.Setenv("ONEBASE_WEBVIEW_PROFILE", `C:\profiles\inherited`)
	if !nativeIsolatedSupported() {
		t.Fatal("GUI-сборка под Windows обязана поддерживать нативные окна")
	}
	cmd, ok := nativeIsolatedCommand(`C:\profiles\p1`, "http://localhost:8080/ui")
	if !ok {
		t.Fatal("nativeIsolatedCommand должен вернуть команду")
	}
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"window", "--url", "http://localhost:8080/ui"} {
		if !strings.Contains(joined, want) {
			t.Errorf("аргументы не содержат %q: %v", want, cmd.Args)
		}
	}
	found, inherited := 0, false
	for _, e := range cmd.Env {
		if e == `ONEBASE_WEBVIEW_PROFILE=C:\profiles\p1` {
			found++
		}
		if e == `ONEBASE_WEBVIEW_PROFILE=C:\profiles\inherited` {
			inherited = true
		}
	}
	if found != 1 || inherited {
		t.Errorf("в окружении нет ONEBASE_WEBVIEW_PROFILE: %v", cmd.Env)
	}
}

func TestNativeSharedProfileDoesNotInheritIsolatedProfile(t *testing.T) {
	t.Setenv("ONEBASE_WEBVIEW_PROFILE", `C:\profiles\inherited`)
	cmd, ok := nativeIsolatedCommand("", "http://localhost:8080/ui")
	if !ok {
		t.Fatal("nativeIsolatedCommand должен вернуть команду")
	}
	for _, e := range cmd.Env {
		if strings.HasPrefix(strings.ToUpper(e), "ONEBASE_WEBVIEW_PROFILE=") {
			t.Fatalf("общее окно унаследовало изолированный профиль: %q", e)
		}
	}
}
