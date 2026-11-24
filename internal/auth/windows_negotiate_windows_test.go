//go:build windows

package auth

import (
	"crypto/sha256"
	"encoding/binary"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alexbrainman/sspi"
	"golang.org/x/sys/windows"
)

func TestWindowsSSPIRejectsMalformedTicket(t *testing.T) {
	if sid, _, err := acceptWindowsTicket(t.Context(), "HTTP/base.example.org", []byte("invalid")); err == nil || sid != "" {
		t.Fatalf("SSPI unexpectedly authenticated a malformed ticket")
	}
}

// Run under the domain service account that owns ONEBASE_TEST_WINDOWS_SPN.
// The Windows KDC and SSPI perform both sides of the exchange using the
// current logon. The outer HTTPS endpoint supplies a real TLS certificate.
func TestWindowsDomainKerberos(t *testing.T) {
	spn := os.Getenv("ONEBASE_TEST_WINDOWS_SPN")
	if spn == "" {
		t.Skip("set ONEBASE_TEST_WINDOWS_SPN to an HTTP SPN owned by the test process account")
	}
	cred, err := sspi.AcquireCredentials("", sspi.NEGOSSP_NAME, sspi.SECPKG_CRED_OUTBOUND, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cred.Release() }()
	client := sspi.NewClientContext(cred, sspi.ISC_REQ_CONNECTION|sspi.ISC_REQ_MUTUAL_AUTH)
	defer func() { _ = client.Release() }()
	target, err := windows.UTF16PtrFromString(spn)
	if err != nil {
		t.Fatal(err)
	}
	outer := httptest.NewTLSServer(nil)
	defer outer.Close()
	certHash := sha256.Sum256(outer.Certificate().Raw)
	appData := append([]byte("tls-server-end-point:"), certHash[:]...)
	// SEC_CHANNEL_BINDINGS followed by the application data, as in Schannel.
	bindings := make([]byte, 32+len(appData))
	binary.LittleEndian.PutUint32(bindings[24:28], uint32(len(appData))) // #nosec G115 -- fixed-size hash and literal prefix.
	binary.LittleEndian.PutUint32(bindings[28:32], 32)
	copy(bindings[32:], appData)
	in := []sspi.SecBuffer{{}}
	in[0].Set(sspi.SECBUFFER_CHANNEL_BINDINGS, bindings)
	out := []sspi.SecBuffer{{}}
	request := make([]byte, maxWindowsTicket)
	out[0].Set(sspi.SECBUFFER_TOKEN, request)
	status := client.Update(target, sspi.NewSecBufferDesc(out), sspi.NewSecBufferDesc(in))
	if status != sspi.SEC_I_CONTINUE_NEEDED {
		t.Fatalf("initial client context: %v", status)
	}
	sid, reply, err := acceptWindowsTicket(t.Context(), spn, request[:out[0].BufferSize])
	if err != nil {
		t.Fatalf("accept domain ticket: %v", err)
	}
	localUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if want := localUser.User.Sid.String(); sid == "" || sid != want {
		t.Fatalf("context SID %s, want %s", sid, want)
	}
	if len(reply) == 0 {
		t.Fatal("missing mutual authentication token")
	}
	in = append(in, sspi.SecBuffer{})
	in[1].Set(sspi.SECBUFFER_TOKEN, reply)
	out[0].Set(sspi.SECBUFFER_TOKEN, request)
	if status := client.Update(target, sspi.NewSecBufferDesc(out), sspi.NewSecBufferDesc(in)); status != sspi.SEC_E_OK {
		t.Fatalf("verify mutual authentication: %v", status)
	}
}
