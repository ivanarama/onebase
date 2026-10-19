package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const restLoaderProcessEnv = "ONEBASE_TEST_REST_LOADER"

type restLoaderProcessConfig struct {
	Base, Root, Loader, Worker string
	PinOwnerHTTP               bool
}

type restLoaderSnapshot struct {
	Pulls        []apiPull
	PullComments [][]apiComment // apiPull.Comments is deliberately not JSON-visible.
	Issues       []apiIssue
}

type restLoaderMessage struct {
	Phase    string
	Error    string
	Snapshot restLoaderSnapshot
}

// This is only a test composition seam: every child calls the production
// loader, including pagination, comment reads, getJSON and disk persistence.
// The worker header identifies HTTP observations without changing cache keys.
type restLoaderTransport struct {
	base   http.RoundTripper
	worker string
	pin    bool
}

func (transport restLoaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx := request.Context()
	if transport.pin {
		// Only the budget-test owner ignores its HTTP deadline. Otherwise its
		// own 45-second getJSON deadline releases the lock before the later
		// waiter can exhaust 45 seconds. The waiter's context is untouched;
		// the owner's OS-process deadline still bounds this test-only pin.
		ctx = context.WithoutCancel(ctx)
	}
	request = request.Clone(ctx)
	request.Header.Set("X-Test-Worker", transport.worker)
	return transport.base.RoundTrip(request)
}

func TestRESTCacheLoaderProcess(t *testing.T) {
	configuration := os.Getenv(restLoaderProcessEnv)
	if configuration == "" {
		return
	}
	var config restLoaderProcessConfig
	if err := json.Unmarshal([]byte(configuration), &config); err != nil {
		t.Fatal(err)
	}
	client, err := newGitHubRESTClientWithBase(config.Base, "test-token", config.Root,
		&http.Client{Transport: restLoaderTransport{http.DefaultTransport, config.Worker, config.PinOwnerHTTP}})
	if err != nil {
		t.Fatal(err)
	}
	output := json.NewEncoder(os.Stdout)
	if err := output.Encode(restLoaderMessage{Phase: "ready"}); err != nil {
		t.Fatal(err)
	}
	var command string
	if err := json.NewDecoder(os.Stdin).Decode(&command); err != nil || command != "load" {
		t.Fatalf("load command: %q, %v", command, err)
	}
	if err := output.Encode(restLoaderMessage{Phase: "loading"}); err != nil {
		t.Fatal(err)
	}
	result := restLoaderMessage{Phase: "done"}
	switch config.Loader {
	case "pulls":
		result.Snapshot.Pulls, err = loadPulls(client, "ivanarama/onebase", "ivanarama", "")
		for _, pull := range result.Snapshot.Pulls {
			result.Snapshot.PullComments = append(result.Snapshot.PullComments, pull.Comments)
		}
	case "issues":
		result.Snapshot.Issues, err = loadIssues(client, "ivanarama/onebase", "", false)
	default:
		t.Fatalf("unknown loader %q", config.Loader)
	}
	if err != nil {
		result.Error = err.Error()
	}
	if err := output.Encode(result); err != nil {
		t.Fatal(err)
	}
}

type restLoaderChild struct {
	cmd      *exec.Cmd
	input    io.WriteCloser
	messages chan restLoaderMessage
	readErr  chan error
	done     chan struct{}
	waitErr  error // read only after done is closed by the single Wait goroutine
	stderr   bytes.Buffer
}

