package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Набор фиксирует встроенные горячие клавиши, привычные по 1С: Ins/F9/Ctrl+↑↓
// в табличной части, Ctrl+Enter и Ctrl+S на форме, Ins/↑↓/Enter/F2/Ctrl+F
// в списке. Поведение клиентское; здесь — серверные маркеры, на которые оно
// опирается, и инварианты реальных обработчиков ассетов.

// TestPageListCreateKeyboardMarker — кнопке «Создать» нужен стабильный маркер:
// по нему Ins находит её на странице списка. Селектор по href сломался бы от
// любой правки маршрута.
func TestPageListCreateKeyboardMarker(t *testing.T) {
	for _, hierarchical := range []bool{false, true} {
		name := "обычный"
		if hierarchical {
			name = "иерархический"
		}
		t.Run(name, func(t *testing.T) {
			ent := &metadata.Entity{
				Name:         "Контрагент",
				Kind:         metadata.KindCatalog,
				Hierarchical: hierarchical,
				Fields:       []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			}
			data := map[string]any{
				"Entity":           ent,
				"Rows":             []map[string]any{},
				"Params":           storage.ListParams{},
				"RefFilterOptions": map[string]any{},
				"IsAdmin":          true,
				"CanWrite":         true,
				"CanDelete":        true,
				"CanUnpost":        true,
				"Lang":             "ru",
				"Total":            0,
				"Page":             1,
				"TotalPages":       1,
			}
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "page-list", data); err != nil {
				t.Fatalf("ExecuteTemplate page-list: %v", err)
			}
			if !strings.Contains(buf.String(), "data-ob-list-create") {
				t.Error("на кнопке создания нет data-ob-list-create — клавише Insert не за что зацепиться")
			}
		})
	}
}

// TestFormActionButtonsCarryKeyHints — подсказка сочетания на самой кнопке.
// Пользователь, который не нашёл клавишу, ведёт себя ровно так же, как если бы
// её не было: с этого и начался разбор («или просто не нашёл как»).
func TestFormActionButtonsCarryKeyHints(t *testing.T) {
	form := &metadata.FormModule{
		Name:       "ФормаОбъекта",
		Kind:       "object",
		EntityName: "Заказ",
		LayoutKind: metadata.FormLayoutManaged,
		Title:      map[string]string{"ru": "Заказ"},
	}
	ent := &metadata.Entity{
		Name:    "Заказ",
		Kind:    metadata.KindDocument,
		Posting: true,
		Forms:   []*metadata.FormModule{form},
	}
	data := map[string]any{
		"Entity":        ent,
		"Form":          form,
		"IsNew":         true,
		"CanWrite":      true,
		"CanPost":       true,
		"Values":        map[string]string{},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{},
		"User":          nil,
		"Lang":          "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate page-managed-form: %v", err)
	}
	html := buf.String()
	for _, want := range []string{`title="Ctrl+Enter"`, `title="Ctrl+S"`} {
		if !strings.Contains(html, want) {
			t.Errorf("на кнопках формы нет подсказки %s — клавишу не найдут", want)
		}
	}
}

// TestKeyboardShortcutsContract — инварианты встроенных клавиш в ui.js.
func TestKeyboardShortcutsContract(t *testing.T) {
	js := string(uiJS)
	for _, tc := range []struct{ want, why string }{
		{"function obInitKeyboardShortcuts", "встроенные клавиши формы и списка"},
		{"obInitKeyboardShortcuts()", "инициализатор должен быть зарегистрирован, иначе клавиш нет"},
		{"'post_and_close'", "Ctrl+Enter — «Провести и закрыть»"},
		{"data-ob-list-create", "Insert — создать элемент списка"},
		{"function listSetSel", "курсор списка нужен и клавиатуре, и клику — через единственную точку смены выделения"},
		{"function obListMoveCursor", "↑/↓ ведут по строкам списка"},
		{"obIsTypingTarget", "в поле ввода стрелки и Enter принадлежат полю, а не списку"},
	} {
		if !strings.Contains(js, tc.want) {
			t.Errorf("ui.js не содержит %q — %s", tc.want, tc.why)
		}
	}
}

// Insert/Delete списка не должны срабатывать, пока пользователь печатает в
// поиске или другом поле. Проверяем порядок guard'а в реальном обработчике:
// прежняя версия проверяла typing только ПОСЛЕ ветки Insert.
func TestListDataShortcutsDoNotHijackTyping(t *testing.T) {
	js := string(uiJS)
	start := strings.Index(js, "function obInitKeyboardShortcuts")
	if start < 0 {
		t.Fatal("ui.js: не найдена obInitKeyboardShortcuts")
	}
	body := js[start:]
	insert := strings.Index(body, "if (e.key === 'Insert')")
	typing := strings.Index(body, "obIsTypingTarget(e.target)")
	if insert < 0 || typing < 0 || typing > insert {
		t.Fatal("typing guard должен выполняться до Insert списка")
	}

	deleteHandler := strings.Index(js, "var sel = listSel();\n    if (e.key === 'Delete'")
	if deleteHandler < 0 {
		t.Fatal("ui.js: не найден Delete списка")
	}
	prefixStart := deleteHandler - 500
	if prefixStart < 0 {
		prefixStart = 0
	}
	if !strings.Contains(js[prefixStart:deleteHandler], "obIsTypingTarget(e.target)") {
		t.Fatal("Delete списка не защищён от нажатия в поле ввода")
	}
}

