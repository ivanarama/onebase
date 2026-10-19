package ui

import (
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
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// Виджет-список «мои записи»: &ТекущийПользователь — идентификатор учётки
// смотрящего, &ТекущийЛогин — его логин. Один и тот же виджет на главной у
// двух пользователей показывает каждому своё — проверка через тот же адрес,
// которым браузер обновляет виджет.
func TestWidgetPartial_CurrentUserParams(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "widget-user.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	задача := &metadata.Entity{
		Name: "Задача", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Исполнитель", Type: metadata.FieldTypeString},
			{Name: "Логин", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{задача}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, row := range []map[string]any{
		{"Наименование": "задача первого", "Исполнитель": "u-1", "Логин": "first"},
		{"Наименование": "задача второго", "Исполнитель": "u-2", "Логин": "second"},
	} {
		if err := db.Upsert(ctx, задача.Name, uuid.New(), row, задача); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{задача}})
	reg.LoadWidgets([]*metadata.Widget{
		{Name: "МоиЗадачи", Type: metadata.WidgetTypeList, Title: "Мои задачи",
			Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Задача ГДЕ Исполнитель = &ТекущийПользователь"},
		{Name: "ПоЛогину", Type: metadata.WidgetTypeList, Title: "По логину",
			Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Задача ГДЕ Логин = &ТекущийЛогин"},
	})
	reg.LoadHomePage(&metadata.HomePage{Layout: "rows", Rows: []metadata.HomePageRow{{Widgets: []string{"МоиЗадачи", "ПоЛогину"}}}})
	s := &Server{reg: reg, store: db, widgetCache: widget.NewCache(time.Minute), messages: NewMessageStore()}
	router := chi.NewRouter()
	s.Mount(router)

	html := func(name string, user *auth.User) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/ui/_widget/"+url.PathEscape(name), nil)
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%q", name, rec.Code, rec.Body.String())
		}
		var envelope widgetPartialResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return envelope.HTML
	}
	первый := &auth.User{ID: "u-1", Login: "first", IsAdmin: true}
	второй := &auth.User{ID: "u-2", Login: "second", IsAdmin: true}
	for _, w := range []string{"МоиЗадачи", "ПоЛогину"} {
		if got := html(w, первый); !strings.Contains(got, "задача первого") || strings.Contains(got, "задача второго") {
			t.Fatalf("%s первому пользователю: %q", w, got)
		}
		// Кэш виджета — по пользователю: второй не получает чужой результат.
		if got := html(w, второй); !strings.Contains(got, "задача второго") || strings.Contains(got, "задача первого") {
			t.Fatalf("%s второму пользователю: %q", w, got)
		}
	}
}

// Обычная главная читает кэш, в отличие от принудительного обновления виджета.
// Каждый служебный параметр проверяется отдельно, чтобы правильный результат
// одного виджета не скрыл чужие или устаревшие данные другого.
func TestWidgetHome_CurrentUserCacheIsolation(t *testing.T) {
	for _, parameter := range []struct {
		name  string
		field string
	}{
		{"ТекущийПользователь", "Исполнитель"},
		{"ТекущийЛогин", "Логин"},
	} {
		t.Run(parameter.name, func(t *testing.T) {
			for _, order := range []struct {
				name  string
				first int
			}{
				{"first-then-second", 0},
				{"second-then-first", 1},
			} {
				t.Run(order.name, func(t *testing.T) {
					dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
						ctx := context.Background()
						entity := &metadata.Entity{
							Name: "Задача", Kind: metadata.KindCatalog,
							Fields: []metadata.Field{
								{Name: "Наименование", Type: metadata.FieldTypeString},
								{Name: "Исполнитель", Type: metadata.FieldTypeString},
								{Name: "Логин", Type: metadata.FieldTypeString},
							},
						}
						if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
							t.Fatalf("Migrate: %v", err)
						}
						users := []*auth.User{
							{ID: "u-1", Login: "first", IsAdmin: true},
							{ID: "u-2", Login: "second", IsAdmin: true},
						}
						ids := []uuid.UUID{uuid.New(), uuid.New()}
						original := []string{"задача первого", "задача второго"}
						updated := []string{"изменённая задача первого", "изменённая задача второго"}
						write := func(i int, name string) {
							t.Helper()
							if err := db.Upsert(ctx, entity.Name, ids[i], map[string]any{
								"Наименование": name, "Исполнитель": users[i].ID, "Логин": users[i].Login,
							}, entity); err != nil {
								t.Fatalf("Upsert: %v", err)
							}
						}
						for i := range users {
							write(i, original[i])
						}
						reg := runtime.NewRegistry()
						reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
						const widgetName = "МоиЗадачи"
						reg.LoadWidgets([]*metadata.Widget{{
							Name: widgetName, Type: metadata.WidgetTypeList, Title: "Мои задачи",
							Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Задача ГДЕ " + parameter.field + " = &" + parameter.name,
						}})
						reg.LoadHomePage(&metadata.HomePage{Layout: "rows", Rows: []metadata.HomePageRow{{Widgets: []string{widgetName}}}})
						s := &Server{reg: reg, store: db, widgetCache: widget.NewCache(time.Hour), messages: NewMessageStore()}
						router := chi.NewRouter()
						s.Mount(router)
						get := func(path string, i int) string {
							t.Helper()
							req := httptest.NewRequest(http.MethodGet, path, nil)
							req = req.WithContext(auth.ContextWithUser(req.Context(), users[i]))
							rec := httptest.NewRecorder()
							router.ServeHTTP(rec, req)
							if rec.Code != http.StatusOK {
								t.Fatalf("%s (%s): status=%d body=%q", path, users[i].Login, rec.Code, rec.Body.String())
							}
							if path == "/ui/" {
								return rec.Body.String()
							}
							var envelope widgetPartialResponse
							if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
								t.Fatalf("decode: %v", err)
							}
							return envelope.HTML
						}
						check := func(got string, i int, fresh bool) {
							t.Helper()
							want := original[i]
							if fresh {
								want = updated[i]
							}
							// Проверяем именно ячейку списка: обновлённое имя содержит
							// старое как подстроку и не должно сойти за cache hit.
							if strings.Count(got, "<td>"+want+"</td>") != 1 || strings.Contains(got, `class="w-error"`) {
								t.Fatalf("%s fresh=%t: expected one row %q without widget errors; body=%q", users[i].Login, fresh, want, got)
							}
							for _, name := range append(append([]string{}, original...), updated...) {
								if name != want && strings.Contains(got, "<td>"+name+"</td>") {
									t.Fatalf("%s fresh=%t: unexpected row %q", users[i].Login, fresh, name)
								}
							}
						}
						// Заполняем и перечитываем общий серверный кэш в обоих порядках.
						sequence := []int{order.first, 1 - order.first, 1 - order.first, order.first}
						for _, i := range sequence {
							check(get("/ui/", i), i, false)
						}
						for i := range users {
							write(i, updated[i])
						}
						// Старые строки после записи доказывают cache hit через HTTP.
						for _, i := range sequence {
							check(get("/ui/", i), i, false)
						}
						// Fresh-контроль подтверждает, что запись действительно изменилась.
						for _, i := range sequence[:2] {
							check(get("/ui/_widget/"+url.PathEscape(widgetName), i), i, true)
						}
					})
				})
			}
		})
	}
}
