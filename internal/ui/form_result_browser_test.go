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
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

func TestResultFormBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "result.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fixture := filepath.Join(browseracceptance.Root(), "e2e/fixtures/form-result")
	source, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(fixture, "source.form.yaml"), "Orders")
	if err != nil {
		t.Fatal(err)
	}
	mini, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(fixture, "result.form.yaml"), "Completion")
	if err != nil {
		t.Fatal(err)
	}
	e := &metadata.Entity{Name: "Orders", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Номер", Type: metadata.FieldTypeString}, {Name: "Комментарий", Type: metadata.FieldTypeString}, {Name: "Block", Type: metadata.FieldTypeBool},
	}, Forms: []*metadata.FormModule{source}}
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
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
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{e}})
	reg.LoadProcessors([]*processor.Processor{{Name: "Completion", Params: []processor.Param{{Name: "Record", Type: "reference:Orders"}}, Forms: []*metadata.FormModule{mini}}})
	s := New(reg, db, interpreter.New(), repo, Config{}, nil)
	router := chi.NewRouter()
	h := &auth.Handlers{Repo: repo}
	router.Get("/login", h.LoginPage)
	router.Post("/auth/login", h.LoginJSON)
	s.MountStatic(router)
	router.Group(func(r chi.Router) { r.Use(repo.Middleware); s.Mount(r) })
	server := httptest.NewServer(router)
	defer func() { s.CloseEventStreams(); server.Close(); s.Close() }()
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "form-result.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=form-result", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("result form browser acceptance: %v", err)
	}
}
