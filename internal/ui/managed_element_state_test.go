package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"golang.org/x/net/html"
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
	return отрисоватьСВариантами(t, ent, form, values, map[string]any{})
}

func отрисоватьСВариантами(t *testing.T, ent *metadata.Entity, form *metadata.FormModule, values map[string]string, refOptions map[string]any) string {
	t.Helper()
	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": false,
		"Values": values, "RefOptions": refOptions,
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

// --- Флажок под условием ---------------------------------------------------
// Тот же класс, что и у скрытой табличной части, только цена ошибки другая:
// таблица теряла строки, а флажок молча переписывает реквизит в ложь. Браузер
// не шлёт ключ ни когда галка снята, ни когда флажка на форме не было вовсе, —
// значит по одному лишь размещению на форме эти случаи неразличимы.

func заявкаСФлажком(скрытие, запрет string) *metadata.Entity {
	флажок := &metadata.FormElement{
		Kind: metadata.FormElementCheckbox, Name: "ФлагСогласовано",
		DataPath: "Объект.Согласовано", HiddenWhen: скрытие, ReadOnlyWhen: запрет,
	}
	ent := &metadata.Entity{
		Name: "ЗаявкаСФлажком", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "СтадияОформления", Type: metadata.FieldTypeString},
			{Name: "Согласовано", Type: metadata.FieldTypeBool},
		},
	}
	ent.Forms = []*metadata.FormModule{managedObjectForm(
		fieldEl("ПолеСтадии", "Объект.СтадияОформления"), флажок)}
	return ent
}

func заявкаСВзведённымФлажком(t *testing.T, ent *metadata.Entity, стадия string) (*Server, uuid.UUID) {
	t.Helper()
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{
		"СтадияОформления": стадия, "Согласовано": true}, ent); err != nil {
		t.Fatal(err)
	}
	return srv, id
}

func флажокЗаявки(t *testing.T, srv *Server, ent *metadata.Entity, id uuid.UUID) any {
	t.Helper()
	row, err := srv.store.GetByID(t.Context(), ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	return row["Согласовано"]
}

func отрисоватьЗаявкуСФлажком(t *testing.T, ent *metadata.Entity, стадия string) string {
	t.Helper()
	return отрисоватьСУсловиями(t, ent, ent.Forms[0], map[string]string{
		"СтадияОформления": стадия, "Согласовано": "true"})
}

type managedBrowserControl struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	Value            string `json:"value"`
	Checked          bool   `json:"checked"`
	Disabled         bool   `json:"disabled"`
	ReadOnly         bool   `json:"readOnly"`
	CheckboxPresence bool   `json:"checkboxPresence"`
}

type managedBrowserResult struct {
	Controls []managedBrowserControl `json:"controls"`
	Values   url.Values              `json:"values"`
}

func managedHTMLAttr(n *html.Node, name string) (string, bool) {
	for _, attr := range n.Attr {
		if attr.Key == name {
			return attr.Val, true
		}
	}
	return "", false
}

// managedElementAnchor находит якорь data-ob-el элемента формы — ровно тот узел,
// внутри которого applyElementStates ищет контролы.
func managedElementAnchor(t *testing.T, rendered, elementName string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatalf("parse managed form HTML: %v", err)
	}

	var wrapper *html.Node
	var findWrapper func(*html.Node)
	findWrapper = func(n *html.Node) {
		if wrapper != nil {
			return
		}
		if n.Type == html.ElementNode {
			if value, ok := managedHTMLAttr(n, "data-ob-el"); ok && value == elementName {
				wrapper = n
				return
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			findWrapper(child)
		}
	}
	findWrapper(doc)
	if wrapper == nil {
		t.Fatalf("managed form has no data-ob-el=%q", elementName)
	}
	return wrapper
}

// managedCheckboxControls parses the real managed-form markup, so the JS
// harness below starts with exactly the controls emitted by the template.
func managedCheckboxControls(t *testing.T, rendered, elementName string) []managedBrowserControl {
	t.Helper()
	wrapper := managedElementAnchor(t, rendered, elementName)

	var controls []managedBrowserControl
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			name, _ := managedHTMLAttr(n, "name")
			typeName, _ := managedHTMLAttr(n, "type")
			value, _ := managedHTMLAttr(n, "value")
			_, checked := managedHTMLAttr(n, "checked")
			_, disabled := managedHTMLAttr(n, "disabled")
			_, readOnly := managedHTMLAttr(n, "readonly")
			presence, _ := managedHTMLAttr(n, "data-ob-checkbox-presence")
			controls = append(controls, managedBrowserControl{
				Name: name, Type: typeName, Value: value,
				Checked: checked, Disabled: disabled, ReadOnly: readOnly,
				CheckboxPresence: presence == "1",
			})
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(wrapper)
	return controls
}

