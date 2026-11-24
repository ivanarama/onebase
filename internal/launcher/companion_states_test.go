package launcher

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCompanionStatesTrackLaunchAndExit(t *testing.T) {
	setLauncherDir(t, t.TempDir())
	store := newTestStore(t)
	base, err := NewClientBase("Test", "https://example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	base.Companions = []Companion{
		{Key: "worker", Path: "worker"},
		{Key: "broken", Path: "broken"},
		{Key: "absent", Path: "never-installed"},
		{Key: "unrequested", Path: "unrequested"},
	}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	workerExit := make(chan error, 1)
	brokenExit := make(chan error, 1)
	t.Cleanup(func() {
		close(workerExit)
		close(brokenExit)
	})
	rt := companionRuntime{
		stat: func(path string) error {
			if path == "never-installed" {
				return os.ErrNotExist
			}
			return nil
		},
		running: func(string) bool { return false },
		start: func(c Companion) (func() error, error) {
			switch c.Key {
			case "worker":
				return func() error { return <-workerExit }, nil
			case "broken":
				return func() error { return <-brokenExit }, nil
			default:
				return nil, errors.New("unexpected program")
			}
		},
	}
	msgs := startBaseCompanions(newCompanionLauncher(rt), store, base.ID, []string{"worker", "broken", "absent", "undeclared"})
	if len(msgs) != 1 {
		t.Fatalf("launch messages: %v", msgs)
	}
	read := func() map[string]companionState {
		t.Helper()
		unlock, err := lockCompanions(store)
		if err != nil {
			t.Fatal(err)
		}
		states, err := store.readCompanionStates(base.ID)
		unlock()
		if err != nil {
			t.Fatal(err)
		}
		return states
	}
	states := read()
	if len(states) != 4 || states["worker"].Status != companionRunning || states["broken"].Status != companionRunning ||
		states["absent"].Status != companionUnavailable || states["undeclared"].Status != companionUndeclared {
		t.Fatalf("states after launch: %+v", states)
	}
	workerExit <- nil
	brokenExit <- errors.New("worker failed")
	deadline := time.Now().Add(5 * time.Second)
	for {
		states = read()
		if states["worker"].Status == companionExited && states["broken"].Status == companionFailed {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("states after completion: %+v", states)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOldCompanionExitKeepsNewAttempt(t *testing.T) {
	store := newTestStore(t)
	old := companionState{Key: "worker", Status: companionRunning, Path: "worker", Attempt: "old"}
	next := old
	next.Attempt = "new"
	store.writeCompanionStates("base", map[string]companionState{next.Key: next})
	store.completeCompanionLaunch("base", old, errors.New("previous attempt failed"))
	states, err := store.readCompanionStates("base")
	if err != nil || states[next.Key] != next {
		t.Fatalf("current launch overwritten: %+v, %v", states, err)
	}
}

func TestIndexCompanionStatesThroughHTTP(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(host.Close)
	store := newTestStore(t)
	base, err := NewClientBase("Test", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewClientBase("Other", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []*Base{base, other} {
		if err := store.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store.writeCompanionStates(base.ID, map[string]companionState{
		"ready": {Key: "ready", Status: companionRunning, Path: self},
		"done":  {Key: "done", Status: companionExited},
		"err":   {Key: "err", Status: companionFailed},
		"miss":  {Key: "miss", Status: companionUnavailable},
		"extra": {Key: "extra", Status: companionUndeclared},
	})
	h := &handler{store: store, runner: NewRunner()}
	request := func(id string) string {
		t.Helper()
		w := httptest.NewRecorder()
		h.index(w, httptest.NewRequest(http.MethodGet, "/?sel="+id, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("launcher index: HTTP %d", w.Code)
		}
		return w.Body.String()
	}
	html := request(base.ID)
	for _, status := range []string{"running", "exited", "failed", "unavailable", "undeclared"} {
		if !strings.Contains(html, `data-companion-state="`+status+`"`) {
			t.Errorf("missing status %s", status)
		}
	}
	if strings.Contains(html, self) || strings.Contains(request(other.ID), "data-companion-state=") {
		t.Fatal("companion details escaped the selected base")
	}
}
