package launcher

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/formdoc"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Конструктор форм и рантайм обязаны показывать ОДНУ форму: раскладка
// (width/height/halign/valign, #1185) действует и на холсте, и в предпросмотре.
// Иначе поле, подогнанное на холсте, разъезжается при открытии карточки — а
// ловится это уже пользователем.

const layoutCanvasSample = `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Звонок
elements:
  - kind: ПолеВвода
    name: ПолеНомер
    data_path: Объект.Номер
    width: 220
    halign: center
  - kind: ПолеВвода
    name: ПолеКомментарий
    data_path: Объект.Комментарий
    height: 180
  - kind: ПолеВвода
    name: ПолеБезРаскладки
    data_path: Объект.Дата
  - kind: СтраницыФормы
    name: Страницы
    width: 500
    children:
      - kind: Страница
        name: Основное
        width: 310
  - kind: Страница
    name: ОтдельнаяСтраница
    width: 320
`

func TestRenderFormCanvas_AppliesLayout(t *testing.T) {
	doc, err := formdoc.Load([]byte(layoutCanvasSample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	if !strings.Contains(out, "width:220px") || !strings.Contains(out, "margin-left:auto;margin-right:auto") {
		t.Errorf("холст не показывает размер и выравнивание:\n%s", out)
	}
	if !strings.Contains(out, metadata.FormLayoutFillClass) {
		t.Errorf("холст не помечает блок с заданной высотой классом растяжки:\n%s", out)
	}
	for _, want := range []string{"width:500px", "width:310px", "width:320px"} {
		if !strings.Contains(out, want) {
			t.Errorf("холст потерял layout контейнера страниц (%s):\n%s", want, out)
		}
	}
	// Элемент без ключей раскладки рисуется прежней разметкой — без style.
	if !strings.Contains(out, `data-node-id="elements.2" data-kind="ПолеВвода"><label>`) {
		t.Errorf("style появился у элемента без раскладки:\n%s", out)
	}
}

// Панель свойств отдаёт halign/valign клиенту: без них выпадающие списки
// открывались бы пустыми и молча стирали значение при первой же правке.
func TestCanvasModel_CarriesLayout(t *testing.T) {
	doc, err := formdoc.Load([]byte(layoutCanvasSample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	model, err := canvasModel(doc)
	if err != nil {
		t.Fatalf("canvasModel: %v", err)
	}
	info, ok := model["elements.0"]
	if !ok {
		t.Fatalf("в модели нет первого элемента: %+v", model)
	}
	if info.Width != 220 || info.HAlign != "center" {
		t.Errorf("раскладка не попала в модель панели свойств: %+v", info)
	}
	if model["elements.1"].Height != 180 {
		t.Errorf("высота не попала в модель: %+v", model["elements.1"])
	}
}

func TestRenderManagedFormPreview_AppliesLayout(t *testing.T) {
	fm := &metadata.FormModule{
		EntityName: "Контрагент",
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеНаименование", DataPath: "Объект.Наименование", Width: 220, HorizontalAlign: "right"},
			{Kind: metadata.FormElementField, Name: "ПолеКомментарий", DataPath: "Объект.Комментарий", Height: 160},
			{Kind: metadata.FormElementButton, Name: "Кнопка", Width: 150},
			{Kind: metadata.FormElementGroupBox, Name: "Группа", Width: 600},
			{Kind: metadata.FormElementPicture, Name: "Логотип", Width: 64, Height: 64, HorizontalAlign: "center"},
			{Kind: metadata.FormElementPages, Name: "Страницы", Width: 500, Children: []*metadata.FormElement{
				{Kind: metadata.FormElementPage, Name: "Основное", Width: 310},
			}},
			{Kind: metadata.FormElementPage, Name: "ОтдельнаяСтраница", Width: 320},
		},
	}
	out := renderManagedFormPreview(fm, nil)
	for _, want := range []string{
		`class="fg" style="width:220px;max-width:100%;flex:0 0 auto;min-width:0;margin-left:auto;"`,
		`height:160px`,
		metadata.FormLayoutFillClass,
		`<button type="button" class="btn" style="width:150px`,
		`<fieldset style="width:600px`,
		`<div class="tabs" style="width:500px`,
		`<div class="tab-page active" style="width:310px`,
		`<fieldset style="width:320px`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("предпросмотр не применил раскладку (%s):\n%s", want, out)
		}
	}
	// У картинки width/height — размер самой картинки, поэтому в обёртке их быть
	// не должно: иначе предпросмотр обещал бы не то, что нарисует рантайм.
	if strings.Contains(out, `class="hint" style="width:64px`) {
		t.Errorf("предпросмотр принял размер картинки за размер блока:\n%s", out)
	}
	if !strings.Contains(out, `class="hint" style="margin-left:auto;margin-right:auto;"`) {
		t.Errorf("выравнивание картинки в предпросмотре потеряно:\n%s", out)
	}
	// Правило растяжки должно быть в самом документе предпросмотра — класс без
	// него ничего не меняет.
	if !strings.Contains(out, ".fg."+metadata.FormLayoutFillClass) {
		t.Errorf("в стиле предпросмотра нет правила растяжки:\n%s", out)
	}
}

// Форма без новых ключей рендерится прежней разметкой: ни одного style= на
// блоках, где его не было.
func TestRenderManagedFormPreview_NoLayoutNoStyle(t *testing.T) {
	fm := &metadata.FormModule{
		EntityName: "Контрагент",
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеНаименование", DataPath: "Объект.Наименование"},
		},
	}
	out := renderManagedFormPreview(fm, nil)
	if !strings.Contains(out, `<div class="fg"><label>`) {
		t.Errorf("разметка поля без раскладки изменилась:\n%s", out)
	}
}

// Панель свойств конструктора обязана предлагать эти ключи: пока их не было в
// панели, единственным способом задать раскладку была правка YAML руками.
func TestFormsEditorPanel_HasLayoutControls(t *testing.T) {
	src := formsEditorScript(t)
	for _, want := range []string{
		"addLayoutSection",
		"'Ширина, px', 'width'",
		"'Высота, px', 'height'",
		"'halign'",
		"'valign'",
		"stretch",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("в панели свойств конструктора нет %q", want)
		}
	}
	// Колонке и командной панели раскладку предлагать нельзя: своего блока у них
	// нет, и заполненный ключ снова ничего не сделал бы.
	if !strings.Contains(src, "info.kind === 'Колонка' || info.kind === 'КоманднаяПанель'") {
		t.Error("панель предлагает раскладку видам без собственного блока")
	}
}