func managedControlByName(t *testing.T, controls []managedBrowserControl, name string) managedBrowserControl {
	t.Helper()
	for _, control := range controls {
		if control.Name == name {
			return control
		}
	}
	t.Fatalf("managed form has no control %q: %#v", name, controls)
	return managedBrowserControl{}
}

func successfulManagedControls(controls []managedBrowserControl) url.Values {
	values := make(url.Values)
	for _, control := range controls {
		if control.Name == "" || control.Disabled {
			continue
		}
		if (control.Type == "checkbox" || control.Type == "radio") && !control.Checked {
			continue
		}
		values.Add(control.Name, control.Value)
	}
	return values
}

// applyManagedElementStatesInNode executes applyElementStates extracted from
// the production managed.js. The returned values follow browser successful-
// control rules and can be sent straight to submitEdit.
func applyManagedElementStatesInNode(t *testing.T, controls []managedBrowserControl, states *elementStates) managedBrowserResult {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for managed checkbox state integration test")
	}
	payload, err := json.Marshal(struct {
		Controls []managedBrowserControl `json:"controls"`
		States   *elementStates          `json:"states"`
	}{Controls: controls, States: states})
	if err != nil {
		t.Fatal(err)
	}

	const script = `
const fs = require('node:fs');
const source = fs.readFileSync(process.argv[1], 'utf8');
function extract(name) {
  const start = source.indexOf('function ' + name);
  if (start < 0) throw new Error('managed.js has no function ' + name);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') {
      depth--;
      if (depth === 0) return source.slice(start, i + 1);
    }
  }
  throw new Error('unterminated function ' + name);
}
const payload = JSON.parse(fs.readFileSync(0, 'utf8'));
const controls = payload.controls.map((control) => ({
  tagName: 'INPUT',
  name: control.name,
  type: control.type,
  value: control.value,
  checked: control.checked,
  disabled: control.disabled,
  readOnly: control.readOnly,
  dataset: control.checkboxPresence ? {obCheckboxPresence: '1'} : {},
}));
const wrapper = {
  tagName: 'DIV',
  style: {},
  querySelectorAll(selector) {
    return selector === 'input, textarea' ? controls : [];
  },
};
global.window = {CSS: null};
global.document = {querySelector() { return wrapper; }};
const applyElementStates = new Function(
  extract('applyElementStates') + '\nreturn applyElementStates;'
)();
applyElementStates(payload.states);
const values = {};
for (const control of controls) {
  if (!control.name || control.disabled) continue;
  if ((control.type === 'checkbox' || control.type === 'radio') && !control.checked) continue;
  (values[control.name] ||= []).push(control.value);
}
process.stdout.write(JSON.stringify({
  controls: controls.map((control) => ({
    name: control.name,
    type: control.type,
    value: control.value,
    checked: control.checked,
    disabled: control.disabled,
    readOnly: control.readOnly,
    checkboxPresence: control.dataset.obCheckboxPresence === '1',
  })),
  values,
}));
`
	cmd := exec.CommandContext(t.Context(), node, "-e", script, "static/managed.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execute managed.js applyElementStates: %v\n%s", err, output)
	}
	var result managedBrowserResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode managed.js result: %v; output=%s", err, output)
	}
	return result
}

func TestСкрытыйФлажок_ЗначениеПереживаетЗапись(t *testing.T) {
	ent := заявкаСФлажком(`СтадияОформления = "Принята"`, "")
	srv, id := заявкаСВзведённымФлажком(t, ent, "Принята")

	html := отрисоватьЗаявкуСФлажком(t, ent, "Принята")
	if strings.Contains(html, `name="_ob_present_Согласовано"`) {
		t.Fatalf("скрытый флажок не должен отрисовывать маркер присутствия:\n%s", html)
	}

	// Ровно то, что уходит с этой формы: ни значения флажка, ни маркера.
	записатьЗаявку(t, srv, ent, id, url.Values{"СтадияОформления": {"Принята"}})

	if got := флажокЗаявки(t, srv, ent, id); !isTruthyStored(got) {
		t.Fatalf("значение скрытого флажка затёрто записью: %#v", got)
	}
}

