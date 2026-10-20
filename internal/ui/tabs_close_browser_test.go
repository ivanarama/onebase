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
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// A real session, shell, managed form, DSL write, SSE notification and close
// lifecycle run together. Playwright holds only delivery of the unchanged
// form-event response so the command deterministically precedes savedId.
func TestTabsCloseBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "tabs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	form, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(browseracceptance.Root(), "e2e/fixtures/close-after-save/object.form.yaml"), "ПриемкаЗакрытия")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "ПриемкаЗакрытия", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Запрет", Type: metadata.FieldTypeBool},
	}, Forms: []*metadata.FormModule{form}}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
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
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := New(reg, db, interpreter.New(), repo, Config{}, nil)
	router := chi.NewRouter()
	handlers := &auth.Handlers{Repo: repo}
	router.Get("/login", handlers.LoginPage)
	router.Post("/auth/login", handlers.LoginJSON)
	s.MountStatic(router)
	router.Group(func(r chi.Router) { r.Use(repo.Middleware); s.Mount(r) })
	server := httptest.NewServer(router)
	defer func() { s.CloseEventStreams(); server.Close(); s.Close() }()
	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "tabs-close.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=tabs-close", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("tabs close browser acceptance: %v", err)
	}
}
