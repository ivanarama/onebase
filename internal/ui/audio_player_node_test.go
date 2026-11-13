package ui

import (
	"os/exec"
	"testing"
)

func TestAudioPlayerClientProtocol(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	cmd := exec.CommandContext(t.Context(), node, "static/audio_behavior_test.js")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("audio client protocol: %v\n%s", err, out)
	}
}
