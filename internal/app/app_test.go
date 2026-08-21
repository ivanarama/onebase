package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestBuildDoesNotOpenListener(t *testing.T) {
	t.Setenv("ONEBASE_DEBUG_TOKEN", "")
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	application, err := Build(context.Background(), Config{
		Registry: runtime.NewRegistry(), Store: db, Interpreter: interpreter.New(),
		AuthRepo: auth.NewRepo(db), Host: "127.0.0.1", Port: port,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("Build opened the application port: %v", err)
	}
	_ = listener.Close()
}

type lifecycleRecorder struct {
	events chan string
}

func (r *lifecycleRecorder) add(event string) { r.events <- event }

type fakeHTTPRuntime struct {
	recorder *lifecycleRecorder
	stop     chan struct{}
}

func (f *fakeHTTPRuntime) Listen() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
func (f *fakeHTTPRuntime) Serve(listener net.Listener) error {
	<-f.stop
	_ = listener.Close()
	return http.ErrServerClosed
}
func (f *fakeHTTPRuntime) Shutdown(context.Context) error {
	f.recorder.add("http")
	close(f.stop)
	return nil
}
func (f *fakeHTTPRuntime) ResyncWSIntakes() {}

type fakeSchedulerRuntime struct{ recorder *lifecycleRecorder }

func (f *fakeSchedulerRuntime) RunReady(ctx context.Context, ready chan<- struct{}) error {
	close(ready)
	<-ctx.Done()
	f.recorder.add("scheduler")
	return nil
}
func (f *fakeSchedulerRuntime) BeginQuiesce() { f.recorder.add("quiesce") }

type fakeQueueRuntime struct{ recorder *lifecycleRecorder }

func (f *fakeQueueRuntime) Run(ctx context.Context) error {
	<-ctx.Done()
	f.recorder.add("queue")
	return nil
}

func TestCloseOrder(t *testing.T) {
	recorder := &lifecycleRecorder{events: make(chan string, 8)}
	httpRuntime := &fakeHTTPRuntime{recorder: recorder, stop: make(chan struct{})}
	application := &Application{
		httpRuntime:  httpRuntime,
		scheduler:    &fakeSchedulerRuntime{recorder: recorder},
		queue:        &fakeQueueRuntime{recorder: recorder},
		serveStopped: make(chan struct{}),
	}
	application.SetBeforeDrain(func() { recorder.add("watcher") })
	if err := application.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := application.Close(ctx); err != nil {
		t.Fatal(err)
	}

	events := make([]string, 0, 5)
	for len(recorder.events) > 0 {
		events = append(events, <-recorder.events)
	}
	index := func(want string) int {
		for i, event := range events {
			if event == want {
				return i
			}
		}
		return -1
	}
	if index("quiesce") != 0 || index("watcher") != 1 {
		t.Fatalf("close must quiesce then stop producers, got %v", events)
	}
	httpIndex := index("http")
	if httpIndex < 0 || index("scheduler") < 0 || index("queue") < 0 ||
		index("scheduler") > httpIndex || index("queue") > httpIndex {
		t.Fatalf("workers must drain before HTTP/frontend shutdown, got %v", events)
	}
}
