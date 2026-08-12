package launcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/processcontrol"
)

func controlTestBase(t *testing.T, ts *httptest.Server, token string) *Base {
	t.Helper()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return &Base{ID: "base-control", Name: "Управляемая", Port: port, ControlToken: token}
}

func TestManagedProcMatchesPersistentGeneration(t *testing.T) {
	mp := &managedProc{port: 8080, controlToken: "generation-a"}
	if !managedProcMatchesBase(mp, &Base{ID: "same", Port: 9090, ControlToken: "generation-a"}) {
		t.Fatal("mutable port change split the same persistent generation")
	}
	if managedProcMatchesBase(mp, &Base{ID: "same", Port: 8080, ControlToken: "generation-b"}) {
		t.Fatal("reused ID and port matched a different persistent generation")
	}
}

func authenticatedControlHandler(t *testing.T, secret, baseID string, onStop func()) http.Handler {
	t.Helper()
	const instance = "test-process-instance"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/debug/process/identity":
			challenge := r.URL.Query().Get(processcontrol.ChallengeQuery)
			identity := processcontrol.Identity{BaseID: baseID, PID: 4242, Instance: instance}
			identity.Proof = processcontrol.IdentityProof(secret, baseID, identity.PID, instance, challenge)
			_ = json.NewEncoder(w).Encode(identity)
		case "/debug/process/stop":
			nonce := r.Header.Get(processcontrol.HeaderNonce)
			want := processcontrol.StopProof(secret, baseID, instance, nonce)
			if r.Header.Get(processcontrol.HeaderBaseID) != baseID ||
				r.Header.Get(processcontrol.HeaderInstance) != instance ||
				!processcontrol.Verify(r.Header.Get(processcontrol.HeaderProof), want) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			if onStop != nil {
				onStop()
			}
		case "/healthz":
			w.Header().Set("X-OneBase-Version", "test")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	})
}

func TestRuntimeStatus_RequiresAuthenticatedIdentityForControl(t *testing.T) {
	const token = "secret"
	ts := httptest.NewServer(authenticatedControlHandler(t, token, "base-control", nil))
	t.Cleanup(ts.Close)

	runner := NewRunner()
	base := controlTestBase(t, ts, token)
	if got := runner.RuntimeStatus(base); !got.Running || !got.Controllable || !got.Occupied {
		t.Fatalf("valid proof must give controllable status: %+v", got)
	}
	base.ControlToken = "wrong"
	if got := runner.RuntimeStatus(base); got.Running || got.Controllable || !got.Occupied {
		t.Fatalf("failed HMAC identity must remain only an occupied blocker: %+v", got)
	}
}

func TestRuntimeStatus_DoesNotDiscloseSecretToUntrustedListenerOrRedirect(t *testing.T) {
	var sinkHits atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { sinkHits.Add(1) }))
	t.Cleanup(sink.Close)
	var leaked atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, values := range r.Header {
			if strings.Contains(strings.Join(values, "\n"), "persistent-secret") {
				leaked.Store(true)
			}
		}
		http.Redirect(w, r, sink.URL, http.StatusFound)
	}))
	t.Cleanup(ts.Close)
	base := controlTestBase(t, ts, "persistent-secret")
	got := NewRunner().RuntimeStatus(base)
	if got.Controllable || !got.Occupied {
		t.Fatalf("untrusted listener status: %+v", got)
	}
	if leaked.Load() {
		t.Fatal("persistent secret was sent to an untrusted listener")
	}
	if sinkHits.Load() != 0 {
		t.Fatal("local control probe followed a redirect")
	}
}

func TestEnsureBaseReadyMintsIdentityBeforeAnyLegacyHealthProbe(t *testing.T) {
	var publicHealthHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			publicHealthHits.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t)
	base := &Base{
		ID: "legacy-tokenless", Name: "Legacy", Port: port,
		DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "legacy.db"),
	}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bases/legacy-tokenless/start", nil)
	if h.ensureBaseReady(rec, req, base, "ru") {
		t.Fatal("foreign listener with public /health was adopted")
	}
	if publicHealthHits.Load() != 0 {
		t.Fatal("tokenless public /health was probed before persistent identity existed")
	}
	stored, err := store.Get(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ControlToken == "" || base.ControlToken != stored.ControlToken {
		t.Fatalf("control identity was not persisted before probing: request=%q stored=%q",
			base.ControlToken, stored.ControlToken)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want fail-closed 500: %s", rec.Code, rec.Body.String())
	}
}

