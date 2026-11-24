package auth

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestLinuxKerberosRejectsLoginWhenVerifierUnavailable(t *testing.T) {
	t.Setenv("ONEBASE_KRB5_REALM", "EXAMPLE.ORG")
	t.Setenv("ONEBASE_KRB5_KEYTAB", filepath.Join(t.TempDir(), "missing.keytab"))
	h := Handlers{WindowsSPN: "HTTP/erp.example.org"}
	request := func(ctx context.Context) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "https://erp.example.org/auth/windows", nil).WithContext(ctx)
		req.Header.Set("X-OneBase-Windows", "1")
		req.Header.Set("Authorization", "Negotiate "+base64.StdEncoding.EncodeToString([]byte("test token")))
		rec := httptest.NewRecorder()
		h.WindowsLogin(rec, req)
		if rec.Code != http.StatusForbidden || rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("WWW-Authenticate") != "" {
			t.Fatalf("failed verification returned HTTP %d, headers %v", rec.Code, rec.Header())
		}
	}
	request(t.Context())
	if len(kerberosAcceptSlots) != 0 {
		t.Fatal("failed verification leaked a worker slot")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	request(cancelled)
	if len(kerberosAcceptSlots) != 0 {
		t.Fatal("cancelled request leaked a worker slot")
	}
	for range cap(kerberosAcceptSlots) {
		kerberosAcceptSlots <- struct{}{}
		defer func() { <-kerberosAcceptSlots }()
	}
	request(t.Context())
}
