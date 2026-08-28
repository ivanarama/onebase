package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Условная доступность элементов управляемой формы (readonly_when / hidden_when).
// Смысл: запрет, который живёт в бизнес-логике, должен быть ВИДЕН на форме, а не
// прилетать исключением при записи — принятая заявка показывает производственные
// реквизиты нередактируемыми, а не «активными до первой попытки сохранить».

func формаСУсловиями(ent *metadata.Entity, els ...*metadata.FormElement) *metadata.FormModule {
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Title:      map[string]string{"ru": ent.Name},
		Elements:   els,
	}
	ent.Forms = []*metadata.FormModule{form}
	return form
}

func отрисоватьСУсловиями(t *testing.T, ent *metadata.Entity, form *metadata.FormModule, values map[string]string) string {
	t.Helper()
	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": false,
		"Values": values, "RefOptions": map[string]any{},
		"EnumOptions": map[string][]EnumOption{}, "TPRefOptions": map[string]any{},
		"User": nil, "Lang": "ru",
	}
	s.prepareManagedFormData(context.Background(), data, form)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

func заявкаСоСтадией() *metadata.Entity {
	return &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Улица", Type: metadata.FieldTypeString},
		{Name: "СтадияОформления", Type: metadata.FieldTypeString},
	}}
}

func TestУсловныйReadonly_ПоСостояниюЗаписи(t *testing.T) {
	ent := заявкаСоСтадией()
	form := формаСУсловиями(ent, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеУлица",
		DataPath: "Объект.Улица", ReadOnlyWhen: `СтадияОформления = "Принята"`,
	})

	// Проверяем сам input, а не наличие слова «readonly» на странице: оно есть в
	// служебном CSS формы (правило .form-group input[readonly]).
	вводУлицы := func(html string) string {
		i := strings.Index(html, `name="Улица"`)
		if i < 0 {
			t.Fatalf("поле «Улица» не отрисовано:\n%s", html)
		}
		j := strings.Index(html[i:], ">")
		return html[i : i+j]
	}

	черновик := вводУлицы(отрисоватьСУсловиями(t, ent, form, map[string]string{
		"Улица": "Ленина 1", "СтадияОформления": "НаОформлении"}))
	if strings.Contains(черновик, "readonly") {
		t.Errorf("черновик: поле не должно быть нередактируемым: %s", черновик)
	}

	принята := вводУлицы(отрисоватьСУсловиями(t, ent, form, map[string]string{
		"Улица": "Ленина 1", "СтадияОформления": "Принята"}))
	if !strings.Contains(принята, "readonly") {
		t.Errorf("принятая заявка: поле должно быть нередактируемым: %s", принята)
	}
}

func TestУсловноеСкрытие_ЭлементНеОтрисован(t *testing.T) {
	ent := заявкаСоСтадией()
	form := формаСУсловиями(ent,
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "КнопкаПринять",
			TitleMap:   map[string]string{"ru": "Принять заявку"},
			HiddenWhen: `СтадияОформления = "Принята"`,
			Handlers:   map[metadata.FormEventType]string{metadata.FormEventOnClick: "Принять"},
		})

	черновик := отрисоватьСУсловиями(t, ent, form, map[string]string{"СтадияОформления": "НаОформлении"})
	if !strings.Contains(черновик, "Принять заявку") {
		t.Errorf("черновик: кнопка должна быть видна\n%s", черновик)
	}

	принята := отрисоватьСУсловиями(t, ent, form, map[string]string{"СтадияОформления": "Принята"})
	if strings.Contains(принята, "Принять заявку") {
		t.Errorf("принятая заявка: кнопка не должна отрисовываться\n%s", принята)
	}
}

func TestСостоянияЭлементов_СодержатЛожныеУсловия(t *testing.T) {
	// В карте состояний должен присутствовать КАЖДЫЙ элемент с объявленным
	// условием, в том числе с ложным: ответ события формы переносит карты на
	// клиент, и без явного false он не смог бы снять запрет.
	ent := заявкаСоСтадией()
	form := формаСУсловиями(ent, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеУлица",
		DataPath: "Объект.Улица", ReadOnlyWhen: `СтадияОформления = "Принята"`,
	})
	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}

	st := s.formElementStates(form, ent, map[string]any{"СтадияОформления": "НаОформлении"})
	if st == nil {
		t.Fatal("состояния не рассчитаны, ожидалась карта с ложным условием")
	}
	if v, есть := st.ReadOnly["ПолеУлица"]; !есть || v {
		t.Errorf("ReadOnly[ПолеУлица] = (%v, есть=%v), ожидалось (false, есть=true)", v, есть)
	}

	st = s.formElementStates(form, ent, map[string]any{"СтадияОформления": "Принята"})
	if !st.ReadOnly["ПолеУлица"] {
		t.Errorf("на принятой заявке ожидалось ReadOnly[ПолеУлица]=true")
	}
}

