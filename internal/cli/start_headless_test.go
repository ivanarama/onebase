package cli

import (
	"testing"

	"github.com/ivantit66/onebase/internal/launcher"
)

func TestRunLauncherFrontendHonorsNoGUI(t *testing.T) {
	oldOpen, oldWait, oldNoGUI := openLauncherFrontend, waitLauncherFrontend, noGUI
	t.Cleanup(func() {
		openLauncherFrontend, waitLauncherFrontend, noGUI = oldOpen, oldWait, oldNoGUI
	})

	for _, tc := range []struct {
		name     string
		env      string
		flag     bool
		wantOpen int
		wantWait int
	}{
		{name: "default opens frontend", wantOpen: 1},
		{name: "environment is headless", env: "1", wantWait: 1},
		{name: "flag is headless", flag: true, wantWait: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ONEBASE_NO_GUI", tc.env)
			noGUI = tc.flag
			openCalls, waitCalls := 0, 0
			openLauncherFrontend = func(string, string, <-chan struct{}, launcher.CloseCoordinator) error {
				openCalls++
				return nil
			}
			waitLauncherFrontend = func(<-chan struct{}) { waitCalls++ }

			if err := runLauncherFrontend("http://127.0.0.1:1/", "title", make(chan struct{}), nil); err != nil {
				t.Fatal(err)
			}
			if openCalls != tc.wantOpen || waitCalls != tc.wantWait {
				t.Fatalf("open calls = %d, wait calls = %d; want %d, %d",
					openCalls, waitCalls, tc.wantOpen, tc.wantWait)
			}
		})
	}
}
