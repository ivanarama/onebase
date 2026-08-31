package ui

// Раскладка элемента управляемой формы (#1185): width/height/halign/valign.
//
// Заявка ровно об этом: ключи объявлены в модели, приняты `onebase check` и
// перенесены конвертером 1С, а рантайм читал только width/height у
// ПолеКартинки. Поэтому главный тест здесь идёт через HTTP-обработчик формы —
// тот самый путь, которым открывает карточку пользователь; проверка на уровне
// шаблона рядом покрывает остальные виды элементов.

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
)

func layoutTestEntity(el *metadata.FormElement) *metadata.Entity {
	ent := &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}
	ent.Forms = []*metadata.FormModule{{
		Name:       "Форма",
		EntityName: "Клиент",
		Kind:       "object",
		LayoutKind: metadata.FormLayoutManaged,
		Elements:   []*metadata.FormElement{el},
	}}
	return ent
}

// Карточка, открытая штатным обработчиком: ширина и выравнивание доезжают до
// разметки. До #1185 в HTML не было ни одного следа этих ключей.
func TestManagedLayout_FormHandlerAppliesSizeAndAlign(t *testing.T) {
	ent := layoutTestEntity(&metadata.FormElement{
		Kind:            metadata.FormElementField,
		Name:            "ПолеНаименование",
		DataPath:        "Объект.Наименование",
		Width:           220,
		HorizontalAlign: "center",
	})
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	req := httptest.NewRequest("GET", "/ui/catalog/клиент/new", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "клиент")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.form(rec, req)

	if rec.Code != 200 {
		t.Fatalf("форма не открылась: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "width:220px") {
		t.Errorf("ширина элемента не доехала до карточки:\n%s", firstFormGroup(body))
	}
	if !strings.Contains(body, "margin-left:auto;margin-right:auto") {
		t.Errorf("halign: center не доехал до карточки:\n%s", firstFormGroup(body))
	}
	// html/template подставляет в style= только доверенный template.CSS; иначе
	// на месте объявлений оказывается ZgotmplZ, и стиль пропадает целиком.
	if strings.Contains(body, "ZgotmplZ") {
		t.Errorf("стиль вырезан контекстным экранированием:\n%s", firstFormGroup(body))
	}
}

// Страница внутри СтраницыФормы имеет собственный внешний блок. Раньше ветка
// вкладок рендерила только её детей, поэтому layout страницы терялся, хотя
// onebase check и конструктор принимали ключи.
func TestManagedLayout_FormHandlerAppliesNestedPageLayout(t *testing.T) {
	ent := layoutTestEntity(&metadata.FormElement{
		Kind:  metadata.FormElementPages,
		Name:  "Страницы",
		Width: 500,
		Children: []*metadata.FormElement{{
			Kind:  metadata.FormElementPage,
			Name:  "Основное",
			Width: 310,
			Children: []*metadata.FormElement{{
				Kind:     metadata.FormElementField,
				Name:     "ПолеНаименование",
				DataPath: "Объект.Наименование",
			}},
		}},
	})
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	req := httptest.NewRequest("GET", "/ui/catalog/клиент/new", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "клиент")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.form(rec, req)

	if rec.Code != 200 {
		t.Fatalf("форма не открылась: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-tab-content="0" style="display:block;width:310px;max-width:100%;flex:0 0 auto;min-width:0;"`) {
		t.Errorf("layout вложенной страницы не применён к контейнеру вкладки:\n%s", body)
	}
}

// Обратная совместимость дороже удобства: форма без новых ключей обязана
// рендериться ровно как раньше — без атрибута style на блоке поля.
func TestManagedLayout_NoKeysNoStyle(t *testing.T) {
	out := renderLayoutElement(t, &metadata.FormElement{
		Kind:     metadata.FormElementField,
		Name:     "ПолеНаименование",
		DataPath: "Объект.Наименование",
	})
	if !strings.Contains(out, `<div class="form-group" data-ob-el="ПолеНаименование">`) {
		t.Errorf("разметка поля без раскладки изменилась:\n%s", out)
	}
}

// height растягивает сам ввод, а не пустое место под ним: внешний блок получает
// класс растяжки, а правило для него лежит в стиле managed-формы.
func TestManagedLayout_HeightStretchesControl(t *testing.T) {
	out := renderLayoutElement(t, &metadata.FormElement{
		Kind:      metadata.FormElementField,
		Name:      "ПолеКомментарий",
		DataPath:  "Объект.Комментарий",
		Multiline: true,
		Height:    180,
	})
	if !strings.Contains(out, metadata.FormLayoutFillClass) {
		t.Errorf("блок с высотой не помечен классом растяжки:\n%s", out)
	}
	if !strings.Contains(out, "height:180px") {
		t.Errorf("высота не применена:\n%s", out)
	}
	if !strings.Contains(managedFormStyles(t), ".form-group."+metadata.FormLayoutFillClass) {
		t.Error("в стиле managed-формы нет правила растяжки — класс остался бы декоративным")
	}
}

// Составное поле растягивается в два шага: сначала строка select/input + кнопки,
// затем сам управляющий элемент внутри строки. Одного flex на непосредственном
// div недостаточно: align-items:center оставлял select/input однострочным.
func TestManagedLayout_HeightReachesCompositeControls(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Контрагент", Type: "reference:Контрагент", RefEntity: "Контрагент"},
			{Name: "Файл", Type: metadata.FieldTypeString},
		},
	}
	ent.Forms = []*metadata.FormModule{{
		Name:       "Форма",
		EntityName: ent.Name,
		Kind:       "object",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "Ссылка", DataPath: "Объект.Контрагент", Height: 180},
			{Kind: metadata.FormElementField, Name: "Путь", DataPath: "Объект.Файл", Type: "file", Height: 180},
		},
	}}
	ctx := map[string]any{
		"Entity":     ent,
		"Form":       ent.Forms[0],
		"Values":     map[string]string{},
		"RefOptions": map[string]any{},
	}

	for _, el := range ent.Forms[0].Elements {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "managed-element", map[string]any{"El": el, "Ctx": ctx}); err != nil {
			t.Fatalf("execute managed-element %s: %v", el.Name, err)
		}
		out := buf.String()
		if !strings.Contains(out, `class="managed-control-row"`) || !strings.Contains(out, `class="managed-fill-control"`) {
			t.Errorf("%s: составной control не помечен для растяжки:\n%s", el.Name, out)
		}
	}
	if !strings.Contains(managedFormStyles(t), ".managed-control-row>.managed-fill-control{height:100%;min-height:0}") {
		t.Error("стиль managed-формы не доводит высоту составной строки до select/input")
	}
}

