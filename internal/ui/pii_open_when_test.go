package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// pii_open_when — как в 1С сокрытие личных данных заявки только вне
// оформления: в форме записи «На оформлении» оператор видит телефон целиком и
// может его исправить; после ухода с оформления — снова маска, и правка не
// проходит. Условие считается по СОХРАНЁННОЙ записи: подменой статуса в
// запросе поле не раскрыть. Через публичные пути — показ формы, ответ события
// формы и запись — на SQLite и PostgreSQL.
func TestPIIOpenWhen_ObjectFormMatrix(t *testing.T) {
	const real = "89161234567"
	const typed = "89001112233"
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "ЗаявкиПДн", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Статус", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString, PII: true, PIIOpenWhen: `Статус = "НаОформлении"`},
		}}
		form := managedObjectForm(fieldEl("Имя", "Объект.Наименование"), fieldEl("Статус", "Объект.Статус"),
			fieldEl("Телефон", "Объект.Телефон"), maskEventButton("Выполнить", "Выполнить"))
		form.EntityName = entity.Name
		form.ProgramAST = mustParse(t, `Процедура Выполнить()
Объект.Наименование = "заявка";
КонецПроцедуры`)
		entity.Forms = []*metadata.FormModule{form}
		s := newSubmitTestServerOn(t, db, []*metadata.Entity{entity})
		router := chi.NewRouter()
		s.Mount(router)
		// Роль про телефон молчит — pii: true маскирует его по умолчанию.
		user := &auth.User{ID: "operator", Login: "operator", Roles: []*auth.Role{{Name: "Оператор",
			Permissions: auth.Permission{Catalogs: map[string][]string{entity.Name: {"read", "write"}}}}}}
		do := func(method, path string, form url.Values) *httptest.ResponseRecorder {
			t.Helper()
			var body *strings.Reader
			if form != nil {
				body = strings.NewReader(form.Encode())
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(method, path, body)
			if form != nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			req = req.WithContext(auth.ContextWithUser(req.Context(), user))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			return rec
		}
		base := "/ui/catalog/" + url.PathEscape(entity.Name)

		for _, tc := range []struct {
			name, stored, submittedStatus string
			open                          bool
		}{
			{name: "на_оформлении", stored: "НаОформлении", submittedStatus: "НаОформлении", open: true},
			{name: "принята", stored: "Принята", submittedStatus: "Принята", open: false},
			{name: "подмена_статуса_в_запросе", stored: "Принята", submittedStatus: "НаОформлении", open: false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				id := uuid.New()
				if err := s.store.Upsert(ctx, entity.Name, id, map[string]any{
					"Наименование": "заявка", "Статус": tc.stored, "Телефон": real}, entity); err != nil {
					t.Fatal(err)
				}
				// 1. Показ формы записи.
				page := do(http.MethodGet, base+"/"+id.String(), nil)
				if page.Code != http.StatusOK {
					t.Fatalf("GET: %d", page.Code)
				}
				if got := strings.Contains(page.Body.String(), real); got != tc.open {
					t.Fatalf("форма показывает телефон целиком = %v, want %v", got, tc.open)
				}
				// 2. Ответ события формы.
				version, err := s.store.EntityVersion(ctx, entity.Name, id)
				if err != nil {
					t.Fatal(err)
				}
				// Браузер присылает то, что видит: при закрытом поле — маску.
				shown := real
				if !tc.open {
					shown = "••••••"
				}
				ev := do(http.MethodPost, base+"/form-event", url.Values{
					"_id": {id.String()}, "_version": {strconv.FormatInt(version, 10)},
					"_element": {"Выполнить"}, "_event": {"Нажатие"},
					"Наименование": {"заявка"}, "Статус": {tc.submittedStatus}, "Телефон": {shown},
				})
				resp := decodeFormEventResponse(t, ev.Body.Bytes())
				if !resp.OK {
					t.Fatalf("form-event: %d %+v", ev.Code, resp)
				}
				if phone := fmt.Sprint(resp.Values["Телефон"]); (phone == real) != tc.open {
					t.Fatalf("ответ события: телефон %q, открыт должен быть %v", phone, tc.open)
				}
				if !tc.open && strings.Contains(ev.Body.String(), real) {
					t.Fatalf("ответ события раскрыл телефон: %s", ev.Body.String())
				}
				// 3. Запись правки телефона.
				save := do(http.MethodPost, base+"/"+id.String(), url.Values{
					"_version":     {strconv.FormatInt(version, 10)},
					"Наименование": {"заявка"}, "Статус": {tc.submittedStatus}, "Телефон": {typed},
				})
				if save.Code >= 400 {
					t.Fatalf("запись: %d %s", save.Code, save.Body.String())
				}
				row, err := s.store.GetByID(ctx, entity.Name, id, entity)
				if err != nil {
					t.Fatal(err)
				}
				want := real
				if tc.open {
					want = typed
				}
				if got := toStringOrEmpty(row["Телефон"]); got != want {
					t.Fatalf("сохранённый телефон %q, want %q", got, want)
				}
			})
		}
	})
}

// Условие с ошибкой оставляет маску: раскрыть по сломанному выражению нельзя.
func TestPIIOpenWhen_BrokenConditionKeepsMask(t *testing.T) {
	entity := &metadata.Entity{Name: "З", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Статус", Type: metadata.FieldTypeString},
		{Name: "Телефон", Type: metadata.FieldTypeString, PII: true, PIIOpenWhen: `НетТакогоПоля = 1`},
	}}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	user := &auth.User{ID: "u", Login: "u", Roles: []*auth.Role{{Name: "Р",
		Permissions: auth.Permission{Catalogs: map[string][]string{entity.Name: {"read"}}}}}}
	ctx := auth.ContextWithUser(context.Background(), user)
	dec := s.recordFieldDecisions(ctx, entity, map[string]any{"Статус": "x", "Телефон": "1"})
	if d, ok := fieldDecisionByName(dec, "Телефон"); !ok || !d.Masked() {
		t.Fatalf("сломанное условие раскрыло поле: %+v", dec)
	}
}
