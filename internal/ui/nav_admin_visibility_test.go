package ui

// Issue #149: пункты меню «Система», ведущие в админ-разделы, не должны
// показываться пользователю без прав администратора. Серверные обработчики
// этих разделов и так отдают 403 неадмину — кроме agentSettings, у которого
// серверной проверки не было (см. TestAgentSettings_ForbiddenForNonAdmin).

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// adminNavLinks — ссылки меню «Система», доступные только администратору.
var adminNavLinks = []string{
	"/ui/admin/users",
	"/ui/admin/roles",
	"/ui/admin/sessions",
	"/ui/admin/api-tokens",
	"/ui/admin/audit",
	"/ui/admin/scheduled",
	"/ui/delete-marked",
	"/ui/admin/cleanup",
	"/ui/settings/agent",
}

func renderNav(t *testing.T, isAdmin bool) string {
	t.Helper()
	var buf bytes.Buffer
	data := map[string]any{"Cfg": Config{}, "Lang": "ru", "IsAdmin": isAdmin}
	if err := tmpl.ExecuteTemplate(&buf, "nav", data); err != nil {
		t.Fatalf("execute nav: %v", err)
	}
	return buf.String()
}

func TestNav_NonAdminOmitsAdminLinks(t *testing.T) {
	out := renderNav(t, false)
	for _, link := range adminNavLinks {
		if strings.Contains(out, link) {
			t.Errorf("меню неадмина не должно содержать %q", link)
		}
	}
}

func TestNav_AdminShowsAdminLinks(t *testing.T) {
	out := renderNav(t, true)
	for _, link := range adminNavLinks {
		if !strings.Contains(out, link) {
			t.Errorf("меню админа должно содержать %q", link)
		}
	}
}

func TestAgentSettings_ForbiddenForNonAdmin(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	// Непустой список пользователей → isAdmin(запрос без пользователя)==false.
	if _, err := authRepo.Create(ctx, "clerk", "pw", "Клерк", false); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, reg: runtime.NewRegistry(), authRepo: authRepo, messages: NewMessageStore()}

	req := httptest.NewRequest("GET", "/ui/settings/agent", nil)
	rec := httptest.NewRecorder()
	s.agentSettings(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("agentSettings неадмину: ожидался 403, получен %d", rec.Code)
	}
}