func TestНеверноеУсловие_НеЗапираетЭлемент(t *testing.T) {
	// Ошибка в условии — ошибка конфигурации. Молча запертое поле объяснить
	// пользователю нечем, поэтому условие игнорируется, а конфигуратор получает
	// предупреждение на форме.
	ent := заявкаСоСтадией()
	form := формаСУсловиями(ent, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеУлица",
		DataPath: "Объект.Улица", ReadOnlyWhen: `((`,
	})
	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": false,
		"Values": map[string]string{"СтадияОформления": "Принята"},
	}
	s.prepareManagedFormData(context.Background(), data, form)

	ro, _ := data["ElReadOnly"].(map[string]bool)
	if ro["ПолеУлица"] {
		t.Error("нерабочее условие не должно делать поле нередактируемым")
	}
	if data["FormWarnings"] == nil {
		t.Error("ожидалось предупреждение конфигуратору о нерабочем условии")
	}
}

// Скрытая табличная часть и запись формы.
//
// Скрытый элемент не отрисован — значит браузеру нечего отправить, и в POST нет
// ни строк, ни поля tp_json. Пустой срез при этом означал бы «пользователь
// удалил все строки», хотя он их даже не видел. Реквизиты шапки в той же
// скрытой группе сохранялись всегда (решение по отсутствию ключа в теле), а
// табличные части шли по метаданным формы — и терялись.

func заявкаСоСтроками(скрытие string, noGrid bool) (*metadata.Entity, metadata.TablePart) {
	tp := metadata.TablePart{Name: "Строки", Fields: []metadata.Field{
		{Name: "Товар", Type: metadata.FieldTypeString},
	}}
	группа := &metadata.FormElement{
		Kind: metadata.FormElementGroupBox, Name: "ГруппаСтрок",
		HiddenWhen: скрытие,
		Children: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "ТаблицаСтрок",
			DataPath: "Объект.Строки", NoGrid: noGrid,
		}},
	}
	form := managedObjectForm(fieldEl("ПолеСтадии", "Объект.СтадияОформления"), группа)
	ent := &metadata.Entity{
		Name: "ЗаявкаСоСтроками", Kind: metadata.KindCatalog,
		Fields:     []metadata.Field{{Name: "СтадияОформления", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{tp},
		Forms:      []*metadata.FormModule{form},
	}
	return ent, tp
}

func отрисоватьФормуСоСтроками(t *testing.T, ent *metadata.Entity, стадия string, строки []map[string]any) string {
	t.Helper()
	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	data := map[string]any{
		"Entity": ent, "Form": ent.Forms[0], "IsNew": false, "CanWrite": true,
		"Values":       map[string]string{"СтадияОформления": стадия},
		"RefOptions":   map[string]any{},
		"EnumOptions":  map[string]any{},
		"TPRefOptions": map[string]any{}, "TPRefMeta": map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TablePartRows": map[string][]map[string]any{"Строки": строки},
		"Lang":          "ru",
	}
	s.prepareManagedFormData(context.Background(), data, ent.Forms[0])
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

// записатьЗаявку прогоняет POST карточки ровно так, как это делает браузер:
// через публичный обработчик записи, а не мимо него.
func записатьЗаявку(t *testing.T, srv *Server, ent *metadata.Entity, id uuid.UUID, body url.Values) {
	t.Helper()
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/"+id.String(), body,
		map[string]string{"entity": ent.Name, "id": id.String()})
	rec := httptest.NewRecorder()
	srv.submitEdit(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("запись: статус=%d body=%s", rec.Code, rec.Body.String())
	}
}

func заявкаСоСтрокойВБазе(t *testing.T, ent *metadata.Entity, tp metadata.TablePart, стадия string) (*Server, uuid.UUID) {
	t.Helper()
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"СтадияОформления": стадия}, ent); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertTablePartRows(ctx, ent.Name, tp.Name, id,
		[]map[string]any{{"Товар": "Гвозди"}}, tp); err != nil {
		t.Fatal(err)
	}
	return srv, id
}

