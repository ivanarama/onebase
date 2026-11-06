package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/page"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Exercise the real shell, ordinary forms, lists, table parts and an embedded
// service at desktop and phone sizes. No existing database or external API.
func TestPageEmbeddedServiceLayoutBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "tabs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ent := &metadata.Entity{Name: "Workspace", Kind: metadata.KindCatalog,
		Fields:     []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{Name: "Lines", Fields: []metadata.Field{{Name: "Item", Type: metadata.FieldTypeString}}}},
	}
	for i := 0; i < 24; i++ {
		ent.Fields = append(ent.Fields, metadata.Field{Name: fmt.Sprintf("Field%d", i), Type: metadata.FieldTypeString})
	}
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
	reg.LoadPages([]*page.Page{{Name: "Service"}})
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}, PagePrograms: map[string]*ast.Program{"Service": mustParse(t, `Процедура ПриФормировании(Страница, Параметры) Экспорт
 Страница.ВстроитьСервис("/hs/layout/", "Service");
 КонецПроцедуры`)}})
	s := New(reg, db, interpreter.New(), repo, Config{}, nil)
	router := chi.NewRouter()
	handlers := &auth.Handlers{Repo: repo}
	router.Get("/login", handlers.LoginPage)
	router.Post("/auth/login", handlers.LoginJSON)
	s.MountStatic(router)
	router.Group(func(r chi.Router) {
		r.Use(repo.Middleware)
		r.Get("/hs/layout/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><body style="margin:0;height:100dvh;display:flex;flex-direction:column"><div style="flex:1;overflow:auto">Service content</div><input id="composer" placeholder="Message" style="flex:none;height:40px"></body></html>`)
		})
		s.Mount(r)
	})
	server := httptest.NewServer(router)
	defer func() { s.CloseEventStreams(); server.Close(); s.Close() }()
	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "service-layout.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=service-layout", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("embedded service layout browser acceptance: %v", err)
	}
}