func startRESTLoaderChild(t *testing.T, config restLoaderProcessConfig) *restLoaderChild {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	// The executable is this test binary, not repository/HTTP-provided input.
	//nolint:gosec // G204: re-executing the current test binary with fixed arguments.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRESTCacheLoaderProcess$", "-test.timeout=75s")
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, restLoaderProcessEnv+"=") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, restLoaderProcessEnv+"="+string(encoded))
	child := &restLoaderChild{cmd: cmd, messages: make(chan restLoaderMessage, 3),
		readErr: make(chan error, 1), done: make(chan struct{})}
	cmd.Stderr = &child.stderr
	child.input, err = cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = child.input.Close()
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		_ = child.input.Close()
		_ = output.Close()
		cancel()
		t.Fatal(err)
	}
	// Drain the protocol before Wait closes StdoutPipe. The trailing Go test
	// PASS line is not protocol data; consume it too before reaping the child.
	go func() {
		decoder := json.NewDecoder(output)
		for range 3 {
			var message restLoaderMessage
			if err := decoder.Decode(&message); err != nil {
				child.readErr <- err
				break
			}
			child.messages <- message
		}
		_, _ = io.Copy(io.Discard, io.MultiReader(decoder.Buffered(), output))
		child.waitErr = cmd.Wait()
		close(child.done)
	}()
	t.Cleanup(func() {
		cancel()
		_ = child.input.Close()
		select {
		case <-child.done:
		case <-time.After(10 * time.Second):
			t.Error("REST loader child was not reaped during cleanup")
		}
	})
	child.message(t, "ready", 15*time.Second)
	return child
}

func (child *restLoaderChild) message(t *testing.T, phase string, budget time.Duration) restLoaderMessage {
	t.Helper()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case message := <-child.messages:
		if message.Phase != phase {
			t.Fatalf("child phase = %q, want %q", message.Phase, phase)
		}
		return message
	case err := <-child.readErr:
		t.Fatalf("child protocol (%s): %v", phase, err)
	case <-timer.C:
		t.Fatalf("child did not report %s within %s", phase, budget)
	}
	return restLoaderMessage{}
}

func (child *restLoaderChild) load(t *testing.T) {
	t.Helper()
	if err := json.NewEncoder(child.input).Encode("load"); err != nil {
		t.Fatal(err)
	}
	child.message(t, "loading", 10*time.Second)
}

