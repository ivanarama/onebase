package ui

// Тумблер включённости регламентного задания в админке (#991).
//
// Тесты идут публичным путём — HTTP-хендлеры + шаблон страницы, как это
// делает администратор в браузере, — а не вызовом storage-методов напрямую.
// Правило из CLAUDE.md, повод — #611.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
)

// toggleTestServer — сервер с планировщиком и двумя заданиями: включённым и
// выключенным в конфигурации.
func toggleTestServer(t *testing.T) (*Server, *storage.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "toggle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureScheduledRunsTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}

	sched := scheduler.New(db, nil, nil)
	if err := sched.ReloadProjectJobs([]*metadata.ScheduledJob{
		{Name: "ВключенноеВКонфигурации", Title: "Включённое", Schedule: "@every 100h", Enabled: true},
		{Name: "ВыключенноеВКонфигурации", Title: "Выключенное", Schedule: "@every 100h", Enabled: false},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sched.Shutdown(context.Background()) })

	return &Server{
		store:     db,
		reg:       runtime.NewRegistry(),
		sched:     sched,
		messages:  NewMessageStore(),
		lockMgr:   runtime.NewLockManager(),
	}, db
}

// postScheduled дёргает POST-хендлер страницы задания с chi-параметром name.
func postScheduled(t *testing.T, s *Server, handler http.HandlerFunc, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/ui/admin/scheduled/"+name+"/toggle", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestScheduledToggle_ПишетРешениеИПоказываетНаСтранице(t *testing.T) {
	s, db := toggleTestServer(t)
	ctx := context.Background()

	// Выключенное в конфигурации включаем тумблером.
	rec := postScheduled(t, s, s.scheduledToggle, "ВыключенноеВКонфигурации")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("toggle: код %d, ожидался 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/ui/admin/scheduled/") {
		t.Fatalf("toggle: редирект на %q", loc)
	}

	// Решение реально записано в базу — тем же чтением, что и планировщик.
	if on, ok, err := db.GetScheduledEnabled(ctx, "ВыключенноеВКонфигурации"); err != nil || !ok || !on {
		t.Fatalf("после toggle: on=%v ok=%v err=%v, ожидалось true/true", on, ok, err)
	}

	// Карточка показывает источник состояния — «администратором».
	detail := getDetail(t, s, "ВыключенноеВКонфигурации")
	if !strings.Contains(detail, "включено администратором") {
		t.Fatal("карточка не показывает «включено администратором»")
	}
	if !strings.Contains(detail, "Вернуть как в конфигурации") {
		t.Fatal("при наличии решения нет кнопки возврата к конфигурации")
	}

	// Обратный тумблер выключает уже включённое.
	rec = postScheduled(t, s, s.scheduledToggle, "ВыключенноеВКонфигурации")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("обратный toggle: код %d", rec.Code)
	}
	if on, ok, _ := db.GetScheduledEnabled(ctx, "ВыключенноеВКонфигурации"); !ok || on {
		t.Fatalf("после обратного toggle: on=%v ok=%v, ожидалось false/true", on, ok)
	}

	// Аудит: действие администратора должно быть в журнале. Фильтр — по
	// действию: entity_kind в AuditSearch не фильтруется.
	entries, err := db.AuditSearch(ctx, storage.AuditFilter{Action: "scheduled.enable"}, 50, 0)
	if err != nil {
		t.Fatalf("AuditSearch: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("действие тумблера не попало в журнал аудита")
	}
}

func TestScheduledReset_ВозвращаетКонфигурацию(t *testing.T) {
	s, db := toggleTestServer(t)
	ctx := context.Background()

	if err := db.SaveScheduledEnabled(ctx, "ВключенноеВКонфигурации", false); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/ui/admin/scheduled/ВключенноеВКонфигурации/reset", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "ВключенноеВКонфигурации")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.scheduledReset(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reset: код %d, ожидался 303", rec.Code)
	}
	if _, ok, _ := db.GetScheduledEnabled(ctx, "ВключенноеВКонфигурации"); ok {
		t.Fatal("после reset решение осталось в базе")
	}

	detail := getDetail(t, s, "ВключенноеВКонфигурации")
	if !strings.Contains(detail, "✓ активно") {
		t.Fatal("после reset карточка не вернулась к состоянию конфигурации")
	}
	if strings.Contains(detail, "Вернуть как в конфигурации") {
		t.Fatal("после reset осталась кнопка возврата")
	}
}

func TestScheduledToggle_НесуществующееЗадание(t *testing.T) {
	s, _ := toggleTestServer(t)
	rec := postScheduled(t, s, s.scheduledToggle, "НетТакого")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("toggle неизвестного задания: код %d, ожидался 404", rec.Code)
	}
}

func TestScheduledToggle_ТребуетАдмина(t *testing.T) {
	s, db := toggleTestServer(t)
	ctx := context.Background()

	// Непустой список пользователей + запрос без пользователя → не админ
	// (образец — TestAgentSettings_ForbiddenForNonAdmin).
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := authRepo.Create(ctx, "clerk", "password", "Клерк", false); err != nil {
		t.Fatal(err)
	}
	s.authRepo = authRepo

	rec := postScheduled(t, s, s.scheduledToggle, "ВключенноеВКонфигурации")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("toggle неадмину: код %d, ожидался 403", rec.Code)
	}
}

// getDetail рендерит карточку задания (GET-хендлер).
func getDetail(t *testing.T, s *Server, name string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/ui/admin/scheduled/"+name, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.scheduledDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail %s: код %d", name, rec.Code)
	}
	return rec.Body.String()
}
