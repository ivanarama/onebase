package processcontrol

import "testing"

func TestProofsAreBoundToChallengeActionAndInstance(t *testing.T) {
	const secret = "persistent-secret"
	challenge, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidNonce(challenge) || ValidNonce("short") {
		t.Fatal("nonce validation is inconsistent")
	}

	identity := IdentityProof(secret, "base-1", 42, "instance-1", challenge)
	if !Verify(identity, IdentityProof(secret, "base-1", 42, "instance-1", challenge)) {
		t.Fatal("valid identity proof was rejected")
	}
	if Verify(identity, IdentityProof(secret, "base-1", 42, "instance-2", challenge)) {
		t.Fatal("identity proof was accepted for another process instance")
	}
	if Verify(identity, StopProof(secret, "base-1", "instance-1", challenge)) {
		t.Fatal("identity proof was accepted as a stop authorization")
	}
}

func TestStopProofCannotBeReplayedAfterRestart(t *testing.T) {
	nonce, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	old := StopProof("secret", "base-1", "old-process", nonce)
	if Verify(old, StopProof("secret", "base-1", "new-process", nonce)) {
		t.Fatal("stop proof for an old process was accepted after restart")
	}
}
