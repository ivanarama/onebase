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
	hideAudioTestProcess(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("audio client protocol: %v\n%s", err, out)
	}
}

func TestAudioPlayerCompactControls(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	cmd := exec.CommandContext(t.Context(), node, "static/audio_compact_controls_test.js")
	hideAudioTestProcess(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compact audio controls: %v\n%s", err, out)
	}
}
