package ui

// `item_form:` определяет состав формы объекта (план 117, Д12).
//
// Ключ существовал с давних пор: парсился, хранился, отдавался в describe,
// круглился конфигуратором и проходил линт — но ни один рендерер его не читал.
// Пользователь снимал в конфигураторе галочку видимости реквизита, сохранял, и
// на форме ничего не менялось.
//
// Тесты идут через HTTP-обработчики формы и её сохранения: проверять надо то,
// что увидит и получит пользователь, а не то, что вернёт функция-делитель.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func itemFormCatalog(itemForm []string) *metadata.Entity {
	return &metadata.Entity{
		Name: "Контрагенты", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
		ItemForm: itemForm,
	}
}

func renderEditForm(t *testing.T, s *Server, id uuid.UUID) string {
	t.Helper()
	r := reqWithChi("GET", "/ui/catalog/Контрагенты/"+id.String(), nil, map[string]string{
		"entity": "Контрагенты",
		"id":     id.String(),
	})
	w := httptest.NewRecorder()
	s.formEdit(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("форма отдала %d: %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

// Реквизит, не названный в item_form, на форме не показывается.
func TestItemForm_HidesUnlistedField(t *testing.T) {
	ent := itemFormCatalog([]string{"Наименование", "Комментарий"})
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{
		metadata.StandardCodeField: "К-000042", "Наименование": "Альфа", "Комментарий": "важный",
	}, ent); err != nil {
		t.Fatalf("вставка: %v", err)
	}

	html := renderEditForm(t, s, id)
	if strings.Contains(html, `<input type="text" autocomplete="off" name="Код"`) {
		t.Error("скрытый реквизит «Код» отрисован как обычное поле ввода")
	}
	if !strings.Contains(html, `name="Наименование"`) || !strings.Contains(html, `name="Комментарий"`) {
		t.Error("перечисленные в item_form реквизиты пропали с формы")
	}
}

// Главное: скрыть — не значит стереть. formToFields кладёт nil для каждого
// реквизита, которого нет в запросе, поэтому без hidden-поля первое же
// сохранение обнулило бы скрытое значение.
func TestItemForm_HiddenFieldSurvivesSave(t *testing.T) {
	ent := itemFormCatalog([]string{"Наименование"})
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{
		metadata.StandardCodeField: "К-000042", "Наименование": "Альфа", "Комментарий": "важный",
	}, ent); err != nil {
		t.Fatalf("вставка: %v", err)
	}

	// Форма отдаёт скрытые реквизиты как hidden — именно они и вернутся в POST.
	html := renderEditForm(t, s, id)
	for _, want := range []string{
		`<input type="hidden" name="Код" value="К-000042">`,
		`<input type="hidden" name="Комментарий" value="важный">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в форме нет скрытого поля: %s", want)
		}
	}

	// Сохраняем ровно то, что отправил бы браузер.
	form := url.Values{
		"Наименование":             {"Альфа-2"},
		metadata.StandardCodeField: {"К-000042"},
		"Комментарий":              {"важный"},
	}
	r := reqWithChi("POST", "/ui/catalog/Контрагенты/"+id.String(), form, map[string]string{
		"entity": "Контрагенты", "id": id.String(),
	})
	w := httptest.NewRecorder()
	s.submitEdit(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("сохранение отдало %d: %s", w.Code, w.Body.String())
	}

	rec, err := s.store.GetByID(ctx, ent.Name, id, ent)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if got := itemFormValue(rec, metadata.StandardCodeField); got != "К-000042" {
		t.Errorf("скрытый «Код» после сохранения = %q, ожидался К-000042", got)
	}
	if got := itemFormValue(rec, "Комментарий"); got != "важный" {
		t.Errorf("скрытый «Комментарий» после сохранения = %q, ожидался «важный»", got)
	}
	if got := itemFormValue(rec, "Наименование"); got != "Альфа-2" {
		t.Errorf("видимый реквизит не сохранился: %q", got)
	}
}

// Порядок формы задаётся списком, а не порядком объявления: item_form — это и
// состав, и расположение, как list_form для колонок списка.
func TestItemForm_OrderFollowsList(t *testing.T) {
	ent := itemFormCatalog([]string{"Комментарий", "Наименование"})
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "Альфа"}, ent); err != nil {
		t.Fatalf("вставка: %v", err)
	}

	html := renderEditForm(t, s, id)
	iComment := strings.Index(html, `name="Комментарий"`)
	iName := strings.Index(html, `name="Наименование"`)
	if iComment < 0 || iName < 0 {
		t.Fatal("реквизиты не найдены на форме")
	}
	if iComment > iName {
		t.Error("порядок формы не следует item_form")
	}
}

// Без item_form ничего не меняется: видны все реквизиты, как было.
func TestItemForm_AbsentShowsEverything(t *testing.T) {
	ent := itemFormCatalog(nil)
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "Альфа"}, ent); err != nil {
		t.Fatalf("вставка: %v", err)
	}

	html := renderEditForm(t, s, id)
	for _, name := range []string{metadata.StandardCodeField, "Наименование", "Комментарий"} {
		if !strings.Contains(html, `name="`+name+`"`) {
			t.Errorf("без item_form пропал реквизит %s", name)
		}
	}
	if strings.Contains(html, `<input type="hidden" name="Комментарий"`) {
		t.Error("без item_form реквизит отрисован скрытым")
	}
}

func itemFormValue(rec map[string]any, field string) string {
	if v, ok := rec[field]; ok && v != nil {
		s, _ := v.(string)
		return s
	}
	low := strings.ToLower(field)
	for k, v := range rec {
		if strings.ToLower(k) == low && v != nil {
			s, _ := v.(string)
			return s
		}
	}
	return ""
}
