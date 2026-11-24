package launcher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http/httpproxy"
)

func TestClientHealthEnvironmentPrecedence(t *testing.T) {
	for _, tc := range []struct {
		addr   string
		config httpproxy.Config
		goHTTP bool
	}{
		{"https://base.example:8448", httpproxy.Config{}, false},
		{"https://base.example:8448", httpproxy.Config{HTTPSProxy: "http://proxy.example"}, true},
		{"https://base.example:8448", httpproxy.Config{HTTPProxy: "http://proxy.example"}, false},
		{"https://127.0.0.1:8448", httpproxy.Config{}, true},
		{"https://192.0.2.62:8448", httpproxy.Config{NoProxy: "192.0.2.0/24"}, true},
		{"https://base.example:8448", httpproxy.Config{NoProxy: "base.example:8443"}, false},
	} {
		u, err := url.Parse(tc.addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := healthUsesEnvironment(u, tc.config); got != tc.goHTTP {
			t.Errorf("healthUsesEnvironment(%s, %+v) = %t, want %t", tc.addr, tc.config, got, tc.goHTTP)
		}
	}
}

func TestClientHealthRetriesOnce(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/health" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if issue := probeClientServer(t.Context(), &Base{ServerURL: server.URL}, server.Client()); issue != "" || attempts.Load() != 2 {
		t.Fatalf("issue %q, attempts %d", issue, attempts.Load())
	}

	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer tlsServer.Close()
	if issue := probeClientServer(t.Context(), &Base{ServerURL: tlsServer.URL}, &http.Client{}); issue != probeCert {
		t.Fatalf("untrusted certificate got %q", issue)
	}

	ctx, cancel := context.WithDeadline(t.Context(), time.Now())
	defer cancel()
	if issue := probeClientServer(ctx, &Base{ServerURL: server.URL}, server.Client()); issue != probeTimeout {
		t.Fatalf("expired deadline got %q", issue)
	}
}

func TestClientHealthLauncherDoesNotWaitForRemote(t *testing.T) {
	savedBundle := launcherBundle
	t.Cleanup(func() { launcherBundle = savedBundle })
	unblock := make(chan struct{})
	var attempts atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-unblock:
		case <-r.Context().Done():
			return
		}
		attempts.Add(1)
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	t.Cleanup(remote.Close)
	released := false
	t.Cleanup(func() {
		if !released {
			close(unblock)
		}
	})
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base, err := NewClientBase("Проверка", remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(store, NewRunner())
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Timeout: 1500 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	t.Cleanup(func() {
		httpClient.CloseIdleConnections()
		srv.Close()
		if err := <-done; err != nil && err != http.ErrServerClosed {
			t.Error(err)
		}
	})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("launcher waited for the remote probe: %v", err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK || !strings.Contains(string(body), "Проверяем подключение") {
		t.Fatalf("initial launcher status %d: %v", res.StatusCode, err)
	}
	close(unblock)
	released = true
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL()+"/bases/"+base.ID+"/client-status", nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			Pending bool   `json:"pending"`
			Text    string `json:"text"`
		}
		err = json.NewDecoder(res.Body).Decode(&status)
		_ = res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("poll status %d: %v", res.StatusCode, err)
		}
		if !status.Pending {
			if status.Text != string(probeProxyAuth) || attempts.Load() != 2 {
				t.Fatalf("status %q, %d attempts", status.Text, attempts.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("health status did not settle")
}
