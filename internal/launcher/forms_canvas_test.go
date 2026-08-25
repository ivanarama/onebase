package launcher

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/formdoc"
	"github.com/ivantit66/onebase/internal/metadata"
)

const canvasSample = `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Звонок
elements:
  - kind: ГруппаФормы
    name: Группа1
    children:
      - kind: ПолеВвода
        name: ПолеНомер
        data_path: Объект.Номер
        required: true
      - kind: ПолеВвода
        name: ПолеДата
        data_path: Объект.Дата
`

func loadCanvasDoc(t *testing.T) *formdoc.Doc {
	t.Helper()
	doc, err := formdoc.Load([]byte(canvasSample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return doc
}

// Холст помечает каждый элемент его node-id — это адресация, по которой клиент
// наводит выбор и правки на конкретный узел дерева (#164).
func TestRenderFormCanvas_NodeIDs(t *testing.T) {
	html, err := renderFormCanvas(loadCanvasDoc(t), "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	for _, want := range []string{
		`data-node-id="elements.0"`,
		`data-node-id="elements.0.children.0"`,
		`data-node-id="elements.0.children.1"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в холсте нет %s:\n%s", want, html)
		}
	}
}

// В контейнере на N детей должно быть N+1 drop-зон с правильными parent/index —
// иначе реквизит из палитры некуда уронить между полями.
func TestRenderFormCanvas_DropZones(t *testing.T) {
	out, err := renderFormCanvas(loadCanvasDoc(t), "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	// Верхний уровень: 1 элемент → 2 зоны (index 0 и 1).
	for _, want := range []string{
		`data-parent="" data-index="0"`,
		`data-parent="" data-index="1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("нет верхней drop-зоны %s", want)
		}
	}
	// Внутри группы: 2 ребёнка → 3 зоны (0,1,2).
	for _, want := range []string{
		`data-parent="elements.0" data-index="0"`,
		`data-parent="elements.0" data-index="1"`,
		`data-parent="elements.0" data-index="2"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("нет drop-зоны группы %s", want)
		}
	}
}

// Выбранный элемент получает класс fc-selected; невыбранный — нет.
func TestRenderFormCanvas_Selection(t *testing.T) {
	out, err := renderFormCanvas(loadCanvasDoc(t), "elements.0.children.1")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if !strings.Contains(out, `data-selected="elements.0.children.1"`) {
		t.Errorf("не проставлен data-selected:\n%s", out)
	}
	// Должна быть ровно одна обёртка с fc-selected, и это поле даты.
	if n := strings.Count(out, "fc-selected"); n != 1 {
		t.Errorf("ожидалась 1 пометка fc-selected, найдено %d:\n%s", n, out)
	}
	// Грубая проверка, что выбран именно children.1: класс рядом с его node-id.
	i := strings.Index(out, `data-node-id="elements.0.children.1"`)
	if i < 0 {
		t.Fatalf("нет узла children.1")
	}
	// fc-selected должен идти в той же открывающей теге (до '>').
	tagStart := strings.LastIndex(out[:i], "<")
	tagEnd := strings.Index(out[tagStart:], ">") + tagStart
	if !strings.Contains(out[tagStart:tagEnd], "fc-selected") {
		t.Errorf("fc-selected не на узле children.1:\n%s", out[tagStart:tagEnd])
	}
}

// Страница редактора должна разворачивать клиент конструктора: вкладку
// «Конструктор», холст, панель свойств и проводку к /forms/edit-op (#164).
func TestFormsEditor_RendersDesignerScaffold(t *testing.T) {
	data := &configuratorData{
		Base: &Base{ID: "test-base"},
		EditingForm: &cfgManagedForm{
			Entity: "Контрагент",
			Name:   "ФормаОбъекта",
			Kind:   "object",
			YAML:   "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Контрагент\n",
		},
	}
	var buf bytes.Buffer
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	page := buf.String()
	for _, want := range []string{
		`id="canvas-host"`,
		`id="prop-panel"`,
		`id="designer-split"`,
		`id="prop-splitter"`,
		`id="editor-layout-toggle"`,
		`layout-modern`,
		`layout-classic`,
		`data-mode="yaml"`,
		`data-mode="os"`,
		`data-mode="design"`,
		`data-mode="preview"`,
		`data-layout="modern"`,
		`data-layout="classic"`,
		`data-source="yaml"`,
		`data-source="os"`,
		`data-rp="design"`,
		`switchRightPane(`,
		`function switchEditorMode`,
		`function switchEditorLayout`,
		`function switchSourceMode`,
		`onebase.forms.editorLayout`,
		`onebase.forms.propPanelSize`,
		`onebase.forms.propPanelClassicSize`,
		`/configurator/forms/edit-op`,
		`function reloadCanvas`,
		`op: 'insert'`,
		`var _entity = "Контрагент"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("в странице редактора нет %q", want)
		}
	}
}

// Страница редактора разворачивает клиентскую обвязку части 2: секцию событий,
// редактор набора значений, свойства формы и палитру Переключателя (план 71b).
func TestFormsEditor_Part2Controls(t *testing.T) {
	data := &configuratorData{
		Base: &Base{ID: "test-base"},
		EditingForm: &cfgManagedForm{
			Entity: "Контрагент", Name: "ФормаОбъекта", Kind: "object",
			YAML: "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Контрагент\n",
		},
	}
	var buf bytes.Buffer
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	page := buf.String()
	for _, want := range []string{
		"function renderFormProps",  // B2 свойства формы
		"function addEventsRows",    // B1 события
		"function addOptionsEditor", // C1 набор значений
		"op: 'delProp'",             // снятие события / view
		"op: 'setOptions'",          // запись набора
		"selectNode('form')",        // закладка «Форма» / клик по пустому холсту
		`data-kind="Переключатель"`, // палитра C1
		"function ensureProcedure",  // «Создать обработчик…»
		// UX-доводка: закладки панели свойств, сворачивание кода, авто-вкладка.
		`data-pt="form"`,          // закладка «Форма» в панели свойств
		"function switchPropTab",  // переключение «Элемент | Форма»
		"function toggleLeftPane", // свернуть/развернуть редактор кода
		"function insertPagesSet", // одиночная страница → набор с вкладкой
		"Расположение реквизитов", // #205 orientation для группы
		"ИмяТабличнойЧасти",       // #205 подсказка в создаваемом обработчике ТЧ
	} {
		if !strings.Contains(page, want) {
			t.Errorf("в странице редактора нет %q", want)
		}
	}
	// Вычищенные элементы шапки не должны возвращаться.
	for _, gone := range []string{"grid-toggle", "setGridFlag", `onclick="refreshPreview()"`} {
		if strings.Contains(page, gone) {
			t.Errorf("в странице редактора остался убранный элемент %q", gone)
		}
	}
}

// Поле с required:true несёт визуальную звёздочку; data_path определяет
// плейсхолдер последнего сегмента.
func TestRenderFormCanvas_FieldDetails(t *testing.T) {
	out, err := renderFormCanvas(loadCanvasDoc(t), "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if !strings.Contains(out, "fc-req") {
		t.Errorf("нет отметки обязательности у поля required:true")
	}
	if !strings.Contains(out, `placeholder="Номер"`) {
		t.Errorf("плейсхолдер не из последнего сегмента data_path:\n%s", out)
	}
}

// Холст набора СтраницыФормы несёт drop-зоны уровня страниц (fc-drop-page) для
// добавления новой страницы: N+1 зон, адресованных в контейнер Pages (follow-up
// #164, слайс C).
func TestRenderFormCanvas_PageDropZones(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Звонок
elements:
  - kind: СтраницыФормы
    name: Страницы1
    children:
      - kind: Страница
        name: Стр1
        children:
          - kind: ПолеВвода
            name: П1
            data_path: Объект.Поле
`
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if !strings.Contains(out, `class="fc-drop-page" data-parent="elements.0" data-index="0"`) {
		t.Errorf("нет drop-зоны страниц перед первой страницей:\n%s", out)
	}
	if !strings.Contains(out, `class="fc-drop-page" data-parent="elements.0" data-index="1"`) {
		t.Errorf("нет drop-зоны страниц после последней страницы:\n%s", out)
	}
}

// Холст табличной части показывает её колонки (kind:Колонка), каждую со своим
// node-id и подписью из последнего сегмента data_path; выбранная подсвечена
// (follow-up #164, слайс D1).
func TestRenderFormCanvas_TablePartColumns(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ТабличнаяЧасть
    name: ТабТовары
    data_path: Объект.Товары
    children:
      - kind: Колонка
        name: КолНоменклатура
        data_path: Объект.Товары.Номенклатура
      - kind: Колонка
        name: КолКоличество
        data_path: Объект.Товары.Количество
`
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := renderFormCanvas(doc, "elements.0.children.0")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if !strings.Contains(out, "fc-cols") {
		t.Errorf("нет контейнера колонок:\n%s", out)
	}
	if !strings.Contains(out, `data-node-id="elements.0.children.0"`) || !strings.Contains(out, `data-node-id="elements.0.children.1"`) {
		t.Errorf("колонки без node-id:\n%s", out)
	}
	if !strings.Contains(out, "Номенклатура") || !strings.Contains(out, "Количество") {
		t.Errorf("нет подписей колонок:\n%s", out)
	}
	if !strings.Contains(out, "fc-col fc-selected") {
		t.Errorf("выбранная колонка не подсвечена:\n%s", out)
	}
}

// Отдельная Страница (вне набора СтраницыФормы) рисуется как страница со своей
// зоной для детей, а не как «неизвестный» элемент (follow-up #164).
func TestRenderFormCanvas_StandalonePage(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Звонок
elements:
  - kind: Страница
    name: Стр1
    title:
      ru: Основное
`
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if strings.Contains(out, "fc-unknown") {
		t.Errorf("отдельная страница рисуется как неизвестный элемент:\n%s", out)
	}
	if !strings.Contains(out, "fc-page") {
		t.Errorf("нет класса fc-page:\n%s", out)
	}
	if !strings.Contains(out, `data-parent="elements.0"`) {
		t.Errorf("нет drop-зоны для детей страницы:\n%s", out)
	}
}

// Не-Страница, затесавшаяся в набор СтраницыФормы, рисуется своим рендером
// (видно ошибку структуры), а не маскируется под вкладку (follow-up #164).
func TestRenderFormCanvas_NonPageInsidePages(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Звонок
elements:
  - kind: СтраницыФормы
    name: Страницы
    children:
      - kind: ГруппаФормы
        name: ЧужаяГруппа
        children: []
`
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	// Группа отрисована своим рендером (fieldset fc-group), а не как fc-page.
	if !strings.Contains(out, "fc-group") {
		t.Errorf("не-страница в наборе не отрисована своим рендером:\n%s", out)
	}
}

// Переключатель рисуется своим элементом (radio-набор по Options), не «unknown»;
// значения и view попадают в модель панели свойств (follow-up #164 batch C1).
func TestRenderFormCanvas_Switch(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: Переключатель
    name: Приоритет
    data_path: Объект.Приоритет
    view: select
    options:
      - value: 1
        label:
          ru: Низкий
      - value: 2
        label:
          ru: Высокий
`
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if strings.Contains(out, "fc-unknown") {
		t.Errorf("Переключатель рисуется как неизвестный элемент:\n%s", out)
	}
	if !strings.Contains(out, "fc-switch") || !strings.Contains(out, "Низкий") || !strings.Contains(out, "Высокий") {
		t.Errorf("нет рендера набора значений:\n%s", out)
	}
	model, err := canvasModel(doc)
	if err != nil {
		t.Fatalf("canvasModel: %v", err)
	}
	info := model["elements.0"]
	if info.View != "select" {
		t.Errorf("view не в модели: %+v", info)
	}
	if len(info.Options) != 2 || info.Options[0].Value != "1" || info.Options[0].Label != "Низкий" {
		t.Errorf("options не в модели: %+v", info.Options)
	}
}

// canvasModel отдаёт события элемента (events) для секции «События» панели
// свойств (follow-up #164 batch B1).
func TestCanvasModel_Events(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Звонок
elements:
  - kind: Кнопка
    name: КнопкаОК
    events:
      Нажатие: Обработать
`
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	model, err := canvasModel(doc)
	if err != nil {
		t.Fatalf("canvasModel: %v", err)
	}
	if model["elements.0"].Events["Нажатие"] != "Обработать" {
		t.Errorf("событие не в модели: %+v", model["elements.0"])
	}
}

// batch A: ПолеКартинки рисуется своим элементом (не «unknown»), а свойства
// mask/picture/width/no_grid попадают в модель панели свойств (follow-up #164).
func TestRenderFormCanvas_BatchAProps(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Счёт
elements:
  - kind: ПолеВвода
    name: ПолеНомер
    data_path: Объект.Номер
    mask: "999-999"
  - kind: ПолеКартинки
    name: Лого
    picture: logo.png
    width: 80
  - kind: ТабличнаяЧасть
    name: ТабТовары
    data_path: Объект.Товары
    no_grid: true
    children: []
`
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if strings.Contains(out, "fc-unknown") {
		t.Errorf("ПолеКартинки рисуется как неизвестный элемент:\n%s", out)
	}
	if !strings.Contains(out, "fc-pic") {
		t.Errorf("нет рендера картинки:\n%s", out)
	}
	model, err := canvasModel(doc)
	if err != nil {
		t.Fatalf("canvasModel: %v", err)
	}
	if model["elements.0"].Mask != "999-999" {
		t.Errorf("mask не в модели: %+v", model["elements.0"])
	}
	if model["elements.1"].Picture != "logo.png" || model["elements.1"].Width != 80 {
		t.Errorf("picture/width не в модели: %+v", model["elements.1"])
	}
	if !model["elements.2"].NoGrid {
		t.Errorf("no_grid не в модели: %+v", model["elements.2"])
	}
}

// Горизонтальное расположение группы проходит через YAML → модель холста →
// предпросмотр. Само свойство редактируется в панели как orientation: horizontal.
func TestRenderFormCanvas_GroupHorizontalOrientation(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Счёт
elements:
  - kind: ГруппаФормы
    name: Реквизиты
    orientation: horizontal
    children:
      - kind: ПолеВвода
        name: ПолеНомер
        data_path: Объект.Номер
`
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if !strings.Contains(out, "fc-group-horizontal") {
		t.Errorf("горизонтальная группа не помечена на холсте:\n%s", out)
	}
	model, err := canvasModel(doc)
	if err != nil {
		t.Fatalf("canvasModel: %v", err)
	}
	if model["elements.0"].Orientation != "horizontal" {
		t.Errorf("orientation не попал в модель: %+v", model["elements.0"])
	}
	preview := renderManagedFormPreview(&metadata.FormModule{
		Elements: []*metadata.FormElement{{
			Kind:        metadata.FormElementGroupBox,
			Name:        "Реквизиты",
			Orientation: "horizontal",
			Children: []*metadata.FormElement{{
				Kind:     metadata.FormElementField,
				Name:     "ПолеНомер",
				DataPath: "Объект.Номер",
			}},
		}},
	}, nil)
	if !strings.Contains(preview, "group-horizontal") {
		t.Errorf("предпросмотр не получил класс горизонтальной группы:\n%s", preview)
	}
}