func (child *restLoaderChild) wait(t *testing.T, killed bool) {
	t.Helper()
	select {
	case <-child.done:
		if killed {
			if _, ok := child.waitErr.(*exec.ExitError); !ok || child.cmd.ProcessState == nil {
				t.Fatalf("killed child Wait = %v, state = %v", child.waitErr, child.cmd.ProcessState)
			}
		} else if child.waitErr != nil {
			t.Fatalf("child Wait: %v; stderr: %s", child.waitErr, child.stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child did not exit within reap budget")
	}
}

func (child *restLoaderChild) kill(t *testing.T) {
	t.Helper()
	if err := child.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.wait(t, true)
}

type restLoaderRequest struct {
	Worker, Path, ETag string
}

type restLoaderServer struct {
	server   *httptest.Server
	root     string
	loader   string
	listPath string
	fixtures map[string][]byte
	expected restLoaderSnapshot
	entered  chan restLoaderRequest
	release  chan struct{}
	resumeB  chan struct{}
	exited   chan struct{}
	mu       sync.Mutex
	requests []restLoaderRequest
	active   map[string]map[*http.Request]struct{}
}

func newRESTLoaderServer(t *testing.T, loader string, holdB bool) *restLoaderServer {
	t.Helper()
	comments := []byte(`[{"id":1,"created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-01T00:00:00Z","user":{"login":"ivanarama"},"body":"Снимок комментария"}]`)
	listPath := "/repos/ivanarama/onebase/" + loader
	fixture := []byte(`[{"number":7,"title":"Снимок PR","body":"body","state":"open","head":{"sha":"` + headA + `"},"base":{"ref":"main"},"labels":[]}]`)
	commentPath := "/repos/ivanarama/onebase/issues/7/comments"
	if loader == "issues" {
		// The live issues loader must filter the pull and populate issue.Thread.
		fixture = []byte(`[{"number":8,"title":"Снимок заявки","body":"body","state":"open","comments":1,"labels":[]},{"number":7,"pull_request":{"url":"pull"}}]`)
		commentPath = "/repos/ivanarama/onebase/issues/8/comments"
	}
	harness := &restLoaderServer{root: t.TempDir(), loader: loader, listPath: listPath,
		fixtures: map[string][]byte{listPath: fixture, commentPath: comments},
		entered:  make(chan restLoaderRequest, 16), release: make(chan struct{}),
		resumeB: make(chan struct{}), exited: make(chan struct{}),
		active: make(map[string]map[*http.Request]struct{})}
	if !holdB {
		close(harness.resumeB)
	}
	var thread []apiComment
	if err := json.Unmarshal(comments, &thread); err != nil {
		t.Fatal(err)
	}
	if loader == "pulls" {
		if err := json.Unmarshal(fixture, &harness.expected.Pulls); err != nil {
			t.Fatal(err)
		}
		harness.expected.PullComments = [][]apiComment{thread}
	} else {
		if err := json.Unmarshal(fixture, &harness.expected.Issues); err != nil {
			t.Fatal(err)
		}
		harness.expected.Issues = harness.expected.Issues[:1]
		harness.expected.Issues[0].Thread = thread
	}
	harness.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !requireTestAuthentication(response, request) {
			t.Error("child HTTP request was not authenticated")
			return
		}
		body, ok := harness.fixtures[request.URL.Path]
		if !ok || request.URL.Query().Get("page") != "1" || request.URL.Query().Get("per_page") != "100" {
			t.Errorf("unexpected loader endpoint: %s", request.URL)
			http.Error(response, "unexpected endpoint", http.StatusBadRequest)
			return
		}
		observation := restLoaderRequest{request.Header.Get("X-Test-Worker"), request.URL.Path, request.Header.Get("If-None-Match")}
		harness.mu.Lock()
		harness.requests = append(harness.requests, observation)
		if harness.active[observation.Path] == nil {
			harness.active[observation.Path] = make(map[*http.Request]struct{})
		}
		harness.active[observation.Path][request] = struct{}{}
		active := 0
		for live := range harness.active[observation.Path] {
			// A killed client's socket can close before its server handler's
			// deferred cleanup is scheduled. Count only live HTTP requests.
			if live.Context().Err() == nil {
				active++
			}
		}
		harness.mu.Unlock()
		defer func() {
			harness.mu.Lock()
			delete(harness.active[observation.Path], request)
			harness.mu.Unlock()
		}()
		if active != 1 {
			t.Errorf("overlapping HTTP critical sections for %s: %d", observation.Path, active)
		}
		if observation.Path == harness.listPath {
			harness.entered <- observation
			if observation.Worker == "A" {
				defer close(harness.exited)
				select {
				case <-harness.release:
				case <-request.Context().Done():
					return
				}
			}
		}
		if observation.Path == harness.listPath && observation.Worker == "B" {
			select {
			case <-harness.resumeB:
			case <-request.Context().Done():
				return
			}
		}
		etag := `"stable-` + loader + `-` + filepath.Base(observation.Path) + `"`
		if observation.ETag != "" && observation.ETag != etag {
			t.Errorf("unexpected ETag: %q", observation.ETag)
			http.Error(response, "wrong ETag", http.StatusBadRequest)
			return
		}
		response.Header().Set("ETag", etag)
		response.Header().Set("Content-Type", "application/json")
		if observation.ETag == etag {
			response.WriteHeader(http.StatusNotModified)
		} else {
			_, _ = response.Write(body)
		}
	}))
	// Registered before child cleanups: all children are cancelled and reaped
	// before the server is closed, even when an assertion fails at a barrier.
	t.Cleanup(func() {
		select {
		case <-harness.release:
		default:
			close(harness.release)
		}
		select {
		case <-harness.resumeB:
		default:
			close(harness.resumeB)
		}
		harness.server.Close()
	})
	return harness
}

