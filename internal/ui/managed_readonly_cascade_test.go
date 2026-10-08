package ui

import (
	"bytes"
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

// Каскад условной нередактируемости (#1184). Статический readonly группы
// наследуется её потомками с самого начала; условный readonly_when — нет, и
// одна и та же мысль давала два разных ответа. Типовой сценарий учётной формы —
// «после проведения вся группа реквизитов замерзает» — приходилось писать
// условием на каждом реквизите: добавил в группу поле, забыл условие, и в
// «замороженной» форме одно поле осталось живым.
//
// Проверяется через публичный путь: отрисовка карточки (formEdit) и событие
// формы (handleManagedFormEvent), а клиентская половина — настоящим
// applyElementStates из managed.js.

// путьСостояния — ключ элемента в карте состояний ответа события: путь
// размещения в дереве формы, а не имя (#1543).
func путьСостояния(t *testing.T, form *metadata.FormModule, имя string) string {
	t.Helper()
	var найдено string
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		if visit.element != nil && visit.element.Name == имя && найдено == "" {
			найдено = visit.path
		}
	})
	if найдено == "" {
		t.Fatalf("в форме нет элемента %q", имя)
	}
	return найдено
}

func заявкаСУсловнойГруппой(t *testing.T, статическийЗапретПоля bool) *metadata.Entity {
	t.Helper()
	ent := &metadata.Entity{
		Name: "ЗаявкаКаскад", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Улица", Type: metadata.FieldTypeString},
			{Name: "СтадияОформления", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}
	поле := &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеУлица",
		DataPath: "Объект.Улица", ReadOnly: статическийЗапретПоля,
	}
	кнопкаВГруппе := &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "КнопкаВГруппе",
	}
	группа := &metadata.FormElement{
		Kind: metadata.FormElementGroupBox, Name: "ГруппаРеквизитов",
		ReadOnlyWhen: `СтадияОформления = "Принята"`,
		Children:     []*metadata.FormElement{поле, кнопкаВГруппе},
	}
	form := managedObjectForm(группа,
		fieldEl("ПолеКомментария", "Объект.Комментарий"),
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "КнопкаОтметить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Отметить"},
		})
	form.EntityName = ent.Name
	// Обработчик намеренно НЕ трогает стадию: условие остаётся тем же, каким
	// было при отрисовке. Событие, меняющее стадию, спрятало бы расхождение —
	// состояние поменялось бы по делу, а не из-за разъехавшихся правил.
	form.ProgramAST = mustParse(t, `
Процедура Отметить()
	Объект.Комментарий = "Отмечено";
КонецПроцедуры
`)
	ent.Forms = []*metadata.FormModule{form}
	return ent
}

func заявкаСНавигациейВУсловнойГруппе(t *testing.T) *metadata.Entity {
	t.Helper()
	ent := заявкаСУсловнойГруппой(t, false)
	ent.Fields = append(ent.Fields, metadata.Field{
		Name: "Клиент", Type: metadata.FieldType("reference:" + ent.Name), RefEntity: ent.Name,
	})
	группа := ent.Forms[0].Elements[0]
	группа.Children = []*metadata.FormElement{
		группа.Children[0],
		fieldEl("ПолеКлиента", "Объект.Клиент"),
		{
			Kind: metadata.FormElementPages, Name: "ВкладкиВГруппе",
			Children: []*metadata.FormElement{
				{Kind: metadata.FormElementPage, Name: "ОсновнаяСтраница", TitleMap: map[string]string{"ru": "Основное"}},
				{Kind: metadata.FormElementPage, Name: "ДополнительнаяСтраница", TitleMap: map[string]string{"ru": "Дополнительно"}},
			},
		},
		группа.Children[1],
	}
	return ent
}

