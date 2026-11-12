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
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

func TestShellSidePanelBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := &metadata.Entity{Name: "Tasks", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Описание", Type: metadata.FieldTypeString}}}
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFormOpenMode(ctx, storage.FormModeTabs); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, e.Name, uuid.New(), map[string]any{"Описание": "Проверить жалобу"}, e); err != nil {
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
	reg.LoadWidgets([]*metadata.Widget{{
		Name: "MyTasks", Type: metadata.WidgetTypeList, Title: "Задачи",
		Query:   "ВЫБРАТЬ Ссылка, Описание ИЗ Документ.Tasks",
		Source:  &metadata.WidgetSource{Entity: "Tasks", IDField: "Ссылка"},
		Columns: []metadata.WidgetColumn{{Field: "Описание", Label: "Задача"}},
	}})
	reg.LoadHomePage(&metadata.HomePage{Title: "Главная", SidePanel: &metadata.HomeSidePanel{
		Widget: "MyTasks", Width: 300, ExceptRoles: []string{"ОператорКЦ"},
	}})
	s := New(reg, db, interpreter.New(), repo, Config{}, nil)
	router := chi.NewRouter()
	h := &auth.Handlers{Repo: repo}
	router.Post("/auth/login", h.LoginJSON)
	s.MountStatic(router)
	router.Group(func(r chi.Router) { r.Use(repo.Middleware); s.Mount(r) })
	server := httptest.NewServer(router)
	defer func() { s.CloseEventStreams(); server.Close(); s.Close() }()
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "side-panel.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=side-panel", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("side panel browser acceptance: %v", err)
	}
}
