package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Набор фиксирует контракт ввода с клавиатуры в ячейке-ссылке табличной части.
// Повод: набрать документ (заказ покупателя) с клавиатуры было невозможно —
// пункт выпадающего списка выбирался только мышью, а набранный текст при
// Tab/Enter молча пропадал. Поведение целиком клиентское, поэтому здесь
// проверяются (а) серверный контракт, на который опирается клиент, и
// (б) инварианты самого ассета. Поведение в браузере проверялось отдельно —
// настоящий SlickGrid и настоящие события клавиатуры через CDP.

// TestManagedFormGridRefEntityAttr — серверная часть контракта: колонка-ссылка
// обязана нести имя сущности в data-sg-cols. Без него редактор ячейки не знает,
// где искать, и остаётся с предзагруженными 50 позициями — товар за их
// пределами в ячейке не найти вообще.
func TestManagedFormGridRefEntityAttr(t *testing.T) {
	form := &metadata.FormModule{
		Name:       "ФормаОбъекта",
		Kind:       "object",
		EntityName: "Заказ",
		LayoutKind: metadata.FormLayoutManaged,
		Title:      map[string]string{"ru": "Заказ"},
		Elements: []*metadata.FormElement{
			{
				Kind:     metadata.FormElementTablePart,
				Name:     "ЭлементТовары",
				TitleMap: map[string]string{"ru": "Товары"},
				DataPath: "Объект.Товары",
			},
		},
	}
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{
			{
				Name: "Товары",
				Fields: []metadata.Field{
					{Name: "Номенклатура", Type: "reference:Номенклатура", RefEntity: "Номенклатура"},
					{Name: "Количество", Type: "number"},
				},
			},
		},
		Forms: []*metadata.FormModule{form},
	}

	data := map[string]any{
		"Entity":        ent,
		"Form":          form,
		"IsNew":         true,
		"Values":        map[string]string{},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{"Товары": {}},
		"User":          nil,
		"Lang":          "ru",
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	if html := buf.String(); !strings.Contains(html, `"ref":"Номенклатура"`) {
		t.Error(`data-sg-cols не содержит "ref":"Номенклатура" — редактору ячейки негде искать позиции`)
	}
}

// TestRefEditorKeyboardContract — инварианты редактора ячейки-ссылки.
func TestRefEditorKeyboardContract(t *testing.T) {
	js := string(managedJS)
	for _, tc := range []struct {
		want, why string
	}{
		{"input.addEventListener('keydown', onKeyDown)", "без слушателя клавиатуры ячейка управляется только мышью"},
		{"'ArrowDown'", "↑/↓ должны вести по выпадающему списку"},
		{"e.stopPropagation()", "иначе стрелки уводят курсор SlickGrid на соседнюю строку прямо из списка"},
		{"function resolveTyped", "ввод по строке: набранный текст обязан превращаться в ссылку"},
		{"'/ui/_ref-options/'", "поиск по всему справочнику, а не по предзагруженным 50 позициям"},
		{"col.refEntity = c.ref", "без имени сущности серверный поиск и форма выбора работать не могут"},
		{"data-ob-ref-list", "маркер выпадающего списка — по нему его находят обработчики и тесты"},
		{"window._obRefDropdown", "Esc обязан закрывать список, не отменяя правку ячейки"},
		{"onValidationError", "непройденная проверка без текста причины выглядит как «залипшая» ячейка"},
	} {
		if !strings.Contains(js, tc.want) {
			t.Errorf("managed.js не содержит %q — %s", tc.want, tc.why)
		}
	}
}

// TestRefEditorTracksTypedText — главный регресс. isValueChanged() обязан
// учитывать НАБРАННЫЙ ТЕКСТ, а не только выбранный id: при false SlickGrid
// (см. commitCurrentEdit в slick.grid.js) не зовёт ни validate(), ни
// serializeValue() — и введённое пользователем пропадает без следа.
func TestRefEditorTracksTypedText(t *testing.T) {
	js := string(managedJS)
	idx := strings.Index(js, "this.isValueChanged = function() {")
	if idx < 0 {
		t.Fatal("managed.js: не найден isValueChanged редактора ячейки-ссылки")
	}
	body := js[idx:]
	if end := strings.Index(body, "this.validate"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "input.value") {
		t.Error("isValueChanged не смотрит на набранный текст — правка ячейки-ссылки будет молча теряться")
	}
}

// TestGridSyncCommitsOpenEditor — второй путь потери введённого: пользователь
// набирает значение в ячейке и сразу жмёт «Записать». obGridSync сериализует
// dataView, куда незакоммиченная правка ещё не попала, поэтому перед выгрузкой
// строк редактор надо закрыть; если закрыть не удалось (ненайденная ссылка) —
// отправку формы отменяем, иначе в базу уйдёт не то, что на экране.
func TestGridSyncCommitsOpenEditor(t *testing.T) {
	js := string(managedJS)
	idx := strings.Index(js, "window.obGridSync = function()")
	if idx < 0 {
		t.Fatal("managed.js: не найдена obGridSync")
	}
	body := js[idx:]
	if end := strings.Index(body, "function gridCellEventParams"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "commitCurrentEdit()") {
		t.Error("obGridSync не закрывает открытый редактор — значение, набранное перед «Записать», потеряется")
	}
	if !strings.Contains(body, "return ok;") {
		t.Error("obGridSync не сообщает об успехе — вызывающий не сможет отменить запись")
	}
	if !strings.Contains(js, "window.obGridSync() === false") {
		t.Error("обработчик submit не проверяет результат obGridSync — форма запишется с непринятой правкой ячейки")
	}
}

// TestRefPickerKeyboard — форма выбора (общая модалка ui.js) тоже обязана
// работать с клавиатуры: раньше пункт выбирался исключительно кликом.
func TestRefPickerKeyboard(t *testing.T) {
	js := string(uiJS)
	idx := strings.Index(js, "function openRefPicker")
	if idx < 0 {
		t.Fatal("ui.js: не найдена openRefPicker")
	}
	body := js[idx:]
	if end := strings.Index(body, "function openRefCurrent"); end > 0 {
		body = body[:end]
	}
	for _, tc := range []struct {
		want, why string
	}{
		{"search.addEventListener('keydown'", "в поле поиска нужны ↑/↓/Enter/Esc"},
		{"selectItem(items[rpActive])", "Enter обязан выбирать подсвеченный пункт"},
		{"rpPaint", "подсветку текущего пункта нужно рисовать"},
	} {
		if !strings.Contains(body, tc.want) {
			t.Errorf("openRefPicker не содержит %q — %s", tc.want, tc.why)
		}
	}
}