// каскадДоИПосле прогоняет цепочку «отрисовка карточки → нажатие кнопки» и
// возвращает разметку формы и ответ события — то есть оба ответа на вопрос
// «редактируемо ли поле», которые обязаны совпадать.
func каскадДоИПосле(t *testing.T, ent *metadata.Entity, стадия string) (string, formEventResponse) {
	t.Helper()
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	values := map[string]any{"Улица": "Ленина 1", "СтадияОформления": стадия}
	for _, field := range ent.Fields {
		if field.Name == "Клиент" {
			values[field.Name] = id.String()
		}
	}
	if err := srv.store.Upsert(ctx, ent.Name, id, values, ent); err != nil {
		t.Fatal(err)
	}

	formRequest := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
	formResponse := httptest.NewRecorder()
	srv.formEdit(formResponse, formRequest)
	if formResponse.Code != http.StatusOK {
		t.Fatalf("публичная отрисовка формы: статус=%d body=%s", formResponse.Code, formResponse.Body.String())
	}
	rendered := formResponse.Body.String()

	eventBody := managedFormSuccessfulValues(t, rendered)
	eventBody.Set("_id", id.String())
	eventBody.Set("_element", "КнопкаОтметить")
	eventBody.Set("_event", string(metadata.FormEventOnClick))
	eventBody.Set("_kind", "object")
	eventBody.Set("СтадияОформления", стадия)
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, eventBody).Body.Bytes())
	if !resp.OK {
		t.Fatalf("событие формы завершилось ошибкой: %q", resp.Error)
	}
	if got, _ := resp.Values["СтадияОформления"].(string); got != стадия {
		t.Fatalf("обработчик не должен менять стадию: %#v", resp.Values)
	}
	return rendered, resp
}

func TestКаскадУсловногоЗапрета_ГруппаЗамораживаетПотомков(t *testing.T) {
	ent := заявкаСУсловнойГруппой(t, false)

	rendered, _ := каскадДоИПосле(t, ent, "Принята")
	dom := managedFormDOM(t, rendered)
	if улица := dom.control(t, "Улица"); !улица.ReadOnly {
		t.Fatalf("истинное условие группы обязано запереть поле внутри неё: %#v", улица)
	}

	черновик, _ := каскадДоИПосле(t, ent, "НаОформлении")
	if улица := managedFormDOM(t, черновик).control(t, "Улица"); улица.ReadOnly {
		t.Fatalf("при ложном условии группы поле обязано остаться редактируемым: %#v", улица)
	}
}

func TestКаскадУсловногоЗапрета_КартаСостоянийНесётПотомков(t *testing.T) {
	// Карта — единственное, что видит клиент. Без строки на потомка он не смог
	// бы ни запереть его, ни отпереть: имя группы ему ничего не говорит.
	ent := заявкаСУсловнойГруппой(t, false)

	_, принята := каскадДоИПосле(t, ent, "Принята")
	if принята.ElementStates == nil || !принята.ElementStates.ReadOnly[путьСостояния(t, ent.Forms[0], "ПолеУлица")] {
		t.Fatalf("карта обязана нести запрет потомка: %#v", принята.ElementStates)
	}

	_, черновик := каскадДоИПосле(t, ent, "НаОформлении")
	if черновик.ElementStates == nil {
		t.Fatal("карта состояний не рассчитана")
	}
	if v, есть := черновик.ElementStates.ReadOnly[путьСостояния(t, ent.Forms[0], "ПолеУлица")]; !есть || v {
		t.Fatalf("ReadOnly[ПолеУлица] = (%v, есть=%v), ожидалось (false, есть=true): "+
			"без явного «ложно» клиенту нечем снять запрет", v, есть)
	}
}

// kind: Кнопка сама является data-ob-el-якорем: querySelectorAll не включает
// корневой узел. Поэтому проверяем не только одинаковое состояние до/после
// события, но и настоящий переход в обе стороны на боевом applyElementStates.
func TestКаскадУсловногоЗапрета_КнопкаВГруппеМеняетСостояниеНаКлиенте(t *testing.T) {
	ent := заявкаСУсловнойГруппой(t, false)
	черновикHTML, черновикResp := каскадДоИПосле(t, ent, "НаОформлении")
	принятаHTML, принятаResp := каскадДоИПосле(t, ent, "Принята")

	черновик := managedFormDOM(t, черновикHTML)
	if кнопка := черновик.button(t, "КнопкаВГруппе"); кнопка.Disabled {
		t.Fatalf("кнопка в доступной группе отрисована неактивной: %#v", кнопка)
	}
	if принятаResp.ElementStates == nil || !принятаResp.ElementStates.ReadOnly[путьСостояния(t, ent.Forms[0], "КнопкаВГруппе")] {
		t.Fatalf("карта обязана нести запрет кнопки-потомка: %#v", принятаResp.ElementStates)
	}

	заморожено := применитьСостоянияВБраузере(t, черновик, принятаResp.ElementStates)
	if кнопка := заморожено.button(t, "КнопкаВГруппе"); !кнопка.Disabled {
		t.Fatalf("applyElementStates не отключил корневую кнопку: %#v", кнопка)
	}
	сверитьДоступность(t, managedFormDOM(t, принятаHTML), заморожено)

	if черновикResp.ElementStates == nil {
		t.Fatal("карта состояний доступной группы не рассчитана")
	}
	разморожено := применитьСостоянияВБраузере(t, заморожено, черновикResp.ElementStates)
	if кнопка := разморожено.button(t, "КнопкаВГруппе"); кнопка.Disabled {
		t.Fatalf("applyElementStates не включил корневую кнопку обратно: %#v", кнопка)
	}
}

