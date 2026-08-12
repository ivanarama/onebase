package processcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
)

func controlEndpoint(port int, path string) string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + path
}

func localClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func requestIdentity(ctx context.Context, client *http.Client, port int, secret, baseID string, expectedPID int) (Identity, error) {
	if port <= 0 || secret == "" || baseID == "" {
		return Identity{}, errors.New("incomplete process-control identity")
	}
	challenge, err := NewNonce()
	if err != nil {
		return Identity{}, err
	}
	query := (url.Values{ChallengeQuery: []string{challenge}}).Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		controlEndpoint(port, "/debug/process/identity?")+query, nil)
	if err != nil {
		return Identity{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Identity{}, err
	}
	defer resp.Body.Close() //nolint:errcheck // status and bounded JSON determine the probe result
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("identity HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return Identity{}, err
	}
	var got Identity
	if err := json.Unmarshal(data, &got); err != nil {
		return Identity{}, err
	}
	want := IdentityProof(secret, got.BaseID, got.PID, got.Instance, challenge)
	if got.BaseID != baseID || got.PID <= 0 || got.Instance == "" || !Verify(got.Proof, want) {
		return Identity{}, errors.New("identity proof mismatch")
	}
	if expectedPID > 0 && got.PID != expectedPID {
		return Identity{}, fmt.Errorf("identity PID %d does not match tracked PID %d", got.PID, expectedPID)
	}
	return got, nil
}

// ProbeIdentity proves that the listener belongs to the exact tracked child,
// rather than accepting a generic health response from any process on the port.
func ProbeIdentity(ctx context.Context, port int, secret, baseID string, expectedPID int) (Identity, error) {
	return requestIdentity(ctx, localClient(nil), port, secret, baseID, expectedPID)
}

// RequestStop authenticates the child and sends the signed stop command over
// the same TCP connection. The transport refuses to redial, so a process that
// races to reoccupy the port cannot receive the follow-up credential.
func RequestStop(ctx context.Context, port int, secret, baseID string, expectedPID int) error {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	var dialMu sync.Mutex
	used := false
	transport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dialMu.Lock()
			defer dialMu.Unlock()
			if used {
				return nil, errors.New("authenticated control connection is closed")
			}
			used = true
			return conn, nil
		},
		MaxConnsPerHost: 1,
	}
	client := localClient(transport)
	defer func() {
		transport.CloseIdleConnections()
		_ = conn.Close()
	}()

	identity, err := requestIdentity(ctx, client, port, secret, baseID, expectedPID)
	if err != nil {
		return err
	}
	nonce, err := NewNonce()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		controlEndpoint(port, "/debug/process/stop"), nil)
	if err != nil {
		return err
	}
	req.Header.Set(HeaderBaseID, baseID)
	req.Header.Set(HeaderInstance, identity.Instance)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderProof, StopProof(secret, baseID, identity.Instance, nonce))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // empty acknowledgement; status is authoritative
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stop HTTP %d", resp.StatusCode)
	}
	return nil
}
