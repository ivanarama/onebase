package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type cacheTestPayload struct {
	Value string `json:"value"`
}

func testRESTClient(t *testing.T, server *httptest.Server) *githubRESTClient {
	t.Helper()
	client, err := newGitHubRESTClientWithBase(server.URL, "test-token", t.TempDir(), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func requireTestAuthentication(response http.ResponseWriter, request *http.Request) bool {
	if request.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(response, "missing authentication", http.StatusUnauthorized)
		return false
	}
	return true
}

func TestRESTCacheReusesBodyOnlyAfterExplicit304(t *testing.T) {
	const body = "{\n  \"value\": \"first\"\n}\n"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !requireTestAuthentication(response, request) {
			return
		}
		switch calls.Add(1) {
		case 1:
			if request.Header.Get("If-None-Match") != "" {
				http.Error(response, "unexpected conditional header", http.StatusBadRequest)
				return
			}
			response.Header().Set("ETag", `"v1"`)
			_, _ = io.WriteString(response, body)
		case 2:
			if request.Header.Get("If-None-Match") != `"v1"` {
				http.Error(response, "wrong conditional header", http.StatusBadRequest)
				return
			}
			response.WriteHeader(http.StatusNotModified)
		default:
			http.Error(response, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	client := testRESTClient(t, server)

	for range 2 {
		var got cacheTestPayload
		if err := client.getJSON("resource", &got); err != nil {
			t.Fatal(err)
		}
		if got.Value != "first" {
			t.Fatalf("value = %q, want first", got.Value)
		}
	}

	requestURL, err := client.endpointURL("resource")
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := client.cache.read(requestURL)
	if !ok {
		t.Fatal("cache entry was not persisted")
	}
	if !bytes.Equal(entry.Body, []byte(body)) {
		t.Fatalf("cached body was changed: %q", entry.Body)
	}
}

func TestRESTCacheReplacesChanged200Response(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !requireTestAuthentication(response, request) {
			return
		}
		switch calls.Add(1) {
		case 1:
			response.Header().Set("ETag", `"v1"`)
			_, _ = io.WriteString(response, `{"value":"first"}`)
		case 2:
			if request.Header.Get("If-None-Match") != `"v1"` {
				http.Error(response, "wrong v1 conditional header", http.StatusBadRequest)
				return
			}
			response.Header().Set("ETag", `"v2"`)
			_, _ = io.WriteString(response, `{"value":"second"}`)
		case 3:
			if request.Header.Get("If-None-Match") != `"v2"` {
				http.Error(response, "wrong v2 conditional header", http.StatusBadRequest)
				return
			}
			response.WriteHeader(http.StatusNotModified)
		default:
			http.Error(response, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	client := testRESTClient(t, server)

	wants := []string{"first", "second", "second"}
	for _, want := range wants {
		var got cacheTestPayload
		if err := client.getJSON("resource", &got); err != nil {
			t.Fatal(err)
		}
		if got.Value != want {
			t.Fatalf("value = %q, want %q", got.Value, want)
		}
	}
}

func TestRESTCacheKeyIsIsolatedByAuthenticationScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("ETag", `"stable"`)
		_, _ = io.WriteString(response, `{"value":"ok"}`)
	}))
	defer server.Close()
	cacheDir := t.TempDir()
	first, err := newGitHubRESTClientWithBase(server.URL, "first-secret-token", cacheDir, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	second, err := newGitHubRESTClientWithBase(server.URL, "second-secret-token", cacheDir, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	requestURL, err := first.endpointURL("resource")
	if err != nil {
		t.Fatal(err)
	}
	firstPath := first.cache.entryPath(requestURL)
	secondPath := second.cache.entryPath(requestURL)
	if firstPath == secondPath {
		t.Fatal("different authentication scopes share one cache key")
	}
	for _, path := range []string{firstPath, secondPath} {
		if strings.Contains(path, "secret-token") {
			t.Fatalf("cache path exposes authentication material: %s", path)
		}
	}
}

func TestRESTCacheMissesForMissingOrCorruptEntries(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, client *githubRESTClient, requestURL string)
	}{
		{name: "missing", prepare: func(*testing.T, *githubRESTClient, string) {}},
		{
			name: "corrupt envelope",
			prepare: func(t *testing.T, client *githubRESTClient, requestURL string) {
				t.Helper()
				if err := os.WriteFile(client.cache.entryPath(requestURL), []byte("not-json"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt body",
			prepare: func(t *testing.T, client *githubRESTClient, requestURL string) {
				t.Helper()
				if err := client.cache.write(requestURL, restCacheEntry{
					Version: cacheEntryVersion,
					URL:     requestURL,
					ETag:    `"broken"`,
					Body:    []byte("{"),
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Header.Get("If-None-Match") != "" {
					http.Error(response, "corrupt cache supplied an ETag", http.StatusBadRequest)
					return
				}
				response.Header().Set("ETag", `"fresh"`)
				_, _ = io.WriteString(response, `{"value":"fresh"}`)
			}))
			defer server.Close()
			client := testRESTClient(t, server)
			requestURL, err := client.endpointURL("resource")
			if err != nil {
				t.Fatal(err)
			}
			test.prepare(t, client, requestURL)

			var got cacheTestPayload
			if err := client.getJSON("resource", &got); err != nil {
				t.Fatal(err)
			}
			if got.Value != "fresh" {
				t.Fatalf("value = %q, want fresh", got.Value)
			}
		})
	}
}

func TestRESTCacheDoesNotFallbackOnRequestFailure(t *testing.T) {
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if fail.Load() {
			http.Error(response, "GitHub unavailable", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("ETag", `"v1"`)
		_, _ = io.WriteString(response, `{"value":"cached"}`)
	}))
	defer server.Close()
	client := testRESTClient(t, server)

	var seeded cacheTestPayload
	if err := client.getJSON("resource", &seeded); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	got := cacheTestPayload{Value: "sentinel"}
	err := client.getJSON("resource", &got)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %v, want HTTP 503", err)
	}
	if got.Value != "sentinel" {
		t.Fatalf("stale cache escaped into destination: %q", got.Value)
	}
}

func TestRESTCacheDoesNotPersistOrReturnMalformed200(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch calls.Add(1) {
		case 1:
			response.Header().Set("ETag", `"v1"`)
			_, _ = io.WriteString(response, `{"value":"cached"}`)
		case 2:
			response.Header().Set("ETag", `"v2"`)
			_, _ = io.WriteString(response, `{"value":`)
		case 3:
			if request.Header.Get("If-None-Match") != `"v1"` {
				http.Error(response, "malformed response replaced the cache", http.StatusBadRequest)
				return
			}
			response.Header().Set("ETag", `"v3"`)
			_, _ = io.WriteString(response, `{"value":"recovered"}`)
		}
	}))
	defer server.Close()
	client := testRESTClient(t, server)

	var seeded cacheTestPayload
	if err := client.getJSON("resource", &seeded); err != nil {
		t.Fatal(err)
	}
	malformed := cacheTestPayload{Value: "sentinel"}
	if err := client.getJSON("resource", &malformed); err == nil {
		t.Fatal("malformed HTTP 200 unexpectedly succeeded")
	}
	if malformed.Value != "sentinel" {
		t.Fatalf("malformed response changed destination to %q", malformed.Value)
	}
	var recovered cacheTestPayload
	if err := client.getJSON("resource", &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Value != "recovered" {
		t.Fatalf("value = %q, want recovered", recovered.Value)
	}
}