// Кнопка перехода к выбранной ссылке и заголовки вкладок — навигация, а не
// редактирование. Сервер оставляет их активными даже внутри readonly-группы;
// обработка первого события обязана сохранить ту же классификацию.
func TestКаскадУсловногоЗапрета_НавигацияОстаётсяДоступнойПослеСобытия(t *testing.T) {
	ent := заявкаСНавигациейВУсловнойГруппе(t)
	rendered, resp := каскадДоИПосле(t, ent, "Принята")

	до := managedFormDOM(t, rendered)
	var ссылки, вкладки int
	for _, control := range до.Controls {
		if control.RefCurrent {
			ссылки++
			if control.Disabled {
				t.Fatalf("сервер отключил навигацию к выбранной ссылке: %#v", control)
			}
		}
		if control.TabButton {
			вкладки++
			if control.Disabled {
				t.Fatalf("сервер отключил заголовок вкладки: %#v", control)
			}
		}
	}
	if ссылки != 1 || вкладки != 2 {
		t.Fatalf("ожидались одна кнопка ссылки и две вкладки, получено ссылки=%d вкладки=%d: %#v",
			ссылки, вкладки, до.Controls)
	}

	после := применитьСостоянияВБраузере(t, до, resp.ElementStates)
	for _, control := range после.Controls {
		if (control.RefCurrent || control.TabButton) && control.Disabled {
			t.Errorf("applyElementStates отключил навигационный контрол: %#v", control)
		}
	}
	сверитьДоступность(t, до, после)
}

// Ключевое требование заявки: до и после первого события формы результат
// одинаков. Раньше он был противоположным — сервер оставлял дочернее поле
// редактируемым, а клиент после первого же события запирал всех потомков
// обходом DOM.
func TestКаскадУсловногоЗапрета_ДоИПослеСобытияОдинаково(t *testing.T) {
	for _, стадия := range []string{"Принята", "НаОформлении"} {
		t.Run(стадия, func(t *testing.T) {
			rendered, resp := каскадДоИПосле(t, заявкаСУсловнойГруппой(t, false), стадия)
			до := managedFormDOM(t, rendered)
			после := применитьСостоянияВБраузере(t, до, resp.ElementStates)
			сверитьДоступность(t, до, после)
		})
	}
}

// Статический readonly потомка при ложном условии предка не снимается ни при
// каких обстоятельствах. Обход потомков на клиенте отпирал именно его: сервер
// рисовал поле нередактируемым навсегда, а первое событие формы возвращало
// «условие группы ложно» — и постоянный запрет конфигурации снимался.
func TestКаскадУсловногоЗапрета_СтатическийЗапретПотомкаНеСнимается(t *testing.T) {
	ent := заявкаСУсловнойГруппой(t, true)
	rendered, resp := каскадДоИПосле(t, ent, "НаОформлении")

	до := managedFormDOM(t, rendered)
	if улица := до.control(t, "Улица"); !улица.ReadOnly {
		t.Fatalf("поле со статическим readonly обязано быть отрисовано нередактируемым: %#v", улица)
	}
	if resp.ElementStates == nil || !resp.ElementStates.ReadOnly[путьСостояния(t, ent.Forms[0], "ПолеУлица")] {
		t.Fatalf("карта обязана нести итоговый запрет, а не ложное условие предка: %#v", resp.ElementStates)
	}

	после := применитьСостоянияВБраузере(t, до, resp.ElementStates)
	if улица := после.control(t, "Улица"); !улица.ReadOnly {
		t.Fatalf("applyElementStates снял статический запрет с потомка: %#v", улица)
	}
	// Соседнее поле вне группы условие не касается — оно обязано остаться живым,
	// иначе «починка» свелась бы к запиранию всей формы.
	if комментарий := после.control(t, "Комментарий"); комментарий.ReadOnly || комментарий.Disabled {
		t.Fatalf("поле вне условной группы заперто зря: %#v", комментарий)
	}
}

