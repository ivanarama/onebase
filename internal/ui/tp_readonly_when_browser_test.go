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
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

func TestTablePartColumnReadonlyWhenBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "tp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	form, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(browseracceptance.Root(), "e2e/fixtures/tp-readonly-when/order.form.yaml"), "Orders")
	if err != nil {
		t.Fatal(err)
	}
	e := &metadata.Entity{Name: "Orders", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Status", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{Name: "Lines", Fields: []metadata.Field{
			{Name: "Product", Type: metadata.FieldTypeString}, {Name: "Qty", Type: metadata.FieldTypeNumber},
		}}},
		Forms: []*metadata.FormModule{form}}
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, e.Name, id, map[string]any{"Status": "Closed"}, e); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(ctx, e.Name, "Lines", id, []map[string]any{{"Product": "Widget", "Qty": 5}}, e.TableParts[0]); err != nil {
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
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	s := New(reg, db, interp, repo, Config{}, nil)
	router := chi.NewRouter()
	h := &auth.Handlers{Repo: repo}
	router.Post("/auth/login", h.LoginJSON)
	s.MountStatic(router)
	router.Group(func(r chi.Router) { r.Use(repo.Middleware); s.Mount(r) })
	server := httptest.NewServer(router)
	defer func() { s.CloseEventStreams(); server.Close(); s.Close() }()
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "tp-readonly-when.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=tp-readonly-when", "OB_ACCEPTANCE_URL="+server.URL, "OB_ACCEPTANCE_ORDER="+id.String())
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("tp readonly_when browser acceptance: %v", err)
	}
}
