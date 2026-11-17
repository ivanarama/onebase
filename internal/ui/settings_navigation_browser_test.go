package ui

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

func TestNavigationSettingsPaletteBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	f := newNavigationHTTPFixture(t)
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	f.server.cfg.Bundle, f.server.cfg.Lang = bundle, "ru"
	f.server.tmpl, err = newTemplate(bundle)
	if err != nil {
		t.Fatal(err)
	}
	f.server.hub = realtime.NewHub()
	server := httptest.NewServer(f.http)
	defer func() {
		f.server.CloseEventStreams()
		server.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "navigation-palette.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=navigation-palette", "OB_ACCEPTANCE_URL="+server.URL,
		"OB_NAV_ADMIN_SESSION="+f.cookie["admin"].Value, "OB_NAV_PERSONAL_SESSION="+f.cookie["alice"].Value)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("navigation palette browser acceptance: %v", err)
	}
}