// Auto-margin выравнивает блочный контейнер, но не inline-button. Layout живёт
// на обёртке с intrinsic width, а data-ob-* остаются на рабочей кнопке.
func TestManagedLayout_ButtonAlignmentUsesBlockWrapper(t *testing.T) {
	out := renderLayoutElement(t, &metadata.FormElement{
		Kind:            metadata.FormElementButton,
		Name:            "КнопкаСправа",
		HorizontalAlign: "right",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventOnClick: "КнопкаСправаНажатие",
		},
	})
	if !strings.Contains(out, `class="managed-btn-layout" data-ob-el="КнопкаСправа" style="margin-left:auto;"`) {
		t.Fatalf("halign применён не к блочной обёртке кнопки:\n%s", out)
	}
	if !strings.Contains(out, `class="btn btn-secondary managed-btn"`) ||
		!strings.Contains(out, `data-ob-fire-click="КнопкаСправа"`) {
		t.Fatalf("рабочие атрибуты потеряны при добавлении обёртки:\n%s", out)
	}
	if strings.Count(out, `data-ob-el="КнопкаСправа"`) != 1 {
		t.Fatalf("у кнопки должен быть один state-якорь на внешнем блоке:\n%s", out)
	}

	withoutLayout := renderLayoutElement(t, &metadata.FormElement{Kind: metadata.FormElementButton, Name: "Обычная"})
	if strings.Contains(withoutLayout, "managed-btn-layout") || !strings.Contains(withoutLayout, `managed-btn" data-ob-el="Обычная"`) {
		t.Fatalf("кнопка без layout получила новую обёртку:\n%s", withoutLayout)
	}
}

// Ширина обязана действовать и в горизонтальной группе: там flex-basis
// перебивает width, если рядом нет flex:0 0 auto.
func TestManagedLayout_WidthSurvivesHorizontalGroup(t *testing.T) {
	out := renderLayoutElement(t, &metadata.FormElement{
		Kind:        metadata.FormElementGroupBox,
		Name:        "Группа",
		Orientation: "horizontal",
		Children: []*metadata.FormElement{{
			Kind:     metadata.FormElementField,
			Name:     "ПолеНаименование",
			DataPath: "Объект.Наименование",
			Width:    120,
		}},
	})
	if !strings.Contains(out, "width:120px;max-width:100%;flex:0 0 auto;min-width:0") {
		t.Errorf("в горизонтальной группе ширину съест flex-basis:\n%s", out)
	}
}

func TestManagedLayout_StretchSurvivesHorizontalGroup(t *testing.T) {
	out := renderLayoutElement(t, &metadata.FormElement{
		Kind:        metadata.FormElementGroupBox,
		Name:        "Группа",
		Orientation: "horizontal",
		Children: []*metadata.FormElement{{
			Kind:            metadata.FormElementField,
			Name:            "ПолеНаименование",
			DataPath:        "Объект.Наименование",
			HorizontalAlign: "stretch",
		}},
	})
	if !strings.Contains(out, "width:100%;flex:1 1 100%;min-width:0") {
		t.Errorf("stretch не перебил flex-basis горизонтальной группы:\n%s", out)
	}
}