func TestФлажокПодЗапретом_ЗначениеПереживаетЗапись(t *testing.T) {
	// readonly_when — та же механика: контрол disabled, браузер молчит.
	ent := заявкаСФлажком("", `СтадияОформления = "Принята"`)
	srv, id := заявкаСВзведённымФлажком(t, ent, "Принята")

	rendered := отрисоватьЗаявкуСФлажком(t, ent, "Принята")
	controls := managedCheckboxControls(t, rendered, "ФлагСогласовано")
	marker := managedControlByName(t, controls, "_ob_present_Согласовано")
	if !marker.CheckboxPresence || !marker.Disabled {
		t.Fatalf("маркер нередактируемого флажка должен быть помечен и disabled: %#v", marker)
	}
	if checkbox := managedControlByName(t, controls, "Согласовано"); !checkbox.Disabled {
		t.Fatalf("нередактируемый флажок должен быть disabled: %#v", checkbox)
	}

	записатьЗаявку(t, srv, ent, id, url.Values{"СтадияОформления": {"Принята"}})

	if got := флажокЗаявки(t, srv, ent, id); !isTruthyStored(got) {
		t.Fatalf("значение запертого флажка затёрто записью: %#v", got)
	}
}

func TestВидимыйФлажок_СнятиеГалкиВсёЖеРаботает(t *testing.T) {
	// Обратная сторона: пока флажок на форме есть, отсутствие ключа значит
	// «пользователь снял галку», и снятие обязано работать.
	ent := заявкаСФлажком(`СтадияОформления = "Принята"`, "")
	srv, id := заявкаСВзведённымФлажком(t, ent, "НаОформлении")

	html := отрисоватьЗаявкуСФлажком(t, ent, "НаОформлении")
	if !strings.Contains(html, `name="_ob_present_Согласовано"`) {
		t.Fatalf("видимый флажок должен отрисовать маркер присутствия:\n%s", html)
	}

	записатьЗаявку(t, srv, ent, id, url.Values{
		"СтадияОформления": {"НаОформлении"}, "_ob_present_Согласовано": {"1"}})

	if got := флажокЗаявки(t, srv, ent, id); isTruthyStored(got) {
		t.Fatalf("снятие галки не сработало: %#v", got)
	}
}

