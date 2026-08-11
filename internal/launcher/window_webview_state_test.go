//go:build windows && webview

package launcher

import (
	"errors"
	"strings"
	"testing"
)

func TestWindowSessionClosePhaseTransitions(t *testing.T) {
	s := newWindowSession(1, 1, nil)
	if !s.beginCloseCheck() {
		t.Fatal("idle session did not enter close check")
	}
	if s.beginCloseCheck() {
		t.Fatal("a second close check started while the first one was active")
	}

	// A one-shot /quit received while a safety decision is in progress must be
	// consumed, not converted into WM_QUIT behind the decision's back.
	s.requestDoneTermination()
	if got := windowSessionPhase(s); got != windowCheckingClose {
		t.Fatalf("done changed checking phase to %v", got)
	}

	s.cancelCloseCheck()
	if got := windowSessionPhase(s); got != windowIdle {
		t.Fatalf("cancel phase = %v, want idle", got)
	}
	if !s.beginCloseCheck() {
		t.Fatal("session could not retry close after cancellation")
	}
	s.allowClose()
	if got := windowSessionPhase(s); got != windowTerminating {
		t.Fatalf("allow phase = %v, want terminating", got)
	}
}

func TestWindowSessionDoneDoesNotInterruptStop(t *testing.T) {
	s := newWindowSession(1, 1, nil)
	s.mu.Lock()
	s.phase = windowStopping
	s.mu.Unlock()

	s.requestDoneTermination()
	if got := windowSessionPhase(s); got != windowStopping {
		t.Fatalf("done changed stopping phase to %v", got)
	}
}

func TestWindowSessionRunEndIsIdempotent(t *testing.T) {
	s := newWindowSession(1, 1, nil)
	s.markRunEnded()
	s.markRunEnded()
	if got := windowSessionPhase(s); got != windowEnded {
		t.Fatalf("ended phase = %v", got)
	}
	select {
	case <-s.runEndedCh:
	default:
		t.Fatal("run-ended channel was not closed")
	}
	if s.beginCloseCheck() {
		t.Fatal("ended session accepted a new close check")
	}
}

func TestNativeCloseErrorsAreFailClosedAndLocalized(t *testing.T) {
	err := errors.New("boom")
	for _, tc := range []struct {
		lang string
		want string
	}{
		{lang: "ru", want: "Окно лаунчера останется открытым"},
		{lang: "en-US", want: "The launcher window will remain open"},
	} {
		for _, got := range []string{
			closeStateErrorText(tc.lang, err),
			stopBasesErrorText(tc.lang, err),
			stopTimeoutErrorText(tc.lang),
		} {
			if !strings.Contains(got, tc.want) {
				t.Errorf("%s text does not say the window remains open: %q", tc.lang, got)
			}
		}
	}
}

func windowSessionPhase(s *windowSession) windowPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}
