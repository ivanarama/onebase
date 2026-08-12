package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ivantit66/onebase/internal/processcontrol"
)

func processControlRequest(s *Server, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestProcessControl_UsesChallengeProofAndInstanceBoundStop(t *testing.T) {
	const secret = "launcher-control-secret"
	t.Setenv("ONEBASE_DEBUG_TOKEN", "ephemeral-debug-secret")
	t.Setenv("ONEBASE_CONTROL_TOKEN", secret)
	t.Setenv("ONEBASE_BASE_ID", "base-1")
	srv := newDebugTestServer(t)

	if rec := processControlRequest(srv, http.MethodGet, "/debug/process/identity", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("identity without challenge: %d", rec.Code)
	}
	challenge, err := processcontrol.NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	rec := processControlRequest(srv, http.MethodGet,
		"/debug/process/identity?"+processcontrol.ChallengeQuery+"="+challenge, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("identity: %d: %s", rec.Code, rec.Body.String())
	}
	var identity processcontrol.Identity
	if err := json.Unmarshal(rec.Body.Bytes(), &identity); err != nil {
		t.Fatalf("identity JSON: %v", err)
	}
	wantProof := processcontrol.IdentityProof(secret, identity.BaseID, identity.PID, identity.Instance, challenge)
	if identity.BaseID != "base-1" || identity.PID <= 0 || identity.Instance == "" ||
		!processcontrol.Verify(identity.Proof, wantProof) {
		t.Fatalf("invalid identity: %+v", identity)
	}

	nonce, err := processcontrol.NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{
		processcontrol.HeaderBaseID:   "base-1",
		processcontrol.HeaderInstance: "wrong-instance",
		processcontrol.HeaderNonce:    nonce,
		processcontrol.HeaderProof: processcontrol.StopProof(secret, "base-1",
			"wrong-instance", nonce),
	}
	if rec := processControlRequest(srv, http.MethodPost, "/debug/process/stop", headers); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stop for another instance: %d", rec.Code)
	}
	select {
	case <-srv.Done():
		t.Fatal("unauthorized stop closed Done")
	default:
	}

	headers[processcontrol.HeaderInstance] = identity.Instance
	headers[processcontrol.HeaderProof] = processcontrol.StopProof(secret, "base-1", identity.Instance, nonce)
	rec = processControlRequest(srv, http.MethodPost, "/debug/process/stop", headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("stop: %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case <-srv.Done():
	default:
		t.Fatal("authorized stop did not close Done")
	}
}

func TestProcessControl_NotMountedWithoutControlSecret(t *testing.T) {
	t.Setenv("ONEBASE_DEBUG_TOKEN", "debug-only")
	t.Setenv("ONEBASE_CONTROL_TOKEN", "")
	t.Setenv("ONEBASE_BASE_ID", "base-1")
	srv := newDebugTestServer(t)
	rec := processControlRequest(srv, http.MethodPost, "/debug/process/stop", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("process control must not be mounted: %d", rec.Code)
	}
}