type channelExitWaiter struct{ done <-chan struct{} }

func (w *channelExitWaiter) Wait(timeout time.Duration) bool {
	if timeout <= 0 {
		select {
		case <-w.done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.done:
		return true
	case <-timer.C:
		return false
	}
}
func (*channelExitWaiter) Close() error { return nil }

func useExitWaiter(t *testing.T, done <-chan struct{}) {
	t.Helper()
	original := openProcessExitWaiter
	openProcessExitWaiter = func(int) (processExitWaiter, error) {
		return &channelExitWaiter{done: done}, nil
	}
	t.Cleanup(func() { openProcessExitWaiter = original })
}

func TestStopBase_WaitsForProcessExitNotOnlyClosedListener(t *testing.T) {
	const token = "secret"
	processExited := make(chan struct{})
	stopSeen := make(chan struct{})
	useExitWaiter(t, processExited)

	var ts *httptest.Server
	var stopOnce sync.Once
	ts = httptest.NewServer(authenticatedControlHandler(t, token, "base-control", func() {
		stopOnce.Do(func() {
			close(stopSeen)
			go ts.Close()
		})
	}))
	base := controlTestBase(t, ts, token)
	result := make(chan error, 1)
	go func() { result <- NewRunner().StopBase(base) }()
	<-stopSeen
	if !waitPortFree(base.Port, 2*time.Second) {
		t.Fatal("test listener did not close")
	}
	select {
	case err := <-result:
		t.Fatalf("StopBase returned before process exit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(processExited)
	if err := <-result; err != nil {
		t.Fatalf("StopBase: %v", err)
	}
}

func TestStopBase_RefusesSuccessWhenPortWasReoccupied(t *testing.T) {
	const token = "secret"
	processExited := make(chan struct{})
	close(processExited)
	useExitWaiter(t, processExited)

	ts := httptest.NewServer(authenticatedControlHandler(t, token, "base-control", nil))
	t.Cleanup(ts.Close)
	base := controlTestBase(t, ts, token)
	if err := NewRunner().StopBase(base); err == nil {
		t.Fatal("завершение старого PID не доказывает остановку базы, если порт снова занят")
	}
}

func TestStopAll_DoesNotKillUnidentifiedListener(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	base := controlTestBase(t, ts, "wrong")
	if err := NewRunner().StopAll([]*Base{base}, false); err == nil {
		t.Fatal("unidentified listener must fail closed")
	}
	if portFree(base.Port) {
		t.Fatal("StopAll killed another process by port")
	}
}

func TestStopAll_PreflightDoesNotPartiallyStopTrackedBases(t *testing.T) {
	trackedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(trackedServer.Close)
	tracked := controlTestBase(t, trackedServer, "tracked-token")
	tracked.ID = "tracked"

	unknownServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(unknownServer.Close)
	unknown := controlTestBase(t, unknownServer, "unknown-token")
	unknown.ID = "unknown"

	runner := NewRunner()
	runner.procs[tracked.ID] = &managedProc{port: tracked.Port}
	if err := runner.StopAll([]*Base{tracked, unknown}, false); err == nil {
		t.Fatal("unknown occupied port must abort StopAll")
	}
	if !runner.IsRunning(tracked.ID) || portFree(tracked.Port) {
		t.Fatal("preflight failure partially stopped or forgot a tracked base")
	}
}

func TestStartRejectedWhileCloseStopOwnsGate(t *testing.T) {
	runner := NewRunner()
	if err := runner.holdStarts(); err != nil {
		t.Fatal(err)
	}
	defer runner.AllowStarts()
	if err := runner.Start(&Base{ID: "new", Name: "Новая", Port: waitReadyFreePort(t)}); err == nil {
		t.Fatal("Start must be rejected between StopAll and launcher exit")
	}
}