func TestФлажокДинамическиЗапертСобытием_НеСбрасываетсяПриЗаписи(t *testing.T) {
	// Начальный server render даёт редактируемый и взведённый флажок. Обработчик
	// переводит запись в принятую стадию, а ответ события включает readonly=true.
	// После реального applyElementStates браузер не должен отправить ни disabled
	// checkbox, ни его presence-marker: marker без checkbox означает «снять галку».
	ent := заявкаСФлажком("", `СтадияОформления = "Принята"`)
	form := ent.Forms[0]
	form.Elements = append(form.Elements, &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "КнопкаПринять",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Принять"},
	})
	form.ProgramAST = mustParse(t, `
Процедура Принять()
	Объект.СтадияОформления = "Принята";
КонецПроцедуры
`)
	srv, id := заявкаСВзведённымФлажком(t, ent, "НаОформлении")

	formRequest := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
	formResponse := httptest.NewRecorder()
	srv.formEdit(formResponse, formRequest)
	if formResponse.Code != http.StatusOK {
		t.Fatalf("публичная отрисовка формы: статус=%d body=%s", formResponse.Code, formResponse.Body.String())
	}
	rendered := formResponse.Body.String()
	controls := managedCheckboxControls(t, rendered, "ФлагСогласовано")
	marker := managedControlByName(t, controls, "_ob_present_Согласовано")
	checkbox := managedControlByName(t, controls, "Согласовано")
	if marker.Disabled || checkbox.Disabled || !checkbox.Checked {
		t.Fatalf("начальная отрисовка должна дать редактируемый взведённый флажок: marker=%#v checkbox=%#v", marker, checkbox)
	}
	if !marker.CheckboxPresence {
		t.Fatalf("presence-marker не помечен для синхронизации с checkbox: %#v", marker)
	}

	eventBody := successfulManagedControls(controls)
	eventBody.Set("_id", id.String())
	eventBody.Set("_element", "КнопкаПринять")
	eventBody.Set("_event", string(metadata.FormEventOnClick))
	eventBody.Set("_kind", "object")
	eventBody.Set("СтадияОформления", "НаОформлении")
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, eventBody).Body.Bytes())
	if !resp.OK {
		t.Fatalf("событие формы завершилось ошибкой: %q", resp.Error)
	}
	if resp.ElementStates == nil || !resp.ElementStates.ReadOnly["ФлагСогласовано"] {
		t.Fatalf("событие не заперло флажок: %#v", resp.ElementStates)
	}
	stage, ok := resp.Values["СтадияОформления"].(string)
	if !ok || stage != "Принята" {
		t.Fatalf("обработчик не изменил стадию: %#v", resp.Values)
	}

	locked := applyManagedElementStatesInNode(t, controls, resp.ElementStates)
	if marker := managedControlByName(t, locked.Controls, "_ob_present_Согласовано"); !marker.Disabled {
		t.Fatalf("applyElementStates не отключил presence-marker: %#v", marker)
	}
	if checkbox := managedControlByName(t, locked.Controls, "Согласовано"); !checkbox.Disabled {
		t.Fatalf("applyElementStates не отключил checkbox: %#v", checkbox)
	}
	if _, present := locked.Values["_ob_present_Согласовано"]; present {
		t.Fatalf("disabled presence-marker попал в browser submit: %#v", locked.Values)
	}
	if _, present := locked.Values["Согласовано"]; present {
		t.Fatalf("disabled checkbox попал в browser submit: %#v", locked.Values)
	}

	// Карта содержит и false: обратный переход обязан вернуть в отправку оба
	// контрола, иначе после динамического unlock снять галку было бы невозможно.
	unlocked := applyManagedElementStatesInNode(t, locked.Controls, &elementStates{
		ReadOnly: map[string]bool{"ФлагСогласовано": false},
	})
	if marker := managedControlByName(t, unlocked.Controls, "_ob_present_Согласовано"); marker.Disabled {
		t.Fatalf("обратный applyElementStates не включил presence-marker: %#v", marker)
	}
	if checkbox := managedControlByName(t, unlocked.Controls, "Согласовано"); checkbox.Disabled {
		t.Fatalf("обратный applyElementStates не включил checkbox: %#v", checkbox)
	}
	if unlocked.Values.Get("_ob_present_Согласовано") != "1" || unlocked.Values.Get("Согласовано") != "true" {
		t.Fatalf("после unlock оба контрола должны снова отправляться: %#v", unlocked.Values)
	}

	locked.Values.Set("СтадияОформления", stage)
	записатьЗаявку(t, srv, ent, id, locked.Values)
	if got := флажокЗаявки(t, srv, ent, id); !isTruthyStored(got) {
		t.Fatalf("динамически запертый флажок затёрт публичной записью: %#v", got)
	}
}

// --- Постоянный запрет вместе с условием -----------------------------------
// Карта состояний применяется клиентом в ОБЕ стороны, поэтому нести она обязана
// итоговое состояние элемента, а не одно условие. У поля, на которое действуют
// сразу постоянный readonly (свой или унаследованный от группы) и readonly_when,
// эти два ответа расходятся ровно тогда, когда условие ложно: сервер рисует поле
// нередактируемым, а голая ложь в карте первым же событием формы его отпирает.
// Постоянный запрет конфигурации снимался бы одним нажатием кнопки на форме,
// причём незаметно — на отрисовке всё правильно, ломается после события.

func заявкаСГруппойПодЗапретом(t *testing.T) *metadata.Entity {
	t.Helper()
	ent := &metadata.Entity{
		Name: "ЗаявкаСГруппой", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Улица", Type: metadata.FieldTypeString},
			{Name: "СтадияОформления", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}
	поле := &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеУлица",
		DataPath: "Объект.Улица", ReadOnlyWhen: `СтадияОформления = "Принята"`,
	}
	// Условие на самой группе линт отклоняет (CheckFormReadOnlyWhen), а вот
	// постоянный readonly у неё законен и наследуется детьми — сюда запрет и
	// приходит.
	группа := &metadata.FormElement{
		Kind: metadata.FormElementGroupBox, Name: "ГруппаРеквизитов",
		ReadOnly: true, Children: []*metadata.FormElement{поле},
	}
	form := managedObjectForm(группа,
		fieldEl("ПолеСтадии", "Объект.СтадияОформления"),
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "КнопкаОтметить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Отметить"},
		})
	form.EntityName = ent.Name
	// Обработчик намеренно НЕ трогает стадию: условие остаётся ложным, и запрет
	// на поле держится только постоянным readonly группы. Событие, меняющее
	// стадию, дефект бы спрятало — условие стало бы истинным само по себе.
	form.ProgramAST = mustParse(t, `
Процедура Отметить()
	Объект.Комментарий = "Отмечено";
КонецПроцедуры
`)
	ent.Forms = []*metadata.FormModule{form}
	return ent
}

