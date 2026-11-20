package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Exercise F9 through the real form with hidden leading columns and readonly grids.
func TestManagedColumnHiddenCopyRowBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	dbtest.ForEachDialect(t, testManagedColumnHiddenCopyRowBrowser)
}

func testManagedColumnHiddenCopyRowBrowser(t *testing.T, db *storage.DB) {
	server := newManagedColumnHiddenRowBrowserServer(t, db)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "column-hidden-copy-row.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=column-hidden-copy-row", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("column hidden copy row browser: %v", err)
	}
}
