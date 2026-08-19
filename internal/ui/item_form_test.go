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
	fields := make([]metadata.ItemFormField, 0, len(itemForm))
	for _, name := range itemForm {
		fields = append(fields, metadata.ItemFormField{Name: name})
	}
	return itemFormCatalogFields(fields...)
}

func itemFormCatalogFields(itemForm ...metadata.ItemFormField) *metadata.Entity {
	return &metadata.Entity{
		Name: "Контрагенты", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
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

// Реквизит, помеченный `{name: X, readonly: true}`, показывается, но не
// редактируется (#1011). До этого «видно, но не править» требовало managed-формы:
// ради одного служебного реквизита приходилось описывать в YAML всю форму
// целиком — все поля и все табличные части.
func TestItemForm_ReadOnlyFieldIsShownButNotEditable(t *testing.T) {
	ent := itemFormCatalogFields(
		metadata.ItemFormField{Name: "Наименование"},
		metadata.ItemFormField{Name: "Комментарий", ReadOnly: true},
	)
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{
		"Наименование": "Альфа", "Комментарий": "собрано модулем",
	}, ent); err != nil {
		t.Fatalf("вставка: %v", err)
	}

	html := renderEditForm(t, s, id)
	if !strings.Contains(html, `name="Комментарий" value="собрано модулем" placeholder="Комментарий" readonly>`) {
		t.Errorf("реквизит «только просмотр» отрисован обычным полем ввода:\n%s", itemFormFragment(html, "Комментарий"))
	}
	// Скрытым он не стал: смысл признака — именно показать значение.
	if strings.Contains(html, `<input type="hidden" name="Комментарий"`) {
		t.Error("реквизит «только просмотр» уехал в скрытое поле вместо видимого")
	}
	// Соседний реквизит правится как раньше.
	if strings.Contains(html, `name="Наименование" value="Альфа" placeholder="Наименование" readonly`) {
		t.Error("readonly просочился на реквизит, который им не помечен")
	}
}

// Списковые поля (ссылка, перечисление, булево) в режиме просмотра рисуются
// disabled — а отключённый select браузер не отправляет вовсе. Поэтому рядом
// обязан ехать скрытый двойник: без него сборка полей формы (formToFields)
// положила бы nil, и первое же сохранение обнулило бы реквизит.
func TestItemForm_ReadOnlySelectKeepsValueOnSave(t *testing.T) {
	ent := itemFormCatalogFields(
		metadata.ItemFormField{Name: "Наименование"},
		metadata.ItemFormField{Name: "Активен", ReadOnly: true},
	)
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{
		"Наименование": "Альфа", "Активен": true,
	}, ent); err != nil {
		t.Fatalf("вставка: %v", err)
	}

	html := renderEditForm(t, s, id)
	if !strings.Contains(html, `<input type="hidden" name="Активен" value="true">`) {
		t.Fatalf("нет скрытого двойника значения:\n%s", itemFormFragment(html, "Активен"))
	}
	if !strings.Contains(html, `<select disabled>`) {
		t.Errorf("список не заблокирован:\n%s", itemFormFragment(html, "Активен"))
	}

	// Браузер отправит только hidden — disabled-элементы в запрос не попадают.
	form := url.Values{"Наименование": {"Альфа-2"}, "Активен": {"true"}}
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
	if !isTruthyStored(rec["Активен"]) {
		t.Errorf("значение реквизита «только просмотр» потеряно при записи: %#v", rec["Активен"])
	}
}

// itemFormFragment — кусок формы вокруг поля, чтобы в отчёте теста была видна
// причина, а не вся страница.
func itemFormFragment(html, field string) string {
	at := strings.Index(html, field)
	if at < 0 {
		return html
	}
	from := at - 200
	if from < 0 {
		from = 0
	}
	to := at + 600
	if to > len(html) {
		to = len(html)
	}
	return html[from:to]
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