// заявкаПодЗапретомПослеСобытия прогоняет публичную цепочку до карты состояний:
// отрисовка карточки → нажатие кнопки. Возвращает контролы поля «Улица» ровно в
// том виде, в каком их отдал шаблон, и ответ события.
//
// Стадия записи — «НаОформлении», то есть условие ЛОЖНО и запрет держится только
// постоянным readonly группы: именно здесь два ответа и расходились.
func заявкаПодЗапретомПослеСобытия(t *testing.T) ([]managedBrowserControl, formEventResponse) {
	t.Helper()
	ent := заявкаСГруппойПодЗапретом(t)
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{
		"Улица": "Ленина 1", "СтадияОформления": "НаОформлении"}, ent); err != nil {
		t.Fatal(err)
	}

	formRequest := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
	formResponse := httptest.NewRecorder()
	srv.formEdit(formResponse, formRequest)
	if formResponse.Code != http.StatusOK {
		t.Fatalf("публичная отрисовка формы: статус=%d body=%s", formResponse.Code, formResponse.Body.String())
	}
	controls := managedCheckboxControls(t, formResponse.Body.String(), "ПолеУлица")
	if улица := managedControlByName(t, controls, "Улица"); !улица.ReadOnly {
		t.Fatalf("сервер обязан отрисовать поле под readonly-группой нередактируемым: %#v", улица)
	}

	eventBody := successfulManagedControls(controls)
	eventBody.Set("_id", id.String())
	eventBody.Set("_element", "КнопкаОтметить")
	eventBody.Set("_event", string(metadata.FormEventOnClick))
	eventBody.Set("_kind", "object")
	eventBody.Set("СтадияОформления", "НаОформлении")
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, eventBody).Body.Bytes())
	if !resp.OK {
		t.Fatalf("событие формы завершилось ошибкой: %q", resp.Error)
	}
	if stage, _ := resp.Values["СтадияОформления"].(string); stage != "НаОформлении" {
		t.Fatalf("обработчик не должен менять стадию — иначе условие станет истинным само: %#v", resp.Values)
	}
	return controls, resp
}

func TestПостоянныйЗапрет_НеСнимаетсяСобытиемФормы(t *testing.T) {
	_, resp := заявкаПодЗапретомПослеСобытия(t)
	if resp.ElementStates == nil || !resp.ElementStates.ReadOnly["ПолеУлица"] {
		t.Fatalf("карта состояний обязана нести итоговый запрет, а не ложное условие: %#v", resp.ElementStates)
	}
}

// Тот же запрет, но проверенный настоящим клиентским кодом. Вынесен отдельным
// тестом намеренно: applyManagedElementStatesInNode без node уходит в t.Skip, а
// он утащил бы за собой и серверную проверку выше — регрессия перестала бы
// сторожиться там, где node нет.
func TestПостоянныйЗапрет_КлиентНеОтпираетПоле(t *testing.T) {
	controls, resp := заявкаПодЗапретомПослеСобытия(t)
	applied := applyManagedElementStatesInNode(t, controls, resp.ElementStates)
	if улица := managedControlByName(t, applied.Controls, "Улица"); !улица.ReadOnly {
		t.Fatalf("applyElementStates снял постоянный запрет с поля: %#v", улица)
	}
}

func TestУсловныйЗапретБезПостоянного_ВсёЕщёСнимается(t *testing.T) {
	// Обратная сторона: там, где постоянного запрета нет, ложное условие обязано
	// по-прежнему отпирать поле, иначе починка выше просто заперла бы всё.
	ent := заявкаСГруппойПодЗапретом(t)
	ent.Forms[0].Elements[0].ReadOnly = false

	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	st := s.formElementStates(ent.Forms[0], ent, map[string]any{"СтадияОформления": "НаОформлении"})
	if st == nil {
		t.Fatal("состояния не рассчитаны, ожидалась карта с ложным условием")
	}
	if v, есть := st.ReadOnly["ПолеУлица"]; !есть || v {
		t.Errorf("ReadOnly[ПолеУлица] = (%v, есть=%v), ожидалось (false, есть=true)", v, есть)
	}
}