func (harness *restLoaderServer) child(t *testing.T, worker string, pin bool) *restLoaderChild {
	t.Helper()
	return startRESTLoaderChild(t, restLoaderProcessConfig{
		Base: harness.server.URL, Root: harness.root, Loader: harness.loader, Worker: worker, PinOwnerHTTP: pin,
	})
}

func (harness *restLoaderServer) entry(t *testing.T, worker string) restLoaderRequest {
	t.Helper()
	select {
	case request := <-harness.entered:
		if request.Worker != worker {
			t.Fatalf("HTTP entrant = %q, want %q", request.Worker, worker)
		}
		return request
	case <-time.After(10 * time.Second):
		t.Fatalf("worker %s did not reach HTTP", worker)
	}
	return restLoaderRequest{}
}

func (harness *restLoaderServer) blocked(t *testing.T, children ...*restLoaderChild) {
	t.Helper()
	// Readiness and loading are pipe handshakes. This timer is an observation
	// window for forbidden HTTP, not a sleep used to order process starts.
	timer := time.NewTimer(350 * time.Millisecond)
	defer timer.Stop()
	select {
	case request := <-harness.entered:
		t.Fatalf("waiter entered HTTP before owner release: %+v", request)
	case <-timer.C:
	}
	for _, child := range children {
		select {
		case result := <-child.messages:
			t.Fatalf("waiter finished while owner held HTTP: %+v", result)
		case <-child.done:
			t.Fatal("waiter exited while owner held HTTP")
		default:
		}
	}
}

func (harness *restLoaderServer) success(t *testing.T, child *restLoaderChild) {
	t.Helper()
	result := child.message(t, "done", 15*time.Second)
	if result.Error != "" || !reflect.DeepEqual(result.Snapshot, harness.expected) {
		t.Fatalf("loader snapshot: error=%q got=%+v want=%+v", result.Error, result.Snapshot, harness.expected)
	}
	child.wait(t, false)
}

func (harness *restLoaderServer) ownerExited(t *testing.T) {
	t.Helper()
	select {
	case <-harness.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("owner HTTP handler did not leave after process termination")
	}
}

