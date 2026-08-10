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
// опирается, и инварианты ассетов. В браузере (headless Edge + CDP, настоящие
// события клавиатуры) сценарии проверялись отдельно.

// TestPageListCreateKeyboardMarker — кнопке «Создать» нужен стабильный маркер:
// по нему Ins находит её на странице списка. Селектор по href сломался бы от
// любой правки маршрута.
func TestPageListCreateKeyboardMarker(t *testing.T) {
	ent := &metadata.Entity{
		Name:   "Контрагент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
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
		t.Error("на кнопке «Создать» нет data-ob-list-create — клавише Insert не за что зацепиться")
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
		{"function listSelectRow", "курсор списка нужен и клавиатуре, и клику"},
		{"function obListMoveCursor", "↑/↓ ведут по строкам списка"},
		{"obIsTypingTarget", "в поле ввода стрелки и Enter принадлежат полю, а не списку"},
	} {
		if !strings.Contains(js, tc.want) {
			t.Errorf("ui.js не содержит %q — %s", tc.want, tc.why)
		}
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
		{`data-ob-hotkey="F9"`, "явный hotkey кнопки формы важнее встроенного значения клавиши"},
	} {
		if !strings.Contains(js, tc.want) {
			t.Errorf("managed.js не содержит %q — %s", tc.want, tc.why)
		}
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
