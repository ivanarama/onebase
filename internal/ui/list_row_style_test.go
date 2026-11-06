package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// row_style формы списка через публичный GET списка — как ПриВыводеСтроки
// стола КЦ 1С: фон из пути по ссылке (цвет рекламы), правило по коду типа
// задачи важнее пути, жирный — по признаку. Цвет не по формату — без
// оформления; цвет из записи, которую пользователь читать не вправе, не
// показывается.
func TestListRowStyle_Markup(t *testing.T) {
	ad := &metadata.Entity{Name: "Реклама", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Цвет", Type: metadata.FieldTypeString},
	}}
	kind := &metadata.Entity{Name: "ТипЗадачи", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Код", Type: metadata.FieldTypeString},
	}}
	task := &metadata.Entity{Name: "Задача", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Реклама", Type: metadata.FieldType("reference:Реклама"), RefEntity: "Реклама"},
		{Name: "ТипЗадачи", Type: metadata.FieldType("reference:ТипЗадачи"), RefEntity: "ТипЗадачи"},
		{Name: "Пытался", Type: metadata.FieldTypeBool},
	}}
	task.Forms = []*metadata.FormModule{{
		Name: "ФормаСписка", Kind: "list", EntityName: task.Name, LayoutKind: metadata.FormLayoutManaged,
		ListRowStyle: &metadata.FormListRowStyle{
			Rules: []metadata.FormListRowStyleRule{
				{Field: "ТипЗадачи.Код", Equals: "000000037", Background: "#FAFAD2"},
			},
			BackgroundFrom: "Реклама.Цвет",
			BoldFrom:       "Пытался",
		},
	}}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ad, kind, task})
	ctx := context.Background()
	put := func(e *metadata.Entity, v map[string]any) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if err := s.store.Upsert(ctx, e.Name, id, v, e); err != nil {
			t.Fatal(err)
		}
		return id
	}
	red := put(ad, map[string]any{"Наименование": "Сайт", "Цвет": "#FF0055"})
	bad := put(ad, map[string]any{"Наименование": "Плохая", "Цвет": "red;background:url(https://evil/x)"})
	night := put(kind, map[string]any{"Наименование": "Ночной обзвон", "Код": "000000037"})
	plain := put(kind, map[string]any{"Наименование": "Обычная", "Код": "000000001"})
	put(task, map[string]any{"Наименование": "А-красная", "Реклама": red.String(), "ТипЗадачи": plain.String()})
	put(task, map[string]any{"Наименование": "Б-ночная", "Реклама": red.String(), "ТипЗадачи": night.String()})
	put(task, map[string]any{"Наименование": "В-жирная", "ТипЗадачи": plain.String(), "Пытался": true})
	put(task, map[string]any{"Наименование": "Г-плохой-цвет", "Реклама": bad.String(), "ТипЗадачи": plain.String()})

	router := chi.NewRouter()
	s.Mount(router)
	get := func(user *auth.User) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/ui/catalog/задача?sort=Наименование&dir=asc", nil)
		if user != nil {
			req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET списка: %d", rec.Code)
		}
		return rec.Body.String()
	}

	html := get(nil)
	for _, want := range []string{
		`style="cursor:pointer;background-color:#ff0055;color:#ffffff" class="ob-row-styled"`,
		`style="cursor:pointer;background-color:#fafad2;color:#111827" class="ob-row-styled"`,
		`style="cursor:pointer;font-weight:600" class="ob-row-styled"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("нет строки с оформлением %s", want)
		}
	}
	if got := strings.Count(html, `class="ob-row-styled"`); got != 3 {
		t.Errorf("оформлено строк: %d, want 3 (плохой цвет — без оформления)", got)
	}
	if strings.Contains(html, "url(https://evil") {
		t.Error("значение цвета не по формату попало в стиль")
	}

	// Пользователь без права чтения рекламы: цвет из неё не показывается,
	// правило по типу задачи и признак — по-прежнему.
	operator := &auth.User{ID: "op", Login: "op", Roles: []*auth.Role{{Name: "Оператор", Permissions: auth.Permission{
		Catalogs: map[string][]string{"Задача": {"read"}, "ТипЗадачи": {"read"}},
	}}}}
	html = get(operator)
	if strings.Contains(html, "#ff0055") {
		t.Error("цвет из недоступной пользователю рекламы попал в список")
	}
	if !strings.Contains(html, "background-color:#fafad2") || !strings.Contains(html, "font-weight:600") {
		t.Error("доступные правила оформления потерялись")
	}
}
