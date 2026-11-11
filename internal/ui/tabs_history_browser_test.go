package ui

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Run the real shell and widget filter handlers against an authenticated server.
func TestTabsHistoryBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reg, entities := filtersFixtureReg(db)
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	for i, topic := range []string{"A", "B"} {
		id := []string{"79f4d98a-3ce0-4da4-82ea-e9c8686e804f", "a8b64d2d-d422-490a-9e4e-092eefc47a40"}[i]
		if err := db.Upsert(ctx, "Задача", mustUUID(t, id), map[string]any{"Тема": topic}, entities[0]); err != nil {
			t.Fatal(err)
		}
	}
	reg.LoadHomePage(&metadata.HomePage{Title: "History", Rows: []metadata.HomePageRow{{Widgets: []string{"ЗадачиФильтр"}}}})
	if err := db.SaveFormOpenMode(ctx, storage.FormModeTabs); err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, "admin", "secret123", "Browser acceptance", true); err != nil {
		t.Fatal(err)
	}
	s := New(reg, db, interpreter.New(), repo, Config{}, nil)
	router := chi.NewRouter()
	handlers := &auth.Handlers{Repo: repo}
	router.Post("/auth/login", handlers.LoginJSON)
	s.MountStatic(router)
	router.Group(func(r chi.Router) { r.Use(repo.Middleware); s.Mount(r) })
	server := httptest.NewServer(router)
	defer func() { s.CloseEventStreams(); server.Close(); s.Close() }()
	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "tabs-history.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=tabs-history", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("tabs history browser acceptance: %v", err)
	}
}