// --- Страница внутри СтраницыФормы -----------------------------------------

func заявкаСВкладками(скрытие string) *metadata.Entity {
	ent := заявкаСоСтадией()
	формаСУсловиями(ent, &metadata.FormElement{
		Kind: metadata.FormElementPages, Name: "Ветки",
		Children: []*metadata.FormElement{
			{
				Kind: metadata.FormElementPage, Name: "СтраницаОбзвона",
				TitleMap: map[string]string{"ru": "Обзвон"}, HiddenWhen: скрытие,
				Children: []*metadata.FormElement{fieldEl("ПолеУлица", "Объект.Улица")},
			},
			{
				Kind: metadata.FormElementPage, Name: "СтраницаСогласования",
				TitleMap: map[string]string{"ru": "Согласование"},
				Children: []*metadata.FormElement{fieldEl("ПолеСтадии", "Объект.СтадияОформления")},
			},
		},
	})
	return ent
}

func TestСкрытаяСтраница_НиКнопкиВкладкиНиСодержимого(t *testing.T) {
	// Самый естественный способ собрать сценарий «у задачи видна только своя
	// ветка» — вкладки. Ветка СтраницыФормы обходит детей сама, поэтому шаблон
	// элемента для самой страницы не вызывается и hidden_when для неё никто не
	// спрашивал: страница показывалась целиком, вместе с кнопкой вкладки.
	ent := заявкаСВкладками(`СтадияОформления = "Принята"`)

	черновик := отрисоватьСУсловиями(t, ent, ent.Forms[0],
		map[string]string{"СтадияОформления": "НаОформлении"})
	if !strings.Contains(черновик, "Обзвон") || !strings.Contains(черновик, `data-tab-idx="1"`) {
		t.Fatalf("черновик: обе вкладки должны быть видны\n%s", черновик)
	}

	принята := отрисоватьСУсловиями(t, ent, ent.Forms[0],
		map[string]string{"СтадияОформления": "Принята"})
	if strings.Contains(принята, "Обзвон") {
		t.Errorf("принятая заявка: кнопка скрытой вкладки не должна отрисовываться\n%s", принята)
	}
	if strings.Contains(принята, `data-ob-el="ПолеУлица"`) {
		t.Errorf("принятая заявка: содержимое скрытой вкладки не должно отрисовываться\n%s", принята)
	}
	// Нумерация оставшихся вкладок обязана остаться сплошной: кнопка и
	// содержимое связаны индексом, а активна и раскрыта всегда нулевая.
	if strings.Contains(принята, `data-tab-idx="1"`) || strings.Contains(принята, `data-tab-content="1"`) {
		t.Errorf("после скрытия первой вкладки нумерация разошлась\n%s", принята)
	}
	if !strings.Contains(принята, `class="managed-tab-btn active" data-tab-idx="0"`) {
		t.Errorf("оставшаяся вкладка должна быть активной\n%s", принята)
	}
	if !strings.Contains(принята, `data-tab-content="0" style="display:block"`) {
		t.Errorf("содержимое оставшейся вкладки должно быть раскрыто\n%s", принята)
	}
}

// Кнопка «Открыть карточку» (🔍) у ссылочного поля — единственный контрол внутри
// якоря, который серверная отрисовка под запретом намеренно оставляет РАБОЧИМ
// (templates_managed.go, ветка ссылочного поля): посмотреть связанный объект —
// не редактирование, и на нередактируемом поле переход к нему нужен чаще всего.
// Клиентский пересчёт после события формы обязан приходить к тому же виду, иначе
// первое же нажатие кнопки формы отбирает переход к данным до перезагрузки
// страницы (#1210).

// managedAnchorNode — элемент внутри якоря data-ob-el, как его отрисовал сервер.
// Атрибуты сохраняются целиком: селектор из managed.js в стабе ниже проверяется
// по-настоящему, а не сверкой со строкой.
type managedAnchorNode struct {
	Tag      string            `json:"tag"`
	Attrs    map[string]string `json:"attrs"`
	Disabled bool              `json:"disabled"`
	ReadOnly bool              `json:"readOnly"`
}

