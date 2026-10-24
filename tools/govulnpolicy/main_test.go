package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Exercise the command's stdin and exit status, including the fail-closed
// behavior required when govulncheck terminates before producing a valid stream.
func TestPolicyCommand(t *testing.T) {
	if os.Getenv("ONEBASE_VULNPOLICY_CHILD") == "1" {
		main()
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate current test executable: %v", err)
	}
	config := `{"config":{"scanner_name":"govulncheck"}}` + "\n"
	finding := func(id, function string) string {
		return `{"finding":{"osv":"` + id + `","trace":[{"module":"github.com/xuri/excelize/v2","version":"v2.11.1-0.20260930021559-01a9ff32fb3c","function":"` + function + `"}]}}` + "\n"
	}
	cases := []struct {
		name, input string
		success     bool
	}{
		{"clean", config, true},
		{"module_only", config + finding("GO-2099-0001", ""), true},
		{"empty", "", false},
		{"missing_config", finding("GO-2099-0001", "OpenReader"), false},
		{"null_config", `{"config":null}`, false},
		{"non_json", config + "scanner failed", false},
		{"truncated", config + `{"finding":`, false},
		{"old_pin_is_not_exempt", config + strings.ReplaceAll(finding("GO-2026-6452", "OpenReader"), "v2.11.1-0.20260930021559-01a9ff32fb3c", "v2.11.1-0.20260728235842-f98df08a8f6a"), false},
	}
	for _, id := range []string{"GO-2026-6452", "GO-2026-6684", "GO-2026-6688", "GO-2026-6689", "GO-2026-6685", "GO-2099-0001"} {
		cases = append(cases, struct {
			name, input string
			success     bool
		}{id, config + finding(id, "OpenReader"), false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(executable, "-test.run=^TestPolicyCommand$")
			cmd.Env = append(os.Environ(), "ONEBASE_VULNPOLICY_CHILD=1")
			cmd.Stdin = strings.NewReader(tc.input)
			out, err := cmd.CombinedOutput()
			if len(out) == 0 {
				t.Fatalf("policy command produced no output: %v", err)
			}
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
					t.Fatalf("policy command did not return its failure exit code: %v", err)
				}
			}
			if !strings.Contains(string(out), "govulnpolicy:") || (tc.success && !strings.Contains(string(out), "govulnpolicy: OK")) {
				t.Fatalf("missing policy result: %s", out)
			}
			if (err == nil) != tc.success {
				t.Fatalf("success=%v, want %v: %s (%v)", err == nil, tc.success, out, err)
			}
			if strings.HasPrefix(tc.name, "GO-") && !strings.Contains(string(out), "ЗАПРЕЩЕНО: "+tc.name) {
				t.Fatalf("missing advisory: %s", out)
			}
		})
	}
}
