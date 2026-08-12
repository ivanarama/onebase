package processcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testControlServer(t *testing.T, secret, baseID, instance string, pid int) (*httptest.Server, int, *atomic.Bool) {
	t.Helper()
	var mu sync.Mutex
	identityPeer := ""
	var stopped atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/process/identity", func(w http.ResponseWriter, r *http.Request) {
		challenge := r.URL.Query().Get(ChallengeQuery)
		mu.Lock()
		identityPeer = r.RemoteAddr
		mu.Unlock()
		got := Identity{BaseID: baseID, PID: pid, Instance: instance}
		got.Proof = IdentityProof(secret, baseID, pid, instance, challenge)
		_ = json.NewEncoder(w).Encode(got)
	})
	mux.HandleFunc("/debug/process/stop", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		nonce := r.Header.Get(HeaderNonce)
		want := StopProof(secret, baseID, instance, nonce)
		if r.RemoteAddr != identityPeer || r.Header.Get(HeaderBaseID) != baseID ||
			r.Header.Get(HeaderInstance) != instance || !ValidNonce(nonce) ||
			!Verify(r.Header.Get(HeaderProof), want) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		stopped.Store(true)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, portText, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return srv, port, &stopped
}

func TestProbeIdentityRequiresExactPIDAndProof(t *testing.T) {
	const secret, baseID, instance = "secret", "dev-base", "instance"
	_, port, _ := testControlServer(t, secret, baseID, instance, 4242)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := ProbeIdentity(ctx, port, secret, baseID, 4242); err != nil {
		t.Fatalf("valid identity: %v", err)
	}
	if _, err := ProbeIdentity(ctx, port, secret, baseID, 4243); err == nil {
		t.Fatal("foreign PID was accepted")
	}
	if _, err := ProbeIdentity(ctx, port, "wrong-secret", baseID, 4242); err == nil {
		t.Fatal("invalid HMAC was accepted")
	}
}

func TestRequestStopUsesAuthenticatedConnection(t *testing.T) {
	const secret, baseID, instance = "secret", "dev-base", "instance"
	_, port, stopped := testControlServer(t, secret, baseID, instance, 4242)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := RequestStop(ctx, port, secret, baseID, 4242); err != nil {
		t.Fatalf("RequestStop: %v", err)
	}
	if !stopped.Load() {
		t.Fatal("signed stop was not received")
	}
	if err := RequestStop(ctx, port, secret, baseID, 7); err == nil {
		t.Fatal("stop accepted mismatched tracked PID")
	}
}

func TestControlEndpointIsLoopbackOnly(t *testing.T) {
	if got, want := controlEndpoint(8123, "/x"), "http://127.0.0.1:8123/x"; got != want {
		t.Fatalf("control endpoint = %q, want %q", got, want)
	}
	if _, err := url.ParseRequestURI(controlEndpoint(8123, "/x")); err != nil {
		t.Fatal(fmt.Errorf("control endpoint URL: %w", err))
	}
}