func managedAnchorNodes(t *testing.T, rendered, elementName string) []managedAnchorNode {
	t.Helper()
	var nodes []managedAnchorNode
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.ElementNode {
			attrs := make(map[string]string, len(n.Attr))
			for _, a := range n.Attr {
				attrs[a.Key] = a.Val
			}
			_, disabled := attrs["disabled"]
			_, readOnly := attrs["readonly"]
			nodes = append(nodes, managedAnchorNode{
				Tag: n.Data, Attrs: attrs, Disabled: disabled, ReadOnly: readOnly})
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	anchor := managedElementAnchor(t, rendered, elementName)
	for child := anchor.FirstChild; child != nil; child = child.NextSibling {
		collect(child)
	}
	return nodes
}

func managedAnchorNodeBy(t *testing.T, nodes []managedAnchorNode, attr string) managedAnchorNode {
	t.Helper()
	for _, n := range nodes {
		if _, ok := n.Attrs[attr]; ok {
			return n
		}
	}
	t.Fatalf("в разметке нет элемента с атрибутом %q: %#v", attr, nodes)
	return managedAnchorNode{}
}

// applyManagedElementStatesToAnchor прогоняет настоящий applyElementStates из
// managed.js над узлами якоря. querySelectorAll в стабе разбирает ПОЛУЧЕННЫЙ
// селектор, а не сверяет его с ожидаемой строкой: иначе тест проверял бы стаб, а
// не продакшен-код, и правка селектора проехала бы молча зелёной. Неизвестный
// стабу синтаксис — исключение, то есть красный тест.
func applyManagedElementStatesToAnchor(t *testing.T, nodes []managedAnchorNode, states *elementStates) []managedAnchorNode {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for managed element state integration test")
	}
	payload, err := json.Marshal(struct {
		Nodes  []managedAnchorNode `json:"nodes"`
		States *elementStates      `json:"states"`
	}{Nodes: nodes, States: states})
	if err != nil {
		t.Fatal(err)
	}

	const script = `
const fs = require('node:fs');
const source = fs.readFileSync(process.argv[1], 'utf8');
function extract(name) {
  const start = source.indexOf('function ' + name);
  if (start < 0) throw new Error('managed.js has no function ' + name);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') {
      depth--;
      if (depth === 0) return source.slice(start, i + 1);
    }
  }
  throw new Error('unterminated function ' + name);
}
// Разбор простого селектора: имя тега, [атрибут] и :not([атрибут]), через
// запятую. Больше managed.js внутри якоря не использует; всё прочее — ошибка.
function matchesCompound(node, selector) {
  let rest = selector.trim();
  if (!rest) throw new Error('пустой селектор');
  let ok = true;
  while (rest.length) {
    let m;
    if ((m = /^([a-zA-Z][\w-]*)/.exec(rest))) ok = ok && node.tagName === m[1].toUpperCase();
    else if ((m = /^\[([\w-]+)\]/.exec(rest))) ok = ok && node.hasAttribute(m[1]);
    else if ((m = /^:not\(\[([\w-]+)\]\)/.exec(rest))) ok = ok && !node.hasAttribute(m[1]);
    else throw new Error('стаб DOM не разбирает селектор: ' + selector);
    rest = rest.slice(m[0].length);
  }
  return ok;
}
const payload = JSON.parse(fs.readFileSync(0, 'utf8'));
const nodes = payload.nodes.map((n) => ({
  tagName: n.tag.toUpperCase(),
  type: n.attrs.type || '',
  disabled: n.disabled,
  readOnly: n.readOnly,
  dataset: n.attrs['data-ob-checkbox-presence'] === '1' ? {obCheckboxPresence: '1'} : {},
  attrs: n.attrs,
  hasAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attrs, name); },
}));
const anchor = {
  tagName: 'DIV',
  style: {},
  querySelectorAll(selector) {
    return nodes.filter((n) => selector.split(',').some((part) => matchesCompound(n, part)));
  },
};
global.window = {CSS: null};
global.document = {querySelector() { return anchor; }};
const applyElementStates = new Function(
  extract('applyElementStates') + '\nreturn applyElementStates;'
)();
applyElementStates(payload.states);
process.stdout.write(JSON.stringify(nodes.map((n) => ({
  tag: n.tagName.toLowerCase(),
  attrs: n.attrs,
  disabled: !!n.disabled,
  readOnly: !!n.readOnly,
}))));
`
	cmd := exec.CommandContext(t.Context(), node, "-e", script, "static/managed.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execute managed.js applyElementStates: %v\n%s", err, output)
	}
	var result []managedAnchorNode
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode managed.js result: %v; output=%s", err, output)
	}
	return result
}