func TestRESTCacheRejects304WithoutValidEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	client := testRESTClient(t, server)
	got := cacheTestPayload{Value: "sentinel"}
	if err := client.getJSON("resource", &got); err == nil || !strings.Contains(err.Error(), "without a valid cache entry") {
		t.Fatalf("error = %v, want invalid 304 error", err)
	}
	if got.Value != "sentinel" {
		t.Fatalf("destination changed to %q", got.Value)
	}
}

func TestRESTCacheSerializesConcurrentWriters(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			seen := maximum.Load()
			if current <= seen || maximum.CompareAndSwap(seen, current) {
				break
			}
		}
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		if request.Header.Get("If-None-Match") == `"stable"` {
			response.WriteHeader(http.StatusNotModified)
			return
		}
		response.Header().Set("ETag", `"stable"`)
		_, _ = io.WriteString(response, `{"value":"shared"}`)
	}))
	defer server.Close()
	client := testRESTClient(t, server)

	const workers = 12
	errorsByWorker := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var got cacheTestPayload
			if err := client.getJSON("resource", &got); err != nil {
				errorsByWorker <- err
				return
			}
			if got.Value != "shared" {
				errorsByWorker <- fmt.Errorf("value = %q", got.Value)
			}
		}()
	}
	wait.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		t.Error(err)
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent requests = %d, want 1", maximum.Load())
	}
	if calls.Load() != workers {
		t.Fatalf("requests = %d, want %d", calls.Load(), workers)
	}

	requestURL, err := client.endpointURL("resource")
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := client.cache.read(requestURL)
	if !ok || !bytes.Equal(entry.Body, []byte(`{"value":"shared"}`)) {
		t.Fatalf("final cache entry is invalid: ok=%v body=%q", ok, entry.Body)
	}
	files, err := filepath.Glob(filepath.Join(client.cache.dir, ".pipelinehealth-cache-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("temporary cache files remain: %v", files)
	}
}

func TestFixtureLoadingDoesNotRequireRESTClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulls.json")
	if err := os.WriteFile(path, []byte(`[{"number":7,"state":"open"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	pulls, err := loadPulls(nil, "ignored", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pulls) != 1 || pulls[0].Number != 7 {
		t.Fatalf("pulls = %#v", pulls)
	}
	issues, err := loadIssues(nil, "ignored", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("issues = %#v, want empty offline set", issues)
	}
}