// Табличная часть внутри замороженной группы приезжает нередактируемой уже с
// сервера: у ТЧ нет якоря data-ob-el, и клиент её не трогает — расчёт обязан
// быть сделан на отрисовке, а не отложен до первого события.
func TestКаскадУсловногоЗапрета_ТабличнаяЧастьВГруппе(t *testing.T) {
	ent := &metadata.Entity{
		Name: "ЗаявкаКаскадТЧ", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "СтадияОформления", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		}}},
	}
	группа := &metadata.FormElement{
		Kind: metadata.FormElementGroupBox, Name: "ГруппаРеквизитов",
		ReadOnlyWhen: `СтадияОформления = "Принята"`,
		Children: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "ТЧТовары", DataPath: "Объект.Товары",
		}},
	}
	form := формаСУсловиями(ent, группа)

	принята := отрисоватьФормуСТЧ(t, ent, form, "Принята")
	if !strings.Contains(принята, `data-sg-ro="1"`) {
		t.Fatalf("ТЧ в замороженной группе обязана быть отрисована нередактируемой:\n%s", принята)
	}
	черновик := отрисоватьФормуСТЧ(t, ent, form, "НаОформлении")
	if strings.Contains(черновик, `data-sg-ro="1"`) {
		t.Fatalf("при ложном условии группы ТЧ обязана остаться редактируемой:\n%s", черновик)
	}
}

// Обратная сторона того же каскада: запертую по своей причине табличную часть
// ложное условие родительской группы отпирать не должно. Клиент правит контролы
// по карте состояний, а в ней нет ни права на запись, ни статического запрета
// самой ТЧ — поэтому её содержимое он не трогает вовсе, и «+ Добавить строку»
// остаётся неактивной.
func TestКаскадУсловногоЗапрета_ТабличнаяЧастьНеОтпираетсяГруппой(t *testing.T) {
	ent := &metadata.Entity{
		Name: "ЗаявкаКаскадТЧЗапрет", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "СтадияОформления", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		}}},
	}
	группа := &metadata.FormElement{
		Kind: metadata.FormElementGroupBox, Name: "ГруппаРеквизитов",
		ReadOnlyWhen: `СтадияОформления = "Принята"`,
		Children: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "ТЧТовары",
			DataPath: "Объект.Товары", ReadOnly: true, NoGrid: true,
		}},
	}
	form := формаСУсловиями(ent, группа)

	// Условие ЛОЖНО: запрет держится только статическим readonly самой ТЧ.
	rendered := отрисоватьФормуСТЧ(t, ent, form, "НаОформлении")
	до := managedFormDOM(t, rendered)
	var заперто int
	for _, c := range до.Controls {
		if c.InTablePart && c.Disabled {
			заперто++
		}
	}
	if заперто == 0 {
		t.Fatalf("нередактируемая ТЧ обязана отрисовать неактивные контролы:\n%s", rendered)
	}

	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	states := s.formElementStates(form, ent, map[string]any{"СтадияОформления": "НаОформлении"}, true)
	сверитьДоступность(t, до, применитьСостоянияВБраузере(t, до, states))
}

// отрисоватьФормуСТЧ — та же отрисовка, что и отрисоватьСУсловиями, но с правом
// записи и строками табличной части: без CanWrite ветка ТЧ нередактируема
// всегда, и ложное условие ничего бы не доказало.
func отрисоватьФормуСТЧ(t *testing.T, ent *metadata.Entity, form *metadata.FormModule, стадия string) string {
	t.Helper()
	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": false, "CanWrite": true,
		"Values":      map[string]string{"СтадияОформления": стадия},
		"RefOptions":  map[string]any{},
		"EnumOptions": map[string][]EnumOption{}, "TPRefOptions": map[string]any{},
		"TPEnumLabels":   map[string]any{},
		"TablePartRows":  map[string][]map[string]any{"Товары": {}},
		"User":           nil,
		"Lang":           "ru",
		"FormWarningsOK": true,
	}
	s.prepareManagedFormData(t.Context(), data, form)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

// --- Модель разметки формы для клиентской половины --------------------------

// managedControlNode — контрол ровно в том виде, в каком его отдал шаблон, плюс
// цепочка якорей data-ob-el от корня к ближайшему и признак «внутри табличной
// части». Больше про DOM applyElementStates ничего и не спрашивает: он ищет
// элемент по якорю и отличает свой контрол от контрола вложенного элемента.
type managedControlNode struct {
	Tag                string   `json:"tagName"`
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	Value              string   `json:"value"`
	Checked            bool     `json:"checked"`
	Disabled           bool     `json:"disabled"`
	ReadOnly           bool     `json:"readOnly"`
	CheckboxPresence   bool     `json:"checkboxPresence"`
	ReadOnlyNavigation bool     `json:"readOnlyNavigation"`
	RefCurrent         bool     `json:"refCurrent"`
	ROMirror           bool     `json:"roMirror"`
	TabButton          bool     `json:"tabButton"`
	Anchors            []string `json:"anchors"`
	InTablePart        bool     `json:"inTablePart"`
}