func заявкаСоСсылкойНаКлиента() *metadata.Entity {
	return &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Клиент", Type: metadata.FieldType("reference:Клиенты"), RefEntity: "Клиенты"},
		{Name: "СтадияОформления", Type: metadata.FieldTypeString},
	}}
}

func отрисоватьЗаявкуСКлиентом(t *testing.T, ent *metadata.Entity, стадия string) string {
	t.Helper()
	return отрисоватьСВариантами(t, ent, ent.Forms[0],
		map[string]string{"Клиент": "cl-1", "СтадияОформления": стадия},
		map[string]any{"Клиент": []map[string]any{{"id": "cl-1", "_label": "ООО Ромашка"}}})
}

func TestЗапретСобытием_ПереходККарточкеОстаётсяРабочим(t *testing.T) {
	ent := заявкаСоСсылкойНаКлиента()
	формаСУсловиями(ent, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеКлиент",
		DataPath: "Объект.Клиент", ReadOnlyWhen: `СтадияОформления = "Принята"`,
	})

	// Отправная точка — черновик: условие ложно, сервер отрисовал поле
	// редактируемым, обе кнопки рабочие.
	черновик := managedAnchorNodes(t, отрисоватьЗаявкуСКлиентом(t, ent, "НаОформлении"), "ПолеКлиент")
	подбор := managedAnchorNodeBy(t, черновик, "data-ob-ref-picker")
	карточка := managedAnchorNodeBy(t, черновик, "data-ob-ref-current")
	if подбор.Disabled || карточка.Disabled {
		t.Fatalf("черновик: обе кнопки должны быть рабочими: подбор=%#v карточка=%#v", подбор, карточка)
	}

	// Событие формы перевело заявку в принятую стадию — сервер прислал запрет.
	заперто := applyManagedElementStatesToAnchor(t, черновик,
		&elementStates{ReadOnly: map[string]bool{"ПолеКлиент": true}})
	if !managedAnchorNodeBy(t, заперто, "data-ref-entity").Disabled {
		t.Errorf("нередактируемое поле-ссылка должно быть disabled: %#v", заперто)
	}
	if !managedAnchorNodeBy(t, заперто, "data-ob-ref-picker").Disabled {
		t.Errorf("кнопка подбора под запретом должна быть disabled: %#v", заперто)
	}
	if кнопка := managedAnchorNodeBy(t, заперто, "data-ob-ref-current"); кнопка.Disabled {
		t.Errorf("«Открыть карточку» под запретом обязана остаться рабочей — сервер рисует её без disabled: %#v", кнопка)
	}

	// Та же страница, отрисованная сервером при выполненном условии: клиент
	// обязан приходить ровно к ней, иначе вид формы зависит от того, дошёл ли
	// пользователь до кнопки или перезагрузил страницу.
	серверный := managedAnchorNodes(t, отрисоватьЗаявкуСКлиентом(t, ent, "Принята"), "ПолеКлиент")
	if кнопка := managedAnchorNodeBy(t, серверный, "data-ob-ref-current"); кнопка.Disabled {
		t.Fatalf("серверная отрисовка под запретом не должна гасить «Открыть карточку»: %#v", кнопка)
	}

	// Обратный ход: условие перестало выполняться — подбор возвращается.
	отперто := applyManagedElementStatesToAnchor(t, заперто,
		&elementStates{ReadOnly: map[string]bool{"ПолеКлиент": false}})
	if managedAnchorNodeBy(t, отперто, "data-ob-ref-picker").Disabled {
		t.Errorf("снятие условия должно вернуть кнопку подбора: %#v", отперто)
	}
	if кнопка := managedAnchorNodeBy(t, отперто, "data-ob-ref-current"); кнопка.Disabled {
		t.Errorf("«Открыть карточку» не должна остаться серой после снятия условия: %#v", кнопка)
	}
	if поле := managedAnchorNodeBy(t, отперто, "data-ref-entity"); поле.Disabled {
		t.Errorf("снятие условия должно вернуть само поле: %#v", поле)
	}
}
