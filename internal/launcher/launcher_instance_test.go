package launcher

import "testing"

func TestLauncherInstanceRejectsSecondLauncher(t *testing.T) {
	isolatedUpdatesHome(t)
	first, err := AcquireLauncherInstance(false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release() //nolint:errcheck // test cleanup

	if second, err := AcquireLauncherInstance(false); err == nil {
		_ = second.Release()
		t.Fatal("second launcher acquired the same per-user instance lease")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	after, err := AcquireLauncherInstance(false)
	if err != nil {
		t.Fatalf("instance lease was not reusable after release: %v", err)
	}
	if err := after.Release(); err != nil {
		t.Fatal(err)
	}
}