type managedFormDOMModel struct {
	Controls []managedControlNode `json:"controls"`
	// Anchors — якорь → тег элемента: кнопка (kind: Кнопка) сама себе контрол, и
	// applyElementStates гасит её напрямую, а не через потомков.
	Anchors map[string]string `json:"anchors"`
	// AnchorPaths — якорь → путь размещения (data-ob-el-path): состояния в
	// ответе события ключуются путём, а не именем (#1543).
	AnchorPaths map[string]string `json:"anchorPaths"`
}

func (m managedFormDOMModel) control(t *testing.T, name string) managedControlNode {
	t.Helper()
	for _, c := range m.Controls {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("в разметке формы нет контрола %q: %#v", name, m.Controls)
	return managedControlNode{}
}

func (m managedFormDOMModel) button(t *testing.T, elementName string) managedControlNode {
	t.Helper()
	for _, c := range m.Controls {
		if c.Tag == "BUTTON" && len(c.Anchors) > 0 && c.Anchors[len(c.Anchors)-1] == elementName {
			return c
		}
	}
	t.Fatalf("в разметке формы нет кнопки элемента %q: %#v", elementName, m.Controls)
	return managedControlNode{}
}

// managedFormDOM разбирает НАСТОЯЩУЮ разметку управляемой формы. Модель строится
// из неё, а не пишется руками: рукописная разметка закрепила бы предположение о
// шаблоне, а не сам шаблон.
func managedFormDOM(t *testing.T, rendered string) managedFormDOMModel {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatalf("parse managed form HTML: %v", err)
	}
	model := managedFormDOMModel{Anchors: map[string]string{}, AnchorPaths: map[string]string{}}
	var walk func(n *html.Node, anchors []string, inTP bool)
	walk = func(n *html.Node, anchors []string, inTP bool) {
		if n.Type == html.ElementNode {
			if _, ok := managedHTMLAttr(n, "data-ob-tp"); ok {
				inTP = true
			}
			if name, ok := managedHTMLAttr(n, "data-ob-el"); ok {
				anchors = append(append([]string{}, anchors...), name)
				model.Anchors[name] = strings.ToUpper(n.Data)
				if path, ok := managedHTMLAttr(n, "data-ob-el-path"); ok {
					model.AnchorPaths[name] = path
				}
			}
			switch n.Data {
			case "input", "textarea", "select", "button":
				name, _ := managedHTMLAttr(n, "name")
				typeName, _ := managedHTMLAttr(n, "type")
				value, _ := managedHTMLAttr(n, "value")
				_, checked := managedHTMLAttr(n, "checked")
				_, disabled := managedHTMLAttr(n, "disabled")
				_, readOnly := managedHTMLAttr(n, "readonly")
				presence, _ := managedHTMLAttr(n, "data-ob-checkbox-presence")
				_, readOnlyNavigation := managedHTMLAttr(n, "data-ob-readonly-navigation")
				_, refCurrent := managedHTMLAttr(n, "data-ob-ref-current")
				_, tabButton := managedHTMLAttr(n, "data-tab-idx")
				roMirrorVal, _ := managedHTMLAttr(n, "data-ob-ro-mirror")
				model.Controls = append(model.Controls, managedControlNode{
					Tag: strings.ToUpper(n.Data), Name: name, Type: typeName, Value: value,
					Checked: checked, Disabled: disabled, ReadOnly: readOnly,
					CheckboxPresence: presence == "1", ReadOnlyNavigation: readOnlyNavigation,
					RefCurrent: refCurrent, TabButton: tabButton, Anchors: anchors, InTablePart: inTP,
					ROMirror: roMirrorVal == "1",
				})
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, anchors, inTP)
		}
	}
	walk(doc, nil, false)
	return model
}

// managedFormSuccessfulValues — то, что браузер отправит с этой формы:
// успешными считаются только не-disabled контролы.
func managedFormSuccessfulValues(t *testing.T, rendered string) url.Values {
	t.Helper()
	values := make(url.Values)
	for _, c := range managedFormDOM(t, rendered).Controls {
		if c.Name == "" || c.Disabled || c.Tag == "BUTTON" {
			continue
		}
		if (c.Type == "checkbox" || c.Type == "radio") && !c.Checked {
			continue
		}
		values.Add(c.Name, c.Value)
	}
	return values
}

