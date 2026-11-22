//go:build windows

package launcher

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Проверка «уже запущена» по настоящему списку процессов: сам тестовый
// процесс работает, программа по несуществующему пути — нет.
func TestCompanionProcessRunningWindows(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !companionProcessRunning(self) {
		t.Fatalf("работающий процесс %s не найден", self)
	}
	if companionProcessRunning(filepath.Join(t.TempDir(), "нет-такой-программы.exe")) {
		t.Fatal("несуществующая программа считается запущенной")
	}
}

func TestCompanionWindowsProcessFixture(t *testing.T) {
	dir := os.Getenv("ONEBASE_TEST_COMPANION_PROCESS_DIR")
	switch os.Getenv("ONEBASE_TEST_COMPANION_PROCESS_ROLE") {
	case "window":
		store := &Store{path: filepath.Join(dir, "ibases.yaml")}
		messages := startBaseCompanions(newCompanionLauncher(systemCompanionRuntime), store,
			os.Getenv("ONEBASE_TEST_COMPANION_BASE"), []string{"helper"})
		if len(messages) != 0 {
			t.Fatalf("window start: %v", messages)
		}
	case "helper":
		marker, err := os.OpenFile(filepath.Join(dir, "starts"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := marker.WriteString("started\n"); err != nil {
			t.Fatal(err)
		}
		if err := marker.Close(); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
			if _, err := os.Stat(filepath.Join(dir, "finish")); err == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("test helper was not released")
	default:
		t.Skip("subprocess fixture")
	}
}

func TestCompanionSurvivesWindowProcessesWithoutDuplicatesWindows(t *testing.T) {
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer binary.Close()
	helper, err := os.CreateTemp(dir, "companion-*.exe")
	if err != nil {
		t.Fatal(err)
	}
	helperPath := helper.Name()
	_, copyErr := io.Copy(helper, binary)
	closeErr := helper.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy test process: %v, %v", copyErr, closeErr)
	}
	requestExit := func() {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "finish"), nil, 0o600); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(requestExit)

	store := &Store{path: filepath.Join(dir, "ibases.yaml")}
	base, err := NewClientBase("Fixture", "https://example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	base.Companions = []Companion{{
		Key: "helper", Path: helperPath, Args: []string{"-test.run=^TestCompanionWindowsProcessFixture$"},
		Env: map[string]string{"ONEBASE_TEST_COMPANION_PROCESS_ROLE": "helper"},
	}}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for range 2 {
		go func() {
			cmd := exec.CommandContext(ctx, self, "-test.run=^TestCompanionWindowsProcessFixture$") //nolint:gosec // current test executable with fixed fixture argument
			detachCompanion(cmd)
			cmd.Env = append(os.Environ(), "ONEBASE_TEST_COMPANION_PROCESS_DIR="+dir,
				"ONEBASE_TEST_COMPANION_PROCESS_ROLE=window", "ONEBASE_TEST_COMPANION_BASE="+base.ID)
			results <- cmd.Run()
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("window fixture: %v", err)
		}
	}
	var starts []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if starts, err = os.ReadFile(filepath.Join(dir, "starts")); err == nil && len(starts) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if string(starts) != "started\n" || !companionProcessRunning(helperPath) {
		t.Fatalf("expected one helper alive after both windows closed, starts=%q", starts)
	}
	states := store.companionStates(base.ID)
	if len(states) != 1 || states[0].Status != companionRunning {
		t.Fatalf("launcher did not see running helper: %+v", states)
	}
	requestExit()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		states = store.companionStates(base.ID)
		if len(states) == 1 && states[0].Status == companionExited {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("launcher did not see helper completion: %+v", states)
}