func (harness *restLoaderServer) disk(t *testing.T, populated bool) {
	t.Helper()
	// Independent envelope inspection, not cache.read/write or a private lock
	// shortcut. Missing entries are allowed only before a successful loader.
	paths, err := filepath.Glob(filepath.Join(harness.root, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if populated && len(paths) != len(harness.fixtures) {
		t.Fatalf("cache entry count = %d, want %d", len(paths), len(harness.fixtures))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var entry restCacheEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			t.Fatalf("partial cache envelope: %v", err)
		}
		matched := false
		for endpoint, body := range harness.fixtures {
			query := "page=1&per_page=100"
			if endpoint == harness.listPath {
				query = "direction=asc&page=1&per_page=100&sort=created&state=open"
			}
			if entry.URL == harness.server.URL+endpoint+"?"+query && bytes.Equal(entry.Body, body) {
				matched = true
			}
		}
		if entry.Version != cacheEntryVersion || entry.ETag == "" || !json.Valid(entry.Body) || !matched {
			t.Fatalf("invalid cache entry %s: %+v", path, entry)
		}
	}
	paths, err = filepath.Glob(filepath.Join(harness.root, ".pipelinehealth-cache-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("temporary cache files after loader: %v, %v", paths, err)
	}
}

func TestRESTCacheProcesses(t *testing.T) {
	for _, loader := range []string{"pulls", "issues"} {
		t.Run(loader, func(t *testing.T) {
			t.Parallel() // Both 45-second budgets run, including on Windows CI.
			t.Run("owner_waiter_contender", func(t *testing.T) {
				h := newRESTLoaderServer(t, loader, false)
				a := h.child(t, "A", false)
				a.load(t)
				if request := h.entry(t, "A"); request.ETag != "" {
					t.Fatal("cold owner supplied an ETag")
				}
				b, c := h.child(t, "B", false), h.child(t, "C", false)
				b.load(t)
				c.load(t)
				h.blocked(t, b, c)
				close(h.release)
				// Kernel locks do not promise FIFO: either waiter may win next.
				h.success(t, a)
				h.success(t, b)
				h.success(t, c)
				h.mu.Lock()
				requests := append([]restLoaderRequest(nil), h.requests...)
				h.mu.Unlock()
				counts, cold := make(map[string]int), make(map[string]int)
				for _, request := range requests {
					counts[request.Path]++
					if request.ETag == "" {
						cold[request.Path]++
					}
					if request.Path == h.listPath && request.Worker != "A" && request.ETag == "" {
						t.Fatalf("waiter did not observe owner's ETag: %+v", request)
					}
				}
				for endpoint := range h.fixtures {
					if counts[endpoint] != 3 || cold[endpoint] != 1 {
						t.Fatalf("endpoint %s: requests=%d cold=%d, want 3 and 1", endpoint, counts[endpoint], cold[endpoint])
					}
				}
				h.disk(t, true)
			})
			for _, warm := range []bool{false, true} {
				t.Run(fmt.Sprintf("killed_owner_warm_%t", warm), func(t *testing.T) {
					h := newRESTLoaderServer(t, loader, true)
					if warm {
						seed := h.child(t, "seed", false)
						seed.load(t)
						h.entry(t, "seed")
						h.success(t, seed)
					}
					a := h.child(t, "A", false)
					a.load(t)
					h.entry(t, "A")
					b := h.child(t, "B", false)
					b.load(t)
					h.blocked(t, b)
					a.kill(t)
					h.ownerExited(t)
					if request := h.entry(t, "B"); (request.ETag != "") != warm {
						t.Fatalf("waiter ETag after owner death: %+v, warm=%t", request, warm)
					}
					h.disk(t, warm)
					close(h.resumeB)
					h.success(t, b)
					h.disk(t, true)
				})
			}
			t.Run("killed_waiter", func(t *testing.T) {
				h := newRESTLoaderServer(t, loader, false)
				a := h.child(t, "A", false)
				a.load(t)
				h.entry(t, "A")
				b := h.child(t, "B", false)
				b.load(t)
				h.blocked(t, b)
				b.kill(t)
				c := h.child(t, "C", false)
				c.load(t)
				h.blocked(t, c)
				close(h.release)
				h.success(t, a)
				if request := h.entry(t, "C"); request.ETag == "" {
					t.Fatal("surviving waiter did not send owner's ETag")
				}
				h.success(t, c)
				h.disk(t, true)
			})
			t.Run("waiter_45_second_budget", func(t *testing.T) {
				h := newRESTLoaderServer(t, loader, false)
				a := h.child(t, "A", true)
				a.load(t)
				h.entry(t, "A")
				b := h.child(t, "B", false)
				started := time.Now()
				b.load(t)
				result := b.message(t, "done", 60*time.Second)
				elapsed := time.Since(started)
				if !strings.Contains(result.Error, "lock REST cache") || !strings.Contains(result.Error, "context deadline exceeded") ||
					elapsed < 44*time.Second || elapsed > 60*time.Second || !reflect.DeepEqual(result.Snapshot, restLoaderSnapshot{}) {
					t.Fatalf("waiter budget: elapsed=%s result=%+v", elapsed, result)
				}
				b.wait(t, false)
				h.blocked(t)
				a.kill(t)
				h.ownerExited(t)
				h.disk(t, false)
				c := h.child(t, "C", false)
				c.load(t)
				if request := h.entry(t, "C"); request.ETag != "" {
					t.Fatal("timed-out waiter persisted an entry")
				}
				h.success(t, c)
				h.disk(t, true)
			})
		})
	}
}
