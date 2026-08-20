package main

import "testing"

func TestIsBinaryVersionProbeInvocation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "long flag", args: []string{"--version"}, want: true},
		{name: "short flag", args: []string{"-v"}, want: true},
		{name: "version flag with global flag", args: []string{"--no-gui", "--version"}, want: true},
		{name: "command", args: []string{"version"}, want: true},
		{name: "command after global flag", args: []string{"--no-gui", "version"}, want: true},
		{name: "invalid version arguments remain read only", args: []string{"version", "extra"}, want: true},
		{name: "ordinary command", args: []string{"start"}, want: false},
		{name: "ordinary command after global flag", args: []string{"--no-gui", "start"}, want: false},
		{name: "version flag before subcommand", args: []string{"--version", "start"}, want: false},
		{name: "subcommand version flag", args: []string{"start", "--version"}, want: false},
		{name: "terminator before version command", args: []string{"--", "version"}, want: false},
		{name: "terminator before version flag", args: []string{"--", "--version"}, want: false},
		{name: "global flag and terminator before version", args: []string{"--no-gui", "--", "version"}, want: false},
		{name: "no arguments", args: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBinaryVersionProbeInvocation(tt.args); got != tt.want {
				t.Fatalf("isBinaryVersionProbeInvocation(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
