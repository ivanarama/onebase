package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

func newWidgetPartialFixture(t *testing.T) (*Server, http.Handler, *metadata.Entity, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "widget-partial.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	entity := &metadata.Entity{
		Name: "Товар", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Наименование": "старое"}, entity); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
	reg.LoadWidgets([]*metadata.Widget{
		{Name: "Товары", Type: metadata.WidgetTypeList, Title: "Товары", Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Товар"},
		{Name: "График", Type: metadata.WidgetTypeChart, Title: "График", Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Товар", XField: "Наименование", ChartKind: "bar"},
		{Name: "Скрытый", Type: metadata.WidgetTypeList, Title: "Скрытый", Query: "это не должно исполняться"},
		{Name: "Действия", Type: metadata.WidgetTypeActions, Title: "Действия"},
	})
	reg.LoadHomePage(&metadata.HomePage{Layout: "rows", Rows: []metadata.HomePageRow{{Widgets: []string{"Товары", "Действия", "График"}}}})
	s := &Server{reg: reg, store: db, widgetCache: widget.NewCache(time.Minute), messages: NewMessageStore()}
	router := chi.NewRouter()
	s.Mount(router)
	return s, router, entity, id
}

func TestWidgetPartial_ChartCarriesSameServerOption(t *testing.T) {
	_, router, _, _ := newWidgetPartialFixture(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/_widget/"+url.PathEscape("График"), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("chart partial status=%d body=%q", rec.Code, rec.Body.String())
	}
	var envelope widgetPartialResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode chart partial: %v", err)
	}
	if envelope.Chart == nil || envelope.Chart["xAxis"] == nil {
		t.Fatalf("chart option missing from envelope: %#v", envelope.Chart)
	}
	if !strings.Contains(envelope.HTML, `class="w-chart-canvas"`) {
		t.Fatalf("chart body missing shared canvas: %q", envelope.HTML)
	}
}

func TestWidgetPartial_UsesSharedBodyAndFreshExactCacheEntry(t *testing.T) {
	s, router, entity, id := newWidgetPartialFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	data := s.homeDashboardData(req)
	result := data["WidgetRows"].([][]widget.Result)[0][0]
	var initial bytes.Buffer
	if err := tmpl.ExecuteTemplate(&initial, "widget-body", result); err != nil {
		t.Fatalf("render initial widget body: %v", err)
	}

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/ui/_widget/"+url.PathEscape("Товары"), nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first partial status=%d body=%q", first.Code, first.Body.String())
	}
	if got := first.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
	var envelope widgetPartialResponse
	if err := json.Unmarshal(first.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode partial: %v", err)
	}
	if strings.TrimSpace(envelope.HTML) != strings.TrimSpace(initial.String()) {
		t.Fatalf("initial and partial use different body\ninitial=%q\npartial=%q", initial.String(), envelope.HTML)
	}

	if err := s.store.Upsert(context.Background(), entity.Name, id, map[string]any{"Наименование": "новое"}, entity); err != nil {
		t.Fatalf("update row: %v", err)
	}
	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/ui/_widget/"+url.PathEscape("Товары"), nil))
	if second.Code != http.StatusOK {
		t.Fatalf("fresh partial status=%d body=%q", second.Code, second.Body.String())
	}
	if err := json.Unmarshal(second.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode fresh partial: %v", err)
	}
	if !strings.Contains(envelope.HTML, "новое") || strings.Contains(envelope.HTML, "старое") {
		t.Fatalf("manual refresh returned cached body: %q", envelope.HTML)
	}

	// Fresh replaces the same cache entry: the next ordinary full render also
	// sees the updated result rather than the stale pre-refresh entry.
	data = s.homeDashboardData(req)
	rows := data["WidgetRows"].([][]widget.Result)[0][0].Rows
	if len(rows) != 1 || rows[0]["наименование"] != "новое" {
		t.Fatalf("fresh partial did not replace exact cache entry: %#v", rows)
	}
}

func TestWidgetPartial_RejectsUnplacedActionAndUnknownParameters(t *testing.T) {
	_, router, _, _ := newWidgetPartialFixture(t)
	tests := []struct {
		path string
		want int
	}{
		{path: "/ui/_widget/" + url.PathEscape("Скрытый"), want: http.StatusNotFound},
		{path: "/ui/_widget/" + url.PathEscape("Действия"), want: http.StatusNotFound},
		{path: "/ui/_widget/" + url.PathEscape("Товары") + "?query=ВЫБРАТЬ", want: http.StatusBadRequest},
		{path: "/ui/_widget/" + url.PathEscape("Товары") + "?subsystem=missing", want: http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%q", rec.Code, tc.want, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
				t.Fatalf("error response Cache-Control=%q", got)
			}
		})
	}
}