func сверитьДоступность(t *testing.T, до, после managedFormDOMModel) {
	t.Helper()
	if len(до.Controls) != len(после.Controls) {
		t.Fatalf("модель формы изменилась: было %d контролов, стало %d", len(до.Controls), len(после.Controls))
	}
	for i, было := range до.Controls {
		стало := после.Controls[i]
		// Зеркало значения (#1672) намеренно переключает disabled вместе с
		// запретом: активно только пока select не отправляется. Связку
		// «запрет ↔ зеркало» сторожит behavior-тест managed_ro_mirror.
		if было.ROMirror || стало.ROMirror {
			continue
		}
		if было.ReadOnly != стало.ReadOnly || было.Disabled != стало.Disabled {
			t.Errorf("контрол %q: до события readOnly=%v disabled=%v, после — readOnly=%v disabled=%v",
				было.Name, было.ReadOnly, было.Disabled, стало.ReadOnly, стало.Disabled)
		}
	}
}

// применитьСостоянияВБраузере исполняет applyElementStates, вырезанный из
// боевого managed.js, над моделью настоящей разметки. Подменять реализацию
// нельзя: расхождение сервера с клиентом — ровно тот дефект, который сторожат
// эти тесты.
func применитьСостоянияВБраузере(t *testing.T, dom managedFormDOMModel, states *elementStates) managedFormDOMModel {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed readonly cascade test")
	}
	payload, err := json.Marshal(struct {
		DOM    managedFormDOMModel `json:"dom"`
		States *elementStates      `json:"states"`
	}{DOM: dom, States: states})
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
const anchorNodes = {};
const controls = payload.dom.controls.map((c) => ({
  tagName: c.tagName,
  name: c.name,
  type: c.type,
  value: c.value,
  checked: c.checked,
  disabled: c.disabled,
  readOnly: c.readOnly,
  dataset: Object.assign(
    c.checkboxPresence ? {obCheckboxPresence: '1'} : {},
    c.readOnlyNavigation ? {obReadonlyNavigation: '1'} : {}
  ),
  _refCurrent: c.refCurrent,
  _tabButton: c.tabButton,
  _anchors: c.anchors || [],
  _inTablePart: c.inTablePart,
  // Ровно два селектора, которые спрашивает applyElementStates.
  closest(selector) {
    if (selector === '[data-ob-tp]') return this._inTablePart ? {} : null;
    if (selector === '[data-ob-el]') {
      return this._anchors.length ? anchor(this._anchors[this._anchors.length - 1]) : null;
    }
    throw new Error('unexpected selector ' + selector);
  },
}));
function anchor(name) {
  if (anchorNodes[name]) return anchorNodes[name];
  const tag = payload.dom.anchors[name];
  // Кнопка — сама себе якорь: applyElementStates гасит её напрямую.
  const own = tag === 'BUTTON' ? controls.find((c) => (c._anchors[c._anchors.length - 1] === name)) : null;
  const node = own || {tagName: tag, style: {}};
  node.querySelectorAll = function (selector) {
    const tags = selector === 'input, textarea' ? ['INPUT', 'TEXTAREA'] : ['SELECT', 'BUTTON'];
    return controls.filter((c) => c._anchors.includes(name) && tags.includes(c.tagName) && c !== node);
  };
  if (!node.style) node.style = {};
  anchorNodes[name] = node;
  return node;
}
global.window = {CSS: null};
global.document = {
  querySelector(selector) {
    let m = /^\[data-ob-el-path="(.*)"\]$/.exec(selector);
    if (m) {
      for (const [name, path] of Object.entries(payload.dom.anchorPaths || {})) {
        if (path === m[1]) return anchor(name);
      }
      return null;
    }
    m = /^\[data-ob-el="(.*)"\]$/.exec(selector);
    if (!m) throw new Error('unexpected selector ' + selector);
    return payload.dom.anchors[m[1]] === undefined ? null : anchor(m[1]);
  },
};
const applyElementStates = new Function(
  extract('applyElementStates') + '\nreturn applyElementStates;'
)();
applyElementStates(payload.states);
process.stdout.write(JSON.stringify({
  anchors: payload.dom.anchors,
  anchorPaths: payload.dom.anchorPaths || {},
  controls: controls.map((c) => ({
    tagName: c.tagName,
    name: c.name,
    type: c.type,
    value: c.value,
    checked: c.checked,
    disabled: c.disabled,
    readOnly: c.readOnly,
    checkboxPresence: c.dataset.obCheckboxPresence === '1',
    readOnlyNavigation: c.dataset.obReadonlyNavigation === '1',
    refCurrent: c._refCurrent,
    tabButton: c._tabButton,
    anchors: c._anchors,
    inTablePart: c._inTablePart,
  })),
}));
`
	cmd := exec.CommandContext(t.Context(), node, "-e", script, "static/managed.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execute managed.js applyElementStates: %v\n%s", err, output)
	}
	var result managedFormDOMModel
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode managed.js result: %v; output=%s", err, output)
	}
	return result
}

// --- Путь размещения вместо имени (#1543) -----------------------------------
// Карта состояний ключуется именем элемента: имя бывает пустым (Надписи,
// декорации) и повторяющимся (ТЧ размещена дважды), а после #1226 в карту
// попадает каждый потомок условного контейнера. Безымянный потомок клал
// состояние под общий ключ «», и поле вне группы получало чужой запрет.

// якоряФормы — все пары (data-ob-el, data-ob-el-path) в порядке разметки.
func якоряФормы(t *testing.T, rendered string) []struct{ имя, путь string } {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatalf("parse managed form HTML: %v", err)
	}
	var out []struct{ имя, путь string }
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			name, естьИмя := managedHTMLAttr(n, "data-ob-el")
			путь, естьПуть := managedHTMLAttr(n, "data-ob-el-path")
			if естьИмя && естьПуть {
				out = append(out, struct{ имя, путь string }{name, путь})
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return out
}

func TestКаскадУсловногоЗапрета_БезымянныеЭлементыНеПопадаютВОбщийКлюч(t *testing.T) {
	ent := &metadata.Entity{
		Name: "ЗаявкаБезымянные", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "СтадияОформления", Type: metadata.FieldTypeString}},
	}
	группа := &metadata.FormElement{
		Kind: metadata.FormElementGroupBox, Name: "ГруппаРеквизитов",
		ReadOnlyWhen: `СтадияОформления = "Принята"`,
		Children: []*metadata.FormElement{
			{Kind: metadata.FormElementLabel, TitleMap: map[string]string{"ru": "Одна"}},
			{Kind: metadata.FormElementLabel, TitleMap: map[string]string{"ru": "Две"}},
		},
	}
	form := managedObjectForm(группа, fieldEl("ПолеСтадии", "Объект.СтадияОформления"))
	form.EntityName = ent.Name
	ent.Forms = []*metadata.FormModule{form}

	// Пройдено через ту же функцию, что и ответ события формы.
	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	states := s.formElementStates(form, ent, map[string]any{"СтадияОформления": "Принята"}, true)
	if states == nil {
		t.Fatal("карта состояний не рассчитана")
	}
	if _, есть := states.ReadOnly[""]; есть {
		t.Fatalf("безымянные потомки попали в общий пустой ключ: %#v", states.ReadOnly)
	}
	// Группа и обе надписи — три разные строки карты с непустыми ключами.
	if len(states.ReadOnly) != 3 {
		t.Fatalf("ожидались строки группы и обеих надписей, получено: %#v", states.ReadOnly)
	}

	rendered := отрисоватьСУсловиями(t, ent, form, map[string]string{"СтадияОформления": "Принята"})
	var безымянные []string
	for _, якорь := range якоряФормы(t, rendered) {
		if якорь.имя == "" {
			безымянные = append(безымянные, якорь.путь)
		}
	}
	if len(безымянные) != 2 || безымянные[0] == безымянные[1] || безымянные[0] == "" {
		t.Fatalf("безымянные надписи обязаны получить разные непустые пути: %v", безымянные)
	}
}

func TestКаскадУсловногоЗапрета_ОдноимённыеЭлементыСРазнымиУсловиями(t *testing.T) {
	// Одна ТЧ, размещённая дважды (readonly-сводка и редактируемая сетка), —
	// случай, который applyTableParts обрабатывает специально, значит, он бывает.
	// Одно имя у двух размещений не должно склеивать их состояния.
	ent := &metadata.Entity{
		Name: "ЗаявкаДвойники", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Улица", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
			{Name: "СтадияОформления", Type: metadata.FieldTypeString},
		},
	}
	вГруппе := &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "Двойник",
		DataPath: "Объект.Улица", ReadOnlyWhen: `СтадияОформления = "Принята"`,
	}
	группа := &metadata.FormElement{
		Kind: metadata.FormElementGroupBox, Name: "ГруппаРеквизитов",
		ReadOnlyWhen: `СтадияОформления = "Принята"`,
		Children:     []*metadata.FormElement{вГруппе},
	}
	form := managedObjectForm(группа,
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "Двойник",
			DataPath: "Объект.Комментарий",
		},
		fieldEl("ПолеСтадии", "Объект.СтадияОформления"),
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "КнопкаОтметить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Отметить"},
		})
	form.EntityName = ent.Name
	form.ProgramAST = mustParse(t, `