// TestShortcutsAreLayoutIndependent — буквенные сочетания разбираются по
// e.code, а не по e.key. При русской раскладке Ctrl+S приходит как «ы», и
// сравнение с 's' просто не сработало бы — для русскоязычного продукта это не
// мелочь, а неработающая клавиша у большинства пользователей.
func TestShortcutsAreLayoutIndependent(t *testing.T) {
	js := string(uiJS)
	idx := strings.Index(js, "function obInitKeyboardShortcuts")
	if idx < 0 {
		t.Fatal("ui.js: не найдена obInitKeyboardShortcuts")
	}
	body := js[idx:]
	if end := strings.Index(body, "\nfunction "); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "e.code === 'KeyS'") || !strings.Contains(body, "e.code === 'KeyF'") {
		t.Error("буквенные сочетания разбираются не по e.code — при русской раскладке они не сработают")
	}
}

// TestGridRowKeysContract — Ins/F9/Ctrl+↑↓ для строк табличной части.
func TestGridRowKeysContract(t *testing.T) {
	js := string(managedJS)
	for _, tc := range []struct{ want, why string }{
		{"window.obGridCopyRow", "F9 — копировать строку"},
		{"window.obGridMoveRow", "Ctrl+↑/↓ — переместить строку"},
		{"function reindexOrd", "порядок строк документа хранится в _ord и должен оставаться сплошным"},
		{`e.key === "Insert"`, "Ins — добавить строку"},
		{`e.key === "F9"`, "F9 — копировать строку"},
		{"hasActionableFormHotkey", "доступный явный hotkey формы важнее встроенного значения клавиши"},
		{"g.readOnly", "структурные клавиши не меняют табличную часть только для чтения"},
		{"!commitGridEdit(g)", "невалидный редактор блокирует структурную операцию"},
		{"Object.assign({}, src._obCellClasses", "копия строки не разделяет изменяемые стили ячеек с оригиналом"},
	} {
		if !strings.Contains(js, tc.want) {
			t.Errorf("managed.js не содержит %q — %s", tc.want, tc.why)
		}
	}
}

// Несколько гридов могут одновременно хранить activeCell. Выбор первого при
// обходе map зависел от порядка свойств и отправлял F9/Insert не в ту ТЧ.
func TestGridRowKeysRememberLastInteractedGrid(t *testing.T) {
	js := string(managedJS)
	start := strings.Index(js, "function activeGridName")
	if start < 0 {
		t.Fatal("managed.js: не найдена activeGridName")
	}
	body := js[start:]
	if end := strings.Index(body, "function gridTypingTarget"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "window._obActiveGridName") {
		t.Fatal("последний реально активный грид не запоминается")
	}
	if strings.Contains(body, "for (var tp in grids)") {
		t.Fatal("activeGridName снова выбирает произвольный первый грид с activeCell")
	}
	if !strings.Contains(js, `div.addEventListener("mousedown"`) || !strings.Contains(js, `div.addEventListener("focusin"`) {
		t.Fatal("пользовательское взаимодействие с гридом не обновляет его как последний активный")
	}
}

func TestReadOnlyGridHidesAndRejectsStructuralActions(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{{
			Name:   "Строки",
			Fields: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
		}},
	}
	render := func(readOnly bool) string {
		t.Helper()
		el := &metadata.FormElement{
			Kind:     metadata.FormElementTablePart,
			Name:     "СтрокиФормы",
			DataPath: "Объект.Строки",
			ReadOnly: readOnly,
		}
		ctx := map[string]any{
			"Entity":        ent,
			"TablePartRows": map[string][]map[string]any{"Строки": {}},
			"TPRefOptions":  map[string]any{},
			"TPEnumLabels":  map[string]map[string]map[string]string{},
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "managed-element", map[string]any{"El": el, "Ctx": ctx}); err != nil {
			t.Fatalf("ExecuteTemplate managed-element: %v", err)
		}
		return buf.String()
	}

	readOnly := render(true)
	if !strings.Contains(readOnly, `data-sg-ro="1"`) {
		t.Fatal("readonly-флаг не дошёл до SlickGrid")
	}
	if strings.Contains(readOnly, "data-ob-grid-add") || strings.Contains(readOnly, "data-ob-grid-del") {
		t.Fatal("readonly-таблица показывает структурные кнопки")
	}
	editable := render(false)
	if !strings.Contains(editable, "data-ob-grid-add") || !strings.Contains(editable, "data-ob-grid-del") {
		t.Fatal("редактируемая таблица потеряла структурные кнопки")
	}
}

// TestGridRowKeysUseCapturePhase — обработчик обязан висеть в ФАЗЕ ПЕРЕХВАТА.
// SlickGrid слушает клавиши на канве грида, то есть глубже документа, и в фазе
// всплытия успевает разобрать их первым: Ctrl+↓ у него — «перейти к последней
// строке», из-за чего перемещение строки не срабатывало вовсе. Проверено в
// браузере: без capture сценарий падает.
func TestGridRowKeysUseCapturePhase(t *testing.T) {
	js := string(managedJS)
	idx := strings.Index(js, "window._obGridKeysHook = true;")
	if idx < 0 {
		t.Fatal("managed.js: не найден обработчик клавиш строк ТЧ")
	}
	body := js[idx:]
	if end := strings.Index(body, "// SlickGrid-aware applyTableParts"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "}, true);") {
		t.Error("клавиши строк ТЧ слушаются во всплытии — SlickGrid разберёт Ctrl+↓ раньше и перемещение не сработает")
	}
	if !strings.Contains(body, "e.stopPropagation()") {
		t.Error("без stopPropagation грид выполнит ещё и своё действие по той же клавише")
	}
}
