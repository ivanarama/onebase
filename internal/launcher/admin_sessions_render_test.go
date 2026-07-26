package launcher

// Панель активных сессий конфигуратора (план 78): несколько сессий одного
// логина, вид сессии, kick по public_id и kick all, и сам kick-хендлер.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
)

// newSessionsFixture: sqlite-база с пользователем и двумя сессиями разных видов.
func newSessionsFixture(t *testing.T, baseID string) (*handler, *Base, *auth.Repo, [2]string) {
	t.Helper()
	ctx := context.Background()

	b := &Base{ID: baseID, Name: "s", ConfigSource: "file", Path: t.TempDir(),
		DBType: "sqlite", DBPath: filepath.Join(t.TempDir(), "s.db")}
	st := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := st.Add(b); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	t.Cleanup(CloseAuthPools)

	db, err := getAuthDB(ctx, b)
	if err != nil {
		t.Fatalf("getAuthDB: %v", err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	user, err := repo.Create(ctx, "admin", "password", "Админ", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ent, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise, IP: "127.0.0.1:1111"})
	cfg, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator, IP: "127.0.0.1:2222"})

	return &handler{store: st}, b, repo, [2]string{ent, cfg}
}

func TestCfgAdminSessions_RendersMultiSession(t *testing.T) {
	h, b, _, _ := newSessionsFixture(t, "sess-render")

	req := httptest.NewRequest("GET", "/bases/"+b.ID+"/configurator/admin/sessions", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.cfgAdminSessions(rec, req)

	html := rec.Body.String()
	if strings.Count(html, ">admin<") != 2 {
		t.Errorf("ожидались 2 строки логина admin, HTML:\n%s", html)
	}
	for _, want := range []string{"Предприятие", "Конфигуратор", "cfgKickSession(", "cfgKickAll(", "127.0.0.1:1111"} {
		if !strings.Contains(html, want) {
			t.Errorf("в HTML панели нет %q", want)
		}
	}
}

func TestCfgAdminSessionKick_ByPublicIDAndLogin(t *testing.T) {
	h, b, repo, tokens := newSessionsFixture(t, "sess-kick")
	ctx := context.Background()

	kick := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/bases/"+b.ID+"/configurator/admin/sessions/kick", strings.NewReader(body))
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", b.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.cfgAdminSessionKick(rec, req)
		return rec
	}

	// Одна сессия по public_id: enterprise завершается, configurator живёт.
	sessions, _ := repo.ActiveSessions(ctx)
	var entPublicID string
	for _, s := range sessions {
		if s.Kind == auth.SessionKindEnterprise {
			entPublicID = s.PublicID
		}
	}
	if entPublicID == "" {
		t.Fatal("не нашли enterprise-сессию")
	}
	rec := kick(`{"public_id":"` + entPublicID + `"}`)
	if rec.Code != 200 {
		t.Fatalf("kick по public_id: код %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := repo.LookupSession(ctx, tokens[0]); err == nil {
		t.Fatal("enterprise-сессия должна быть завершена")
	}
	if _, err := repo.LookupSession(ctx, tokens[1]); err != nil {
		t.Fatalf("configurator-сессия не должна пострадать: %v", err)
	}

	// Все сессии по логину.
	rec = kick(`{"login":"admin"}`)
	if rec.Code != 200 {
		t.Fatalf("kick по login: код %d", rec.Code)
	}
	if _, err := repo.LookupSession(ctx, tokens[1]); err == nil {
		t.Fatal("после kick all не должно остаться сессий")
	}

	// Пустое тело — 400.
	var resp map[string]any
	rec = kick(`{}`)
	if rec.Code != 400 {
		t.Fatalf("пустой kick должен вернуть 400, получен %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp["error"] == "" {
		t.Fatalf("ожидалась ошибка в JSON: %s", rec.Body.String())
	}
}
