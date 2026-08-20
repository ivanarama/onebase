//go:build windows

package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsSystemInstallIsNotSelfUpdatableEvenWithWriteAccess(t *testing.T) {
	profile, err := windows.KnownFolderPath(windows.FOLDERID_Profile, windows.KF_FLAG_DEFAULT)
	if err != nil {
		t.Fatal(err)
	}
	volume := filepath.VolumeName(profile)
	systemTarget := filepath.Join(volume+string(filepath.Separator), "Program Files", "onebase")
	// Причину читают через errors.Is: интерфейс формулирует общую установку и
	// отсутствие прав по-разному, а на текст сообщения опираться нельзя.
	if err := validateTargetCoordinationDirectory(systemTarget, nil); !errors.Is(err, ErrTargetShared) {
		t.Fatalf("system installation policy error = %v, want ErrTargetShared", err)
	}
}

func TestWindowsTempEnvironmentCannotBroadenPrivateInstallBoundary(t *testing.T) {
	profile, err := windows.KnownFolderPath(windows.FOLDERID_Profile, windows.KF_FLAG_DEFAULT)
	if err != nil {
		t.Fatal(err)
	}
	volume := filepath.VolumeName(profile)
	systemTarget := filepath.Join(volume+string(filepath.Separator), "Program Files", "onebase")
	t.Setenv("TEMP", filepath.Dir(systemTarget))
	t.Setenv("TMP", filepath.Dir(systemTarget))
	if err := validateTargetCoordinationDirectory(systemTarget, nil); err == nil {
		t.Fatal("caller-controlled TEMP/TMP broadened the private install boundary")
	}
}

func TestReadOnlyConsumerPathMaySkipCoordination(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-writable-or-missing")
	lease, err := AcquireConsumerLeaseIfWritable(missing)
	if err != nil || lease != nil {
		if lease != nil {
			_ = lease.Release()
		}
		t.Fatalf("read-only/system-style consumer path should remain launchable: lease=%v err=%v", lease, err)
	}
	for _, name := range []string{targetOperationLockFileName, targetOperationIntentLockFileName} {
		if _, statErr := os.Lstat(filepath.Join(missing, name)); !os.IsNotExist(statErr) {
			t.Fatalf("read-only consumer path created %s: %v", name, statErr)
		}
	}
}
