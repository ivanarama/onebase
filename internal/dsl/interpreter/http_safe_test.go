package interpreter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSafeHTTPPubliclyRoutableIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},
		{"2001:4860:4860::8888", true},
		{"0.0.0.0", false},
		{"10.0.0.1", false},
		{"100.64.0.1", false},
		{"127.0.0.1", false},
		{"168.63.129.16", false},
		{"169.254.169.254", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"192.0.2.1", false},
		{"198.18.0.1", false},
		{"198.51.100.1", false},
		{"203.0.113.1", false},
		{"224.0.0.1", false},
		{"240.0.0.1", false},
		{"::", false},
		{"::1", false},
		{"::c0a8:101", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:0:c0a8:101", false},
		{"64:ff9b::c0a8:1", false},
		{"100::1", false},
		{"100:0:0:1::1", false},
		{"2001:db8::1", false},
		{"2002:0a00:0001::1", false},
		{"3ffe::1", false},
		{"fc00::1", false},
		{"fec0::1", false},
		{"fe80::1", false},
		{"ff02::1", false},
		{"4000::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := isPubliclyRoutableIP(netip.MustParseAddr(tt.ip)); got != tt.want {
				t.Fatalf("isPubliclyRoutableIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestSafeHTTPPolicyURLBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, rawURL, source string
		want                 bool
	}{
		{"http default", "http://images.example/a.png", "images.example", true},
		{"https default", "https://IMAGES.EXAMPLE/a.png", "images.example", true},
		{"trailing dot", "https://images.example./a.png", "images.example", true},
		{"unicode IDN", "https://пример.рф/a.png", "xn--e1afmkfd.xn--p1ai", true},
		{"explicit port", "https://images.example:8443/a.png", "images.example:8443", true},
		{"wrong port", "https://images.example:8443/a.png", "images.example", false},
		{"source port is exact", "https://images.example/a.png", "images.example:8443", false},
		{"subdomain", "https://cdn.images.example/a.png", "images.example", false},
		{"userinfo", "https://images.example@127.0.0.1/a.png", "images.example", false},
		{"other scheme", "file:///etc/passwd", "images.example", false},
		{"empty policy", "https://images.example/a.png", "", false},
		{"loopback literal", "http://127.0.0.1/a.png", "127.0.0.1", false},
		{"private literal", "http://10.0.0.1/a.png", "10.0.0.1", false},
		{"azure host endpoint", "http://168.63.129.16/a.png", "168.63.129.16", false},
		{"IPv4 compatible", "http://[::c0a8:101]/a.png", "[::c0a8:101]", false},
		{"mapped loopback", "http://[::ffff:127.0.0.1]/a.png", "::ffff:127.0.0.1", false},
		{"IPv4 translatable", "http://[::ffff:0:c0a8:101]/a.png", "[::ffff:0:c0a8:101]", false},
		{"deprecated site local", "http://[fec0::1]/a.png", "[fec0::1]", false},
		{"deprecated 6bone", "http://[3ffe::1]/a.png", "[3ffe::1]", false},
		{"dummy IPv6", "http://[100:0:0:1::1]/a.png", "[100:0:0:1::1]", false},
		{"localhost", "http://localhost/a.png", "localhost", false},
		{"public IPv6", "https://[2606:4700:4700::1111]:8443/a.png", "[2606:4700:4700::1111]:8443", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeHTTPURLAllowed(tt.rawURL, tt.source); got != tt.want {
				t.Fatalf("safeHTTPURLAllowed(%q, %q) = %v, want %v", tt.rawURL, tt.source, got, tt.want)
			}
		})
	}
}

type sequenceSafeResolver struct {
	mu      sync.Mutex
	answers [][]net.IPAddr
	calls   int
}

func (r *sequenceSafeResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.calls
	r.calls++
	if idx >= len(r.answers) {
		idx = len(r.answers) - 1
	}
	return append([]net.IPAddr(nil), r.answers[idx]...), nil
}

func ipAnswers(values ...string) []net.IPAddr {
	out := make([]net.IPAddr, 0, len(values))
	for _, value := range values {
		out = append(out, net.IPAddr{IP: net.ParseIP(value)})
	}
	return out
}

func TestSafeHTTPDialPinsValidatedAddress(t *testing.T) {
	t.Parallel()
	policy, err := parseSafeHTTPPolicy("images.example")
	if err != nil {
		t.Fatal(err)
	}
	resolver := &sequenceSafeResolver{answers: [][]net.IPAddr{ipAnswers("8.8.8.8")}}
	var dialed string
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		client, server := net.Pipe()
		go server.Close()
		return client, nil
	}
	d := safeHTTPDialer{policy: policy, resolver: resolver, dial: dial}
	conn, err := d.DialContext(context.Background(), "tcp", "images.example:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if dialed != "8.8.8.8:443" {
		t.Fatalf("actual dial = %q, want validated numeric address", dialed)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want one (no resolve/dial TOCTOU)", resolver.calls)
	}
}

