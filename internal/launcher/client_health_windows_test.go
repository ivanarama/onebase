package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"syscall"
	"testing"
	"time"
)

func TestClientHealthWinHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/health" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	u, err := url.Parse(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	status, err, supported := systemClientHealth(ctx, u)
	if !supported || status != http.StatusServiceUnavailable || err != nil {
		t.Fatalf("status %d, err %v, WinHTTP supported %t", status, err, supported)
	}
	for errno, expected := range map[syscall.Errno]clientProbeIssue{
		12002: probeTimeout,
		12038: probeCert,
		12175: probeTLS,
		12167: probeProxy,
	} {
		if got := winHealthError(errno); got != expected {
			t.Errorf("WinHTTP error %d got %q, want %q", errno, got, expected)
		}
	}
}