func TestWidgetPartial_ResolvesGlobalAndSubsystemLayoutsWithVisibilityGate(t *testing.T) {
	s, router, entity, _ := newWidgetPartialFixture(t)
	subWidget := &metadata.Widget{Name: "Раздел", Type: metadata.WidgetTypeList, Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Товар"}
	s.reg.LoadWidgets([]*metadata.Widget{
		{Name: "Товары", Type: metadata.WidgetTypeList, Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Товар"},
		subWidget,
	})
	s.reg.LoadSubsystems([]*metadata.Subsystem{{
		Name:     "Продажи",
		Contents: metadata.SubsystemContents{Catalogs: []string{entity.Name}},
		HomePage: &metadata.HomePage{Layout: "rows", Rows: []metadata.HomePageRow{{Widgets: []string{subWidget.Name}}}},
	}})

	request := func(path string, user *auth.User) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if user != nil {
			req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	globalOnly := "/ui/_widget/" + url.PathEscape("Товары")
	subOnly := "/ui/_widget/" + url.PathEscape(subWidget.Name) + "?subsystem=" + url.QueryEscape("Продажи")
	if rec := request(globalOnly+"?subsystem="+url.QueryEscape("Продажи"), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("global widget leaked into subsystem layout: %d %q", rec.Code, rec.Body.String())
	}
	if rec := request("/ui/_widget/"+url.PathEscape(subWidget.Name), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("subsystem widget leaked into global layout: %d %q", rec.Code, rec.Body.String())
	}
	if rec := request(subOnly, nil); rec.Code != http.StatusOK {
		t.Fatalf("subsystem widget status=%d body=%q", rec.Code, rec.Body.String())
	}

	noRights := &auth.User{Login: "гость", Roles: []*auth.Role{{
		Permissions: auth.Permission{Processors: map[string][]string{}},
	}}}
	if rec := request(subOnly, noRights); rec.Code != http.StatusForbidden {
		t.Fatalf("hidden subsystem partial status=%d want=403 body=%q", rec.Code, rec.Body.String())
	}
}

func TestDashboardRefreshControls_DataWidgetsOnly(t *testing.T) {
	s, _, _, _ := newWidgetPartialFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/ui/?subsystem=Продажи", nil)
	for _, typ := range []metadata.WidgetType{metadata.WidgetTypeKPI, metadata.WidgetTypeList, metadata.WidgetTypeChart, metadata.WidgetTypeRecent} {
		res := widget.Result{Name: "Продажи за день", Type: string(typ), Title: "Продажи"}
		s.decorateRefreshResult(request, nil, &res, nil)
		if res.PartialURL != "/ui/_widget/"+url.PathEscape(res.Name)+"?subsystem="+url.QueryEscape("Продажи") {
			t.Fatalf("type=%s PartialURL=%q", typ, res.PartialURL)
		}
		var rendered bytes.Buffer
		if err := tmpl.ExecuteTemplate(&rendered, "widget-card", res); err != nil {
			t.Fatalf("render type=%s: %v", typ, err)
		}
		if !strings.Contains(rendered.String(), "data-ob-widget-refresh") {
			t.Fatalf("type=%s has no manual refresh control: %s", typ, rendered.String())
		}
	}
	action := widget.Result{Name: "Действия", Type: string(metadata.WidgetTypeActions), Title: "Действия"}
	s.decorateRefreshResult(request, nil, &action, nil)
	var rendered bytes.Buffer
	if err := tmpl.ExecuteTemplate(&rendered, "widget-card", action); err != nil {
		t.Fatalf("render actions: %v", err)
	}
	if strings.Contains(rendered.String(), "data-ob-widget-refresh") {
		t.Fatalf("actions widget unexpectedly got refresh: %s", rendered.String())
	}
}

func TestDashboardRefreshOnSubscription(t *testing.T) {
	s, _, _, _ := newWidgetPartialFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	res := widget.Result{Name: "Задачи", Type: string(metadata.WidgetTypeList), Title: "Задачи"}
	s.decorateRefreshResult(request, &metadata.Widget{
		Name: "Задачи", Type: metadata.WidgetTypeList,
		RefreshOn: []string{"данные.а_задача", "задача.изменён"},
	}, &res, nil)
	if res.RefreshOn != "данные.а_задача задача.изменён" {
		t.Fatalf("RefreshOn = %q", res.RefreshOn)
	}
	var rendered bytes.Buffer
	if err := tmpl.ExecuteTemplate(&rendered, "widget-card", res); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := rendered.String()
	for _, want := range []string{
		`data-ob-refresh-on="данные.а_задача задача.изменён"`,
		`data-ob-live="widget/Задачи"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("card markup misses %q: %s", want, out)
		}
	}

	// Без refresh_on атрибутов подписки быть не должно — карточка остаётся
	// только с ручной кнопкой.
	plain := widget.Result{Name: "Выручка", Type: string(metadata.WidgetTypeKPI), Title: "Выручка"}
	s.decorateRefreshResult(request, &metadata.Widget{Name: "Выручка", Type: metadata.WidgetTypeKPI}, &plain, nil)
	var plainBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&plainBuf, "widget-card", plain); err != nil {
		t.Fatalf("render plain: %v", err)
	}
	if strings.Contains(plainBuf.String(), "data-ob-refresh-on") || strings.Contains(plainBuf.String(), "data-ob-live") {
		t.Fatalf("plain card unexpectedly subscribed: %s", plainBuf.String())
	}
}