func TestSafeHTTPDialRejectsAnyNonPublicDNSAnswer(t *testing.T) {
	t.Parallel()
	policy, err := parseSafeHTTPPolicy("images.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, answers := range [][]net.IPAddr{
		ipAnswers("127.0.0.1"),
		ipAnswers("168.63.129.16"),
		ipAnswers("::1"),
		ipAnswers("::c0a8:101"),
		ipAnswers("::ffff:0:c0a8:101"),
		ipAnswers("fec0::1"),
		ipAnswers("8.8.8.8", "10.0.0.1"),
	} {
		dialed := false
		d := safeHTTPDialer{
			policy:   policy,
			resolver: &sequenceSafeResolver{answers: [][]net.IPAddr{answers}},
			dial: func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, fmt.Errorf("must not dial")
			},
		}
		if _, err := d.DialContext(context.Background(), "tcp", "images.example:80"); err == nil {
			t.Fatalf("DNS answers %v were accepted", answers)
		}
		if dialed {
			t.Fatalf("DNS answers %v reached dial", answers)
		}
	}
}

func TestSafeHTTPClientDoesNotInheritDefaultTransportTLSHooks(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("process-global TLS dialer must not be inherited")
		},
	}
	t.Cleanup(func() { http.DefaultTransport = original })

	policy, err := parseSafeHTTPPolicy("images.example")
	if err != nil {
		t.Fatal(err)
	}
	client, transport := newSafeHTTPClient(policy,
		&sequenceSafeResolver{answers: [][]net.IPAddr{ipAnswers("8.8.8.8")}},
		func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("test dial")
		}, time.Second)
	t.Cleanup(transport.CloseIdleConnections)
	if client.Transport != transport {
		t.Fatal("safe client does not use its private transport")
	}
	if transport.DialContext == nil {
		t.Fatal("safe transport has no validated DialContext")
	}
	if transport.DialTLS != nil || transport.DialTLSContext != nil {
		t.Fatal("safe transport inherited a TLS dial hook which can bypass validated DialContext")
	}
	if transport.Proxy != nil {
		t.Fatal("safe transport must not inherit an environment proxy")
	}
}

func safeHTTPTestClient(t *testing.T, srv *httptest.Server, resolver safeHTTPResolver) (*http.Client, safeHTTPPolicy, *int) {
	t.Helper()
	port := strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)
	policy, err := parseSafeHTTPPolicy("images.example:" + port)
	if err != nil {
		t.Fatal(err)
	}
	dials := 0
	realDialer := &net.Dialer{Timeout: time.Second}
	dial := func(ctx context.Context, network, checkedAddress string) (net.Conn, error) {
		dials++
		if !strings.HasPrefix(checkedAddress, "8.8.8.8:") {
			return nil, fmt.Errorf("transport tried unpinned address %q", checkedAddress)
		}
		return realDialer.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	client, transport := newSafeHTTPClient(policy, resolver, dial, 5*time.Second)
	t.Cleanup(transport.CloseIdleConnections)
	return client, policy, &dials
}

func TestSafeHTTPRedirectRechecksHostAllowlist(t *testing.T) {
	var target string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	client, _, _ := safeHTTPTestClient(t, srv,
		&sequenceSafeResolver{answers: [][]net.IPAddr{ipAnswers("8.8.8.8")}})
	port := strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)

	for _, redirect := range []string{
		"http://127.0.0.1:" + port + "/private",
		"http://evil.example:" + port + "/other-host",
	} {
		target = redirect
		_, err := client.Get("http://images.example:" + port + "/start")
		if err == nil || !strings.Contains(err.Error(), "перенаправление запрещено") {
			t.Fatalf("redirect %q error = %v", redirect, err)
		}
	}
}

func TestSafeHTTPRedirectRedialRejectsDNSRebinding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("Connection", "close") // force a fresh validated dial for the redirect
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("should not be reached"))
	}))
	defer srv.Close()
	resolver := &sequenceSafeResolver{answers: [][]net.IPAddr{
		ipAnswers("8.8.8.8"),
		ipAnswers("127.0.0.1"),
	}}
	client, _, dials := safeHTTPTestClient(t, srv, resolver)
	port := strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)

	_, err := client.Get("http://images.example:" + port + "/start")
	if err == nil || !strings.Contains(err.Error(), "непубличный IP") {
		t.Fatalf("DNS rebinding error = %v", err)
	}
	if resolver.calls < 2 {
		t.Fatalf("redirect did not trigger a second DNS validation: %d calls", resolver.calls)
	}
	if *dials != 1 {
		t.Fatalf("actual dials = %d, private rebound address must not reach dial", *dials)
	}
}