func строкиЗаявки(t *testing.T, srv *Server, ent *metadata.Entity, tp metadata.TablePart, id uuid.UUID) []map[string]any {
	t.Helper()
	rows, err := srv.store.GetTablePartRows(t.Context(), ent.Name, tp.Name, id, tp)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestСкрытаяТабличнаяЧасть_СтрокиПереживаютЗапись(t *testing.T) {
	ent, tp := заявкаСоСтроками(`СтадияОформления = "Принята"`, false)
	srv, id := заявкаСоСтрокойВБазе(t, ent, tp, "Принята")

	// На принятой заявке группа со строками скрыта: ни грида, ни скрытого
	// tp_json — отправлять браузеру нечего.
	html := отрисоватьФормуСоСтроками(t, ent, "Принята", []map[string]any{{"Товар": "Гвозди"}})
	if strings.Contains(html, "tp_json.Строки") {
		t.Fatalf("скрытая ТЧ всё же отрисовала поле отправки:\n%s", html)
	}

	// Ровно то, что уходит с этой формы: строк в теле нет.
	записатьЗаявку(t, srv, ent, id, url.Values{"СтадияОформления": {"Принята"}})

	rows := строкиЗаявки(t, srv, ent, tp, id)
	if len(rows) != 1 || rows[0]["Товар"] != "Гвозди" {
		t.Fatalf("строки скрытой ТЧ потеряны при записи: %#v", rows)
	}
}

func TestВидимаяТабличнаяЧасть_ПустойСрезВсёЖеОчищает(t *testing.T) {
	// Обратная сторона: пока таблица на форме есть, пустой tp_json значит
	// «пользователь удалил все строки», и удаление обязано работать.
	ent, tp := заявкаСоСтроками(`СтадияОформления = "Принята"`, false)
	srv, id := заявкаСоСтрокойВБазе(t, ent, tp, "НаОформлении")

	html := отрисоватьФормуСоСтроками(t, ent, "НаОформлении", []map[string]any{{"Товар": "Гвозди"}})
	if !strings.Contains(html, "tp_json.Строки") {
		t.Fatalf("видимая ТЧ должна отрисовать поле отправки:\n%s", html)
	}

	записатьЗаявку(t, srv, ent, id, url.Values{
		"СтадияОформления": {"НаОформлении"}, "tp_json.Строки": {"[]"}})

	if rows := строкиЗаявки(t, srv, ent, tp, id); len(rows) != 0 {
		t.Fatalf("удаление всех строк не сработало: %#v", rows)
	}
}

func TestПростаяТаблица_МаркерОтличаетУдалениеСтрокОтСкрытия(t *testing.T) {
	// no_grid: строки — это сами ключи tp.Строки.<i>.<колонка>. Удалив их все,
	// браузер шлёт то же самое, что и форма со скрытой таблицей, поэтому рядом
	// с таблицей рисуется маркер присутствия.
	ent, tp := заявкаСоСтроками(`СтадияОформления = "Принята"`, true)

	видимая := отрисоватьФормуСоСтроками(t, ent, "НаОформлении", []map[string]any{{"Товар": "Гвозди"}})
	if !strings.Contains(видимая, `name="tp_present.Строки"`) {
		t.Fatalf("видимая простая таблица должна отрисовать маркер присутствия:\n%s", видимая)
	}
	скрытая := отрисоватьФормуСоСтроками(t, ent, "Принята", []map[string]any{{"Товар": "Гвозди"}})
	if strings.Contains(скрытая, `name="tp_present.Строки"`) {
		t.Fatalf("скрытая простая таблица не должна отрисовывать маркер:\n%s", скрытая)
	}

	// Маркер есть, строк нет — пользователь удалил их сам.
	srv, id := заявкаСоСтрокойВБазе(t, ent, tp, "НаОформлении")
	записатьЗаявку(t, srv, ent, id, url.Values{
		"СтадияОформления": {"НаОформлении"}, "tp_present.Строки": {"1"}})
	if rows := строкиЗаявки(t, srv, ent, tp, id); len(rows) != 0 {
		t.Fatalf("удаление всех строк простой таблицы не сработало: %#v", rows)
	}

	// Ни маркера, ни строк — таблицы на форме не было.
	srv2, id2 := заявкаСоСтрокойВБазе(t, ent, tp, "Принята")
	записатьЗаявку(t, srv2, ent, id2, url.Values{"СтадияОформления": {"Принята"}})
	if rows := строкиЗаявки(t, srv2, ent, tp, id2); len(rows) != 1 {
		t.Fatalf("строки скрытой простой таблицы потеряны при записи: %#v", rows)
	}
}
