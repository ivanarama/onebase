// Package processcontrol implements the local launcher-to-base authentication
// protocol. The persistent secret is never sent over HTTP: a base proves its
// identity with HMAC, and a stop request is bound to that exact process
// instance so it cannot be replayed against a later restart.
package processcontrol

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

const (
	ChallengeQuery = "challenge"
	HeaderBaseID   = "X-OneBase-Base-ID"
	HeaderInstance = "X-OneBase-Control-Instance"
	HeaderNonce    = "X-OneBase-Control-Nonce"
	HeaderProof    = "X-OneBase-Control-Proof"

	authVersion = "onebase-process-control-v1"
	nonceBytes  = 32
)

// Identity is returned by the unauthenticated identity endpoint. Proof lets
// the launcher authenticate it without revealing the persistent secret.
type Identity struct {
	BaseID   string `json:"base_id"`
	PID      int    `json:"pid"`
	Instance string `json:"instance"`
	Proof    string `json:"proof"`
}

// NewNonce returns a cryptographically random challenge/request nonce.
func NewNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ValidNonce rejects unbounded or ambiguous challenge input before it is used
// in an HMAC transcript.
func ValidNonce(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == nonceBytes
}

func transcript(parts ...string) []byte {
	buf := make([]byte, 0, 256)
	for _, part := range append([]string{authVersion}, parts...) {
		buf = strconv.AppendInt(buf, int64(len(part)), 10)
		buf = append(buf, ':')
		buf = append(buf, part...)
	}
	return buf
}

func proof(secret string, parts ...string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(transcript(parts...))
	return hex.EncodeToString(mac.Sum(nil))
}

// IdentityProof authenticates a specific base process and launcher challenge.
func IdentityProof(secret, baseID string, pid int, instance, challenge string) string {
	return proof(secret, "identity", baseID, strconv.Itoa(pid), instance, challenge)
}

// StopProof authorizes stopping only the exact process instance that was
// authenticated immediately beforehand.
func StopProof(secret, baseID, instance, nonce string) string {
	return proof(secret, "stop", baseID, instance, nonce)
}

// Verify compares encoded proofs without timing-dependent early exits.
func Verify(got, want string) bool {
	gotBytes, gotErr := hex.DecodeString(got)
	wantBytes, wantErr := hex.DecodeString(want)
	return gotErr == nil && wantErr == nil && hmac.Equal(gotBytes, wantBytes)
}
