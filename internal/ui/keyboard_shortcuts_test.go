package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func TestNormalizedFormHotkey(t *testing.T) {
	tests := map[string]string{
		" f2 ": "F2", "f4": "F4", " F7": "F7", "f8 ": "F8", "f9": "F9", " f10 ": "F10",
		"": "", "F1": "", "F11": "", "Ctrl+F9": "", "Delete": "",
	}
	for input, want := range tests {
		if got := normalizedFormHotkey(input); got != want {
			t.Errorf("normalizedFormHotkey(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestManagedButtonRendersOnlyNormalizedSupportedHotkey(t *testing.T) {
	render := func(hotkey string) string {
		t.Helper()
		button := &metadata.FormElement{Kind: metadata.FormElementButton, Name: "Action", HotKey: hotkey}
		var output bytes.Buffer
		if err := tmpl.ExecuteTemplate(&output, "managed-element", map[string]any{
			"El":  button,
			"Ctx": map[string]any{"CanWrite": true},
		}); err != nil {
			t.Fatalf("render managed button: %v", err)
		}
		return output.String()
	}

	supported := render(" f7 ")
	for _, marker := range []string{`data-ob-hotkey="F7"`, `aria-keyshortcuts="F7"`, `title="F7"`} {
		if !strings.Contains(supported, marker) {
			t.Errorf("normalized F7 lost %s: %s", marker, supported)
		}
	}
	unsupported := render("Ctrl+F9")
	for _, marker := range []string{"data-ob-hotkey", "aria-keyshortcuts", `title="Ctrl+F9"`} {
		if strings.Contains(unsupported, marker) {
			t.Errorf("unsupported hotkey rendered %s: %s", marker, unsupported)
		}
	}
}

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

// В общем layout уже есть глобальный input[name=q]. Ctrl+F списка обязан быть
// связан с отдельным стабильным узлом, иначе querySelector выбирает первый
// поиск в шапке и пользователь печатает не в тот form.
func TestPageListCtrlFSearchHasStableAssociation(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Контрагент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	data := map[string]any{
		"Entity": ent, "Rows": []map[string]any{}, "Params": storage.ListParams{},
		"RefFilterOptions": map[string]any{}, "CanWrite": true, "CanDelete": true,
		"Lang": "ru", "Total": 0, "Page": 1, "TotalPages": 1,
	}
	var rendered bytes.Buffer
	if err := tmpl.ExecuteTemplate(&rendered, "page-list", data); err != nil {
		t.Fatalf("ExecuteTemplate page-list: %v", err)
	}
	doc, err := html.Parse(strings.NewReader(rendered.String()))
	if err != nil {
		t.Fatal(err)
	}
	var searches []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "input" {
			if name, _ := keyboardHTMLAttr(node, "name"); name == "q" {
				searches = append(searches, node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if len(searches) < 2 {
		t.Fatalf("ожидались глобальный и списковый q inputs, найдено %d", len(searches))
	}
	var listSearch *html.Node
	for _, input := range searches {
		if id, _ := keyboardHTMLAttr(input, "id"); id == "ob-list-search" {
			if listSearch != nil {
				t.Fatal("id=ob-list-search встречается больше одного раза")
			}
			listSearch = input
		}
	}
	if listSearch == nil {
		t.Fatal("списковый поиск не имеет стабильного id=ob-list-search")
	}
	if _, ok := keyboardHTMLAttr(listSearch, "data-ob-list-search"); !ok {
		t.Fatal("списковый поиск не имеет семантического data-ob-list-search")
	}
	if !strings.Contains(string(uiJS), "document.getElementById('ob-list-search')") {
		t.Fatal("Ctrl+F не связан со стабильным id спискового поиска")
	}
}

func TestListRowShortcutMarkersRespectDeleteAvailability(t *testing.T) {
	render := func(canDelete bool) []*html.Node {
		t.Helper()
		ent := &metadata.Entity{
			Name: "Контрагент", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		rows := []map[string]any{
			{"id": "11111111-1111-1111-1111-111111111111", "Наименование": "Обычный"},
			{"id": "22222222-2222-2222-2222-222222222222", "Наименование": "Предопределённый", "_is_predefined": true},
		}
		var rendered bytes.Buffer
		if err := tmpl.ExecuteTemplate(&rendered, "page-list", map[string]any{
			"Entity": ent, "Rows": rows, "Params": storage.ListParams{},
			"RefFilterOptions": map[string]any{}, "CanDelete": canDelete,
			"Lang": "ru", "Total": 2, "Page": 1, "TotalPages": 1,
		}); err != nil {
			t.Fatalf("ExecuteTemplate page-list: %v", err)
		}
		doc, err := html.Parse(strings.NewReader(rendered.String()))
		if err != nil {
			t.Fatal(err)
		}
		var found []*html.Node
		var walk func(*html.Node)
		walk = func(node *html.Node) {
			if _, ok := keyboardHTMLAttr(node, "data-ob-list-row"); ok {
				found = append(found, node)
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
		walk(doc)
		return found
	}

	rows := render(true)
	if len(rows) != 2 {
		t.Fatalf("rendered list rows=%d, want 2", len(rows))
	}
	if keys, _ := keyboardHTMLAttr(rows[0], "aria-keyshortcuts"); keys != "ArrowUp ArrowDown Enter F2 Delete" {
		t.Fatalf("deletable row shortcuts=%q", keys)
	}
	if keys, _ := keyboardHTMLAttr(rows[1], "aria-keyshortcuts"); keys != "ArrowUp ArrowDown Enter F2" {
		t.Fatalf("predefined row advertises unavailable Delete: %q", keys)
	}
	rows = render(false)
	if keys, _ := keyboardHTMLAttr(rows[0], "aria-keyshortcuts"); keys != "ArrowUp ArrowDown Enter F2" {
		t.Fatalf("row without delete permission advertises Delete: %q", keys)
	}
}

func TestDynamicRowShortcutMarkersFollowRuntimeAvailability(t *testing.T) {
	ui := string(uiJS)
	for _, want := range []string{
		"if (obListConfig().canDelete === true && !row.predefined && row.mark_url) rowShortcuts += ' Delete';",
		"var domWritable = !!(table && !obDOMTableReadOnly(table));",
		"if (domWritable) {",
		"btn.title = 'Delete';",
		"btn.setAttribute('aria-keyshortcuts', 'Delete');",
	} {
		if !strings.Contains(ui, want) {
			t.Errorf("ui.js dynamic row contract missing %q", want)
		}
	}
	managed := string(managedJS)
	if !strings.Contains(managed, "const domWritable = !!(domTable && domTable.getAttribute('data-ob-readonly') === '0');") ||
		!strings.Contains(managed, "if (domWritable) {") {
		t.Error("managed.js does not gate dynamic Delete markers on writable DOM table")
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
	typing := strings.Index(body, "obIsInteractiveTarget(e.target)")
	if insert < 0 || typing < 0 || typing > insert {
		t.Fatal("typing guard должен выполняться до Insert списка")
	}

	deleteHandler := strings.Index(js, "e.key !== 'Delete'")
	if deleteHandler < 0 {
		t.Fatal("ui.js: не найден Delete списка")
	}
	prefixStart := deleteHandler - 500
	if prefixStart < 0 {
		prefixStart = 0
	}
	if !strings.Contains(js[prefixStart:deleteHandler], "obIsInteractiveTarget(e.target)") {
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
		{"if (window.obGridDelRow(tpName))", "клавиша и toolbar используют одну безопасную мутацию удаления"},
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
	if end := strings.Index(body, "function gridInteractiveTarget"); end > 0 {
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
	render := func(readOnly, canWrite bool) string {
		t.Helper()
		el := &metadata.FormElement{
			Kind:     metadata.FormElementTablePart,
			Name:     "СтрокиФормы",
			DataPath: "Объект.Строки",
			ReadOnly: readOnly,
		}
		ctx := map[string]any{
			"Entity":        ent,
			"CanWrite":      canWrite,
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

	readOnly := render(true, true)
	if !strings.Contains(readOnly, `data-sg-ro="1"`) {
		t.Fatal("readonly-флаг не дошёл до SlickGrid")
	}
	if strings.Contains(readOnly, "data-ob-grid-add") || strings.Contains(readOnly, "data-ob-grid-del") {
		t.Fatal("readonly-таблица показывает структурные кнопки")
	}
	if strings.Contains(readOnly, `aria-keyshortcuts="Insert F9 Delete`) {
		t.Fatal("readonly SlickGrid объявляет недоступные структурные клавиши")
	}
	permissionReadOnly := render(false, false)
	if !strings.Contains(permissionReadOnly, `data-sg-ro="1"`) || strings.Contains(permissionReadOnly, "data-ob-grid-add") {
		t.Fatal("CanWrite=false должен fail-closed блокировать SlickGrid")
	}
	editable := render(false, true)
	if !strings.Contains(editable, "data-ob-grid-add") || !strings.Contains(editable, "data-ob-grid-del") {
		t.Fatal("редактируемая таблица потеряла структурные кнопки")
	}
	if !strings.Contains(editable, `aria-keyshortcuts="Insert F9 Delete Control+ArrowUp Control+ArrowDown"`) {
		t.Fatal("редактируемый SlickGrid не объявляет доступные структурные клавиши")
	}
}

func keyboardHTMLAttr(node *html.Node, name string) (string, bool) {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val, true
		}
	}
	return "", false
}

func keyboardFindHTML(node *html.Node, match func(*html.Node) bool) *html.Node {
	if match(node) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := keyboardFindHTML(child, match); found != nil {
			return found
		}
	}
	return nil
}

// NoGrid is a separate DOM implementation, so readonly must be asserted on
// the rendered controls rather than inferred from SlickGrid's data-sg-ro.
func TestManagedNoGridShortcutsAndReadOnlyRender(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{{
			Name:   "Строки",
			Fields: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
		}},
	}

	for _, tc := range []struct {
		name, marker       string
		readOnly, canWrite bool
	}{
		{name: "element readonly", readOnly: true, canWrite: true, marker: "1"},
		{name: "permission readonly", readOnly: false, canWrite: false, marker: "1"},
		{name: "editable", readOnly: false, canWrite: true, marker: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			el := &metadata.FormElement{
				Kind: metadata.FormElementTablePart, Name: "СтрокиФормы",
				DataPath: "Объект.Строки", ReadOnly: tc.readOnly, NoGrid: true,
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnRowAdded:   "СтрокиПриДобавлении",
					metadata.FormEventOnRowDeleted: "СтрокиПриУдалении",
				},
			}
			ctx := map[string]any{
				"Entity": ent, "CanWrite": tc.canWrite,
				"TablePartRows": map[string][]map[string]any{"Строки": {{"Товар": "A"}}},
				"TPRefOptions":  map[string]any{},
				"TPEnumLabels":  map[string]map[string]map[string]string{},
			}
			var rendered bytes.Buffer
			if err := tmpl.ExecuteTemplate(&rendered, "managed-element", map[string]any{"El": el, "Ctx": ctx}); err != nil {
				t.Fatalf("ExecuteTemplate managed-element: %v", err)
			}
			doc, err := html.Parse(strings.NewReader(rendered.String()))
			if err != nil {
				t.Fatalf("parse rendered HTML: %v", err)
			}
			table := keyboardFindHTML(doc, func(node *html.Node) bool {
				value, ok := keyboardHTMLAttr(node, "data-ob-dom-table")
				return node.Type == html.ElementNode && node.Data == "table" && ok && value == "Строки"
			})
			if table == nil {
				t.Fatal("NoGrid table has no DOM-shortcut marker")
			}
			if marker, _ := keyboardHTMLAttr(table, "data-ob-readonly"); marker != tc.marker {
				t.Fatalf("data-ob-readonly=%q, want %q", marker, tc.marker)
			}
			keys, hasKeys := keyboardHTMLAttr(table, "aria-keyshortcuts")
			if tc.marker == "0" && (!hasKeys || keys != "Insert F9 Delete Control+ArrowUp Control+ArrowDown") {
				t.Fatalf("editable table shortcut hint=%q, present=%v", keys, hasKeys)
			}
			if tc.marker == "1" && hasKeys {
				t.Fatalf("readonly table announces unavailable shortcuts %q", keys)
			}
			if element, _ := keyboardHTMLAttr(table, "data-ob-element"); element != "СтрокиФормы" {
				t.Fatalf("row event element=%q", element)
			}
			for _, marker := range []string{"data-ob-rowadd", "data-ob-rowdel"} {
				value, present := keyboardHTMLAttr(table, marker)
				wantPresent := tc.marker == "0"
				if present != wantPresent || present && value != "1" {
					t.Errorf("%s=%q present=%v, want present=%v", marker, value, present, wantPresent)
				}
			}

			field := keyboardFindHTML(table, func(node *html.Node) bool {
				name, _ := keyboardHTMLAttr(node, "name")
				return node.Type == html.ElementNode && node.Data == "input" && name == "tp.Строки.0.Товар"
			})
			remove := keyboardFindHTML(table, func(node *html.Node) bool {
				className, _ := keyboardHTMLAttr(node, "class")
				return node.Type == html.ElementNode && node.Data == "button" && strings.Contains(className, "del-btn")
			})
			add := keyboardFindHTML(doc, func(node *html.Node) bool {
				style, _ := keyboardHTMLAttr(node, "style")
				return node.Type == html.ElementNode && node.Data == "button" && strings.Contains(style, "margin:0 0 12px")
			})
			if field == nil || remove == nil || add == nil {
				t.Fatal("rendered NoGrid table lost a field or structural button")
			}
			wantDisabled := tc.marker == "1"
			_, fieldReadOnly := keyboardHTMLAttr(field, "readonly")
			if fieldReadOnly != wantDisabled {
				t.Errorf("field readonly=%v, want %v", fieldReadOnly, wantDisabled)
			}
			for label, node := range map[string]*html.Node{"remove": remove, "add": add} {
				_, disabled := keyboardHTMLAttr(node, "disabled")
				if disabled != wantDisabled {
					t.Errorf("%s disabled=%v, want %v", label, disabled, wantDisabled)
				}
			}
			for label, node := range map[string]*html.Node{"remove": remove, "add": add} {
				_, hasShortcut := keyboardHTMLAttr(node, "aria-keyshortcuts")
				if hasShortcut == wantDisabled {
					t.Errorf("%s shortcut marker present=%v for readonly=%v", label, hasShortcut, wantDisabled)
				}
				dataAttr := "data-ob-remove-row"
				if label == "add" {
					dataAttr = "data-ob-add-tp"
				}
				_, hasAction := keyboardHTMLAttr(node, dataAttr)
				if hasAction == wantDisabled {
					t.Errorf("%s action marker present=%v for readonly=%v", label, hasAction, wantDisabled)
				}
			}
		})
	}
}

func TestGeneratedFormDOMTableShortcutRender(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{{
			Name:   "Строки",
			Fields: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
		}},
	}
	for _, canWrite := range []bool{true, false} {
		data := map[string]any{
			"Entity": ent, "IsNew": true, "CanWrite": canWrite,
			"Values": map[string]string{}, "RefOptions": map[string]any{},
			"EnumOptions": map[string]any{}, "TPRefOptions": map[string]any{},
			"TPRefMeta":     map[string]any{},
			"TablePartRows": map[string][]map[string]any{"Строки": {{"Товар": "A"}}},
			"Lang":          "ru",
		}
		var rendered bytes.Buffer
		if err := tmpl.ExecuteTemplate(&rendered, "page-form", data); err != nil {
			t.Fatalf("ExecuteTemplate page-form (CanWrite=%v): %v", canWrite, err)
		}
		doc, err := html.Parse(strings.NewReader(rendered.String()))
		if err != nil {
			t.Fatal(err)
		}
		table := keyboardFindHTML(doc, func(node *html.Node) bool {
			value, ok := keyboardHTMLAttr(node, "data-ob-dom-table")
			return node.Type == html.ElementNode && node.Data == "table" && ok && value == "Строки"
		})
		if table == nil {
			t.Fatal("generated form table has no DOM shortcut marker")
		}
		wantMarker := "0"
		if !canWrite {
			wantMarker = "1"
		}
		if marker, _ := keyboardHTMLAttr(table, "data-ob-readonly"); marker != wantMarker {
			t.Fatalf("CanWrite=%v: data-ob-readonly=%q, want %q", canWrite, marker, wantMarker)
		}
		keys, hasKeys := keyboardHTMLAttr(table, "aria-keyshortcuts")
		if canWrite && (!hasKeys || keys != "Insert F9 Delete Control+ArrowUp Control+ArrowDown") {
			t.Fatalf("CanWrite=true: shortcut hint=%q, present=%v", keys, hasKeys)
		}
		if !canWrite && hasKeys {
			t.Fatalf("CanWrite=false announces unavailable shortcuts %q", keys)
		}
		field := keyboardFindHTML(table, func(node *html.Node) bool {
			name, _ := keyboardHTMLAttr(node, "name")
			return node.Type == html.ElementNode && node.Data == "input" && name == "tp.Строки.0.Товар"
		})
		add := keyboardFindHTML(doc, func(node *html.Node) bool {
			name, ok := keyboardHTMLAttr(node, "data-tp-name")
			return node.Type == html.ElementNode && node.Data == "button" && ok && name == "Строки"
		})
		if field == nil || add == nil {
			t.Fatal("generated table lost a field or add button")
		}
		_, fieldDisabled := keyboardHTMLAttr(field, "disabled")
		_, addDisabled := keyboardHTMLAttr(add, "disabled")
		if fieldDisabled != !canWrite || addDisabled != !canWrite {
			t.Fatalf("CanWrite=%v: field disabled=%v, add disabled=%v", canWrite, fieldDisabled, addDisabled)
		}
		_, addShortcut := keyboardHTMLAttr(add, "aria-keyshortcuts")
		if addShortcut != canWrite {
			t.Fatalf("CanWrite=%v: add shortcut marker present=%v", canWrite, addShortcut)
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
