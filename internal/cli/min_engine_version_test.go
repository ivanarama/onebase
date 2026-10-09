package cli

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/version"
)

func TestCheckMinimumEngineVersion(t *testing.T) {
	previous := version.Build
	t.Cleanup(func() { version.Build = previous })
	for _, tt := range []struct {
		name, current, required string
		warn                    bool
	}{
		{"absent", "v1.2.3", "", false},
		{"empty", "v1.2.3", " ", false},
		{"newer-required", "v1.2.3", "1.2.4", true},
		{"equal", "v1.2.3", "1.2.3", false},
		{"older-required", "v1.2.3", "v1.2.2", false},
		{"numeric-minor", "v1.9.99", "1.10.0", true},
		{"release-required", "v1.2.3-rc.1", "1.2.3", true},
		{"prerelease-required", "v1.2.3", "1.2.3-rc.1", false},
		{"numeric-prerelease", "v1.2.3-rc.2", "1.2.3-rc.10", true},
		{"alpha-after-numeric", "v1.2.3-beta", "1.2.3-10", false},
		{"shorter-prerelease", "v1.2.3-alpha", "1.2.3-alpha.1", true},
		{"build-metadata", "v1.2.3+foo", "1.2.3+bar", false},
		{"large-version", "v1.2.3", "999999999999999999999.0.0", true},
		{"invalid", "v1.2.3", "latest", true},
		{"incomplete", "v1.2.3", "1.2", true},
		{"leading-zero", "v1.2.3", "01.2.3", true},
		{"prerelease-leading-zero", "v1.2.3", "1.2.3-01", true},
		{"dev", "dev-abc1234", "1.2.3", true},
		{"build-channel", "build-500", "1.2.3", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			version.Build = tt.current
			dir := checkFixture(t, false)
			body := "name: check-test\nversion: '1.0'\n"
			if tt.required != "" {
				body += "min_engine_version: '" + tt.required + "'\n"
			}
			writeProcrunFixture(t, dir, "config/app.yaml", body)
			for _, flags := range []map[string]string{nil, {"json": "true"}} {
				out, err := runCheckCmd(t, runCheck, dir, flags)
				if err != nil {
					t.Fatalf("advisory requirement failed check: %v\n%s", err, out)
				}
				found := strings.Contains(out, "config.min-engine-version")
				if flags != nil {
					var result configcheck.Result
					if err := json.Unmarshal([]byte(out), &result); err != nil {
						t.Fatal(err)
					}
					if !result.OK || result.Total != 0 {
						t.Fatalf("warning became an error: %+v", result)
					}
				}
				if found != tt.warn {
					t.Fatalf("warning=%v, want %v: %s", found, tt.warn, out)
				}
			}
		})
	}
}

func TestRunMinimumEngineVersionWarnsAndServes(t *testing.T) {
	previous := version.Build
	version.Build = "v1.2.3"
	t.Cleanup(func() { version.Build = previous })
	var logs bytes.Buffer
	logger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(logger) })
	dir := checkFixture(t, false)
	writeProcrunFixture(t, dir, "config/app.yaml", "name: version-test\nmin_engine_version: '2.0.0'\n")
	base, _, stop := bootSmokeServerProject(t, dir)
	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	stop(t)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("startup was blocked: %d", resp.StatusCode)
	}
	if !strings.Contains(logs.String(), "требуется платформа не ниже 2.0.0, установлена v1.2.3") {
		t.Fatalf("run did not warn: %s", logs.String())
	}
}
