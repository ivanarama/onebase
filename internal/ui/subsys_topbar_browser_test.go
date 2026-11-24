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
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Разделы (подсистемы) — вкладками в тёмной верхней полосе, а не отдельной
// строкой под ней: рабочая область с разделами не ниже, чем без них.
// Два одинаковых сервера отличаются только наличием разделов.
func TestSubsystemTabsInTopbarBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	serve := func(subsystems bool) string {
		ctx := context.Background()
		db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "shell.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		e := &metadata.Entity{Name: "Orders", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}}}
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
		if subsystems {
			reg.LoadSubsystems([]*metadata.Subsystem{
				{Name: "Мессенджер", Title: "Мессенджер", Contents: metadata.SubsystemContents{Documents: []string{"Orders"}}},
				{Name: "Склад", Title: "Склад", Contents: metadata.SubsystemContents{Documents: []string{"Orders"}}},
			})
		}
		s := New(reg, db, interpreter.New(), repo, Config{}, nil)
		router := chi.NewRouter()
		h := &auth.Handlers{Repo: repo}
		router.Post("/auth/login", h.LoginJSON)
		s.MountStatic(router)
		router.Group(func(r chi.Router) { r.Use(repo.Middleware); s.Mount(r) })
		server := httptest.NewServer(router)
		t.Cleanup(func() { s.CloseEventStreams(); server.Close(); s.Close() })
		return server.URL
	}
	withBar, plain := serve(true), serve(false)
	runCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "subsys-topbar.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=subsys-topbar", "OB_ACCEPTANCE_URL="+withBar, "OB_ACCEPTANCE_PLAIN_URL="+plain)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("subsystem topbar browser acceptance: %v", err)
	}
}