// Остальные виды элементов: раскладка объявлена общей, значит и работать обязана
// у всех, а не у одного ПолеВвода — иначе это та же заявка следующей строкой.
func TestManagedLayout_AppliesToEveryKind(t *testing.T) {
	cases := []struct {
		name string
		el   *metadata.FormElement
		want string
	}{
		{"группа", &metadata.FormElement{Kind: metadata.FormElementGroupBox, Name: "Группа", Width: 400}, "width:400px"},
		{"страницы", &metadata.FormElement{Kind: metadata.FormElementPages, Name: "Страницы", Width: 500}, "width:500px"},
		{"флажок", &metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "Флажок", DataPath: "Объект.Комментарий", HorizontalAlign: "right"}, "margin-left:auto"},
		{"надпись", &metadata.FormElement{Kind: metadata.FormElementLabel, Name: "Надпись", Width: 300}, "width:300px"},
		{"кнопка", &metadata.FormElement{Kind: metadata.FormElementButton, Name: "Кнопка", Width: 160}, "width:160px"},
		{"поле даты", &metadata.FormElement{Kind: metadata.FormElementDatePicker, Name: "Дата", DataPath: "Объект.Комментарий", Width: 140}, "width:140px"},
		{"переключатель", &metadata.FormElement{Kind: metadata.FormElementSwitch, Name: "Выбор", DataPath: "Объект.Комментарий", VerticalAlign: "bottom"}, "align-self:flex-end"},
		{"поле кода", &metadata.FormElement{Kind: metadata.FormElementCodeField, Name: "Код", DataPath: "Объект.Комментарий", Width: 700}, "width:700px"},
		{"поле списка", &metadata.FormElement{Kind: metadata.FormElementInputList, Name: "Список", DataPath: "Объект.Комментарий", Width: 240}, "width:240px"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := renderLayoutElement(t, c.el)
			if !strings.Contains(out, c.want) {
				t.Errorf("раскладка не применена (%s не найдено):\n%s", c.want, out)
			}
		})
	}
}

// У ПолеКартинки width/height ограничивают саму картинку — так было до общего
// контракта, и переезд на «размер блока» перестроил бы готовые формы.
func TestManagedLayout_PictureKeepsOwnSizeSemantics(t *testing.T) {
	out := renderLayoutElement(t, &metadata.FormElement{
		Kind:            metadata.FormElementPicture,
		Name:            "Логотип",
		Picture:         "logo.png",
		Width:           64,
		Height:          64,
		HorizontalAlign: "center",
	})
	if !strings.Contains(out, "max-width:64px;max-height:64px") {
		t.Errorf("размер картинки перестал быть размером картинки:\n%s", out)
	}
	if !strings.Contains(out, `class="form-picture" style="margin-left:auto;margin-right:auto;"`) {
		t.Errorf("выравнивание картинки не применено:\n%s", out)
	}
}

// Табличная часть: height перебивает высоту сетки, посчитанную по числу строк.
func TestManagedLayout_TablePartHeightOverridesRowGuess(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{{
			Name:   "Товары",
			Fields: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}},
	}
	el := &metadata.FormElement{
		Kind:     metadata.FormElementTablePart,
		Name:     "Товары",
		DataPath: "Объект.Товары",
		Height:   520,
	}
	var buf bytes.Buffer
	ctx := map[string]any{
		"Entity":        ent,
		"Values":        map[string]string{},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"TablePartRows": map[string][]map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]any{},
		"CanWrite":      true,
	}
	if err := tmpl.ExecuteTemplate(&buf, "managed-element", map[string]any{"El": el, "Ctx": ctx}); err != nil {
		t.Fatalf("execute managed-element: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "height:520px") {
		t.Errorf("высота табличной части не применена:\n%s", out)
	}
	if strings.Contains(out, "height:200px") {
		t.Errorf("высота по числу строк осталась вместо заданной:\n%s", out)
	}
}

// renderLayoutElement рендерит элемент production-шаблоном managed-формы.
func renderLayoutElement(t *testing.T, el *metadata.FormElement) string {
	t.Helper()
	ent := layoutTestEntity(el)
	ctx := map[string]any{
		"Entity":        ent,
		"Form":          ent.Forms[0],
		"Values":        map[string]string{},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": map[string]any{},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "managed-element", map[string]any{"El": el, "Ctx": ctx}); err != nil {
		t.Fatalf("execute managed-element: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "ZgotmplZ") {
		t.Fatalf("стиль вырезан контекстным экранированием:\n%s", out)
	}
	return out
}

// managedFormStyles — CSS страницы управляемой формы (для проверки правил,
// которые нельзя выразить inline-стилем).
func managedFormStyles(t *testing.T) string {
	t.Helper()
	return tplManagedForm
}

// firstFormGroup вырезает окрестности первого блока поля — чтобы сообщение об
// ошибке не печатало страницу целиком.
func firstFormGroup(body string) string {
	i := strings.Index(body, `class="form-group`)
	if i < 0 {
		return body
	}
	end := i + 400
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}