Процедура Отметить()
	Объект.Комментарий = "к";
КонецПроцедуры
`)
	ent.Forms = []*metadata.FormModule{form}

	rendered, response := каскадДоИПосле(t, ent, "Принята")
	states := response.ElementStates
	if states == nil {
		t.Fatal("карта состояний не рассчитана")
	}
	якоря := якоряФормы(t, rendered)
	var пути []string
	for _, якорь := range якоря {
		if якорь.имя == "Двойник" {
			пути = append(пути, якорь.путь)
		}
	}
	if len(пути) != 2 || пути[0] == "" || пути[1] == "" || пути[0] == пути[1] {
		t.Fatalf("одноимённые размещения обязаны получить разные пути: %v", пути)
	}
	// Ответ публичного события использует те же ключи, что и HTML:
	// запрет есть только у размещения внутри условной группы.
	if !states.ReadOnly[пути[0]] {
		t.Fatalf("двойник в группе не получил запрет по пути %q: %#v", пути[0], states.ReadOnly)
	}
	if _, есть := states.ReadOnly[пути[1]]; есть {
		t.Fatalf("двойник вне группы получил чужое состояние по пути %q: %#v", пути[1], states.ReadOnly)
	}
	// Разметка расходит их и по состоянию: в замороженной группе поле
	// нередактируемо, тёзка вне группы — редактируем.
	i := strings.Index(rendered, `name="Улица"`)
	if i < 0 || !strings.Contains(rendered[i:i+200], "readonly") {
		t.Fatalf("двойник внутри замороженной группы обязан быть нередактируемым:\n%s", rendered)
	}
	j := strings.Index(rendered, `name="Комментарий"`)
	if j < 0 {
		t.Fatalf("поле «Комментарий» не отрисовано:\n%s", rendered)
	}
	if strings.Contains(rendered[j:j+200], "readonly") {
		t.Fatalf("тёзка вне группы получил чужой запрет:\n%s", rendered)
	}
}

func TestКаскадУсловногоЗапрета_СкрытаяСтраницаНеСдвигаетПути(t *testing.T) {
	// Пути считаются по исходному дереву ДО фильтрации скрытых страниц: иначе
	// после скрытия первой вкладки ключи разъехались бы с клиентом.
	ent := заявкаСоСтадией()
	form := формаСУсловиями(ent, &metadata.FormElement{
		Kind: metadata.FormElementPages, Name: "Ветки",
		Children: []*metadata.FormElement{
			{
				Kind: metadata.FormElementPage, Name: "СкрытаяСтраница",
				TitleMap:   map[string]string{"ru": "Скрытая"},
				HiddenWhen: `СтадияОформления = "Принята"`,
				Children:   []*metadata.FormElement{fieldEl("ПолеСкрытое", "Объект.Комментарий")},
			},
			{
				Kind: metadata.FormElementPage, Name: "Итоги",
				TitleMap: map[string]string{"ru": "Итоги"},
				Children: []*metadata.FormElement{
					&metadata.FormElement{
						Kind: metadata.FormElementField, Name: "ПолеУлица",
						DataPath: "Объект.Улица", ReadOnlyWhen: `СтадияОформления = "Принята"`,
					},
				},
			},
		},
	})
	ключ := путьСостояния(t, form, "ПолеУлица")
	// Страница-сосед скрыта, но её индекс в пути никуда не девается: скрытие
	// не перенумеровывает дерево.
	if !strings.HasSuffix(ключ, ".1.0") {
		t.Fatalf("путь поля %q не учитывает скрытую страницу-соседа: %q", "ПолеУлица", ключ)
	}

	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	states := s.formElementStates(form, ent, map[string]any{"СтадияОформления": "Принята"}, true)
	if states == nil || !states.ReadOnly[ключ] {
		t.Fatalf("карта обязана нести запрет поля на видимой странице: %#v", states)
	}

	rendered := отрисоватьСУсловиями(t, ent, form, map[string]string{"СтадияОформления": "Принята"})
	if strings.Contains(rendered, "Скрытая") {
		t.Fatalf("скрытая страница не должна отрисовываться:\n%s", rendered)
	}
	var нашёлся bool
	for _, якорь := range якоряФормы(t, rendered) {
		if якорь.имя == "ПолеУлица" {
			нашёлся = якорь.путь == ключ
		}
	}
	if !нашёлся {
		t.Fatalf("якорь поля обязан нести путь %q из карты состояний:\n%s", ключ, rendered)
	}
}
