package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// TestPageList_HasActionsButton — smoke-тест плана 41: страница списка
// рендерится и содержит кнопку «Действия» на панели (id="list-actions-btn"),
// а JS-runtime списка живёт в /static/ui.js, читает JSON-конфиг страницы и
// вызывается через data-ob-* вместо inline handlers.
func TestPageList_HasActionsButton(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}

	rows := []map[string]any{
		{"id": "11111111-1111-1111-1111-111111111111", "Наименование": "ООО Ромашка"},
	}

	data := map[string]any{
		"Entity":           ent,
		"Rows":             rows,
		"Params":           storage.ListParams{},
		"RefFilterOptions": map[string]any{},
		"IsAdmin":          true,
		"CanWrite":         true,
		"CanDelete":        true,
		"CanUnpost":        true,
		"Lang":             "ru",
		"Total":            1,
		"Page":             1,
		"TotalPages":       1,
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-list", data); err != nil {
		t.Fatalf("ExecuteTemplate page-list: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, `id="list-actions-btn"`) {
		t.Error("на панели списка нет кнопки «Действия» (id=list-actions-btn)")
	}
	for _, want := range []string{
		`data-ob-list-actions`,
		`data-ob-auto-submit="320"`,
		`data-ob-list-row`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("страница списка не содержит delegated marker %q", want)
		}
	}
	for _, old := range []string{
		`onclick="listActionsBtnClick(event)"`,
		`oninput="clearTimeout(window._srch)`,
		`onclick="listRowClick(event,this)"`,
		`ondblclick="listRowDblClick(event,this)"`,
		`oncontextmenu="listCtxMenu(event,this)"`,
	} {
		if strings.Contains(html, old) {
			t.Errorf("страница списка содержит старый inline handler %q", old)
		}
	}

	if !strings.Contains(html, `id="ob-list-config"`) {
		t.Error("список не содержит JSON-конфиг ob-list-config")
	}
	if strings.Contains(html, "function listMenuItems") || strings.Contains(html, "function showListMenu") {
		t.Error("runtime списка должен жить в /static/ui.js, а не в HTML")
	}
	js := string(uiJS)
	for _, want := range []string{"function listMenuItems", "function showListMenu", "function listActionsBtnClick", "function obInitListDelegates"} {
		if !strings.Contains(js, want) {
			t.Errorf("/static/ui.js не содержит %q", want)
		}
	}
}

// TestPageList_ActionsButtonStartsInactive — на только что открытом списке
// строка не выбрана, поэтому кнопка «Действия» приходит с сервера уже
// приглушённой (aria-disabled) и с подсказкой-причиной в title. Именно
// серверная разметка, а не JS: если состояние ставить только скриптом, кнопка
// успевает мигнуть активной. Прятать кнопку нельзя — тогда её не было бы на
// каждом первом открытии списка и о ней бы просто не узнали, поэтому тест
// заодно следит, что кнопка в разметке ЕСТЬ.
func TestPageList_ActionsButtonStartsInactive(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	data := map[string]any{
		"Entity":           ent,
		"Rows":             []map[string]any{{"id": "11111111-1111-1111-1111-111111111111", "Наименование": "ООО Ромашка"}},
		"Params":           storage.ListParams{},
		"RefFilterOptions": map[string]any{},
		"CanWrite":         true,
		"CanDelete":        true,
		"Lang":             "ru",
		"Total":            1,
		"Page":             1,
		"TotalPages":       1,
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-list", data); err != nil {
		t.Fatalf("ExecuteTemplate page-list: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, `id="list-actions-btn"`) {
		t.Fatal("кнопка «Действия» исчезла из разметки — её нельзя прятать при пустом выборе")
	}
	if !strings.Contains(html, `data-ob-list-actions aria-disabled="true"`) {
		t.Error("кнопка «Действия» отрендерена активной, хотя строка не выбрана (нет aria-disabled=\"true\")")
	}
	if !strings.Contains(html, `title="Сначала выберите строку списка"`) {
		t.Error("приглушённая кнопка не объясняет причину в title")
	}
	if strings.Contains(html, `disabled>`) || strings.Contains(html, ` disabled `) {
		t.Error("на кнопке атрибут disabled: браузер погасит клик, и объяснить причину будет негде")
	}
	// Приглушение — это стиль по aria-disabled, а не отдельный класс: иначе
	// состояние кнопки пришлось бы держать в двух местах.
	if !strings.Contains(html, `.btn[aria-disabled="true"]`) {
		t.Error("нет CSS-правила для приглушённой кнопки — визуально она осталась обычной")
	}
	if !strings.Contains(html, "actionsReady") {
		t.Error("в ob-list-config нет метки actionsReady — после выбора строки title не на что вернуть")
	}
}

// TestListRuntime_SelectionIsValidatedAgainstDOM — выделение строки живёт в
// переменной ui.js, а живой список (план 87) заменяет строки контейнера целиком.
// Отцепленный от документа или скрытый свёрнутым деревом узел выглядит как
// «ничего не выбрано» (подсветки нет), но переменная остаётся непустой — команды
// и Delete сработали бы по записи, которую пользователь не выбирал и не видит.
// Поэтому источник правды один: listSel() со сверкой DOM и видимости.
func TestListRuntime_SelectionIsValidatedAgainstDOM(t *testing.T) {
	js := string(uiJS)

	for _, want := range []string{
		"function listSel()",
		"if (_listSel && (!document.contains(_listSel) || !obElementVisible(_listSel)))",
		"function obElementVisible(",
		"function listSetSel(",
		"function listSyncActionsBtn(",
		"function listRestoreSel(",
		"function listMenuNoSel(",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("/static/ui.js не содержит %q", want)
		}
	}

	// Клавиша Delete и меню обязаны спрашивать выделение у listSel(), а не
	// читать переменную: чтение напрямую — это ровно тот путь, который бьёт по
	// отцепленной строке.
	if !strings.Contains(js, "if (e.key === 'Delete' && sel && obListConfig().canDelete)") {
		t.Error("обработчик Delete не сверяет выделение через listSel()")
	}
	if strings.Contains(js, "e.key === 'Delete' && _listSel") {
		t.Error("обработчик Delete по-прежнему читает _listSel напрямую (сработает по отцепленной строке)")
	}
	if strings.Contains(js, "showListMenu(listMenuItems(_listSel)") {
		t.Error("меню «Действия» строится по _listSel напрямую вместо listSel()")
	}

	// Живое обновление списка не должно молча съедать выбор пользователя.
	if !strings.Contains(js, "listRestoreSel(selKey, cur)") {
		t.Error("после живого обновления списка выделение не восстанавливается")
	}
	if !strings.Contains(js, "var selMine = cur.contains(listSel());") {
		t.Error("живое обновление трогает выделение, не проверив, что оно принадлежит этому списку")
	}
}

// TestListRuntime_NoAlertOnEmptySelection — клик по приглушённой кнопке
// открывает то же меню с неактивными пунктами и причиной сверху. Модальный
// alert() на предсказуемое состояние — упрёк, который требует «ОК» и ничего не
// показывает; неактивное меню заодно показывает состав команд.
func TestListRuntime_NoAlertOnEmptySelection(t *testing.T) {
	js := string(uiJS)

	if strings.Contains(js, "alert(obListLabel('selectRowFirst'") {
		t.Error("клик по «Действиям» без выбранной строки по-прежнему показывает alert()")
	}
	if !strings.Contains(js, "showListMenu(sel ? listMenuItems(sel) : listMenuNoSel(), r.left, r.bottom)") {
		t.Error("без выбранной строки кнопка не открывает меню-подсказку")
	}
	// Причина отделена от команд отдельным видом пункта (hint), иначе она
	// читается как ещё одна неактивная команда.
	if !strings.Contains(js, "if (item.hint) {") {
		t.Error("showListMenu не умеет рисовать пояснительную строку меню")
	}
	if !strings.Contains(js, "hint: true }") {
		t.Error("в меню без выбора нет пояснительной строки «Сначала выберите строку списка»")
	}
}

func TestPageList_EmbeddedOpenUsesShell(t *testing.T) {
	ent := &metadata.Entity{
		Name: "ЗаказПокупателя",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
	}
	data := map[string]any{
		"Entity":           ent,
		"Rows":             []map[string]any{{"id": "11111111-1111-1111-1111-111111111111", "Номер": "ЗПК-00001"}},
		"Params":           storage.ListParams{},
		"RefFilterOptions": map[string]any{},
		"Lang":             "ru",
		"Total":            1,
		"Page":             1,
		"TotalPages":       1,
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-list", data); err != nil {
		t.Fatalf("ExecuteTemplate page-list: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		`id="ob-list-config"`,
		`data-open-url="/ui/document/`,
		`11111111-1111-1111-1111-111111111111"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("список не содержит embedded-open фрагмент %q", want)
		}
	}
	if !strings.Contains(html, `data-folder-url="/ui/document/`) || !strings.Contains(html, `parent=11111111-1111-1111-1111-111111111111`) {
		t.Error("строка списка не содержит data-folder-url для навигации по папкам")
	}
	js := string(uiJS)
	for _, want := range []string{
		`window.obOpenInShell && window.obOpenInShell(url, title || listTitle())`,
		`window.location.href = tr.dataset.folderUrl`,
		`else listOpen(tr.dataset.openUrl);`,
		`fn: function () { listOpen(tr.dataset.openUrl); }`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("/static/ui.js не содержит embedded-open фрагмент %q", want)
		}
	}
	if strings.Contains(js, `window.location.href = tr.dataset.openUrl`) {
		t.Error("открытие записи из списка по-прежнему заменяет текущий iframe вместо новой вкладки")
	}
}

// TestPageList_TilesView — режим «Плитка» (Фаза 1a): при TilesView=true список
// рендерится карточками (.tile-grid/.tile-card) с теми же data-*, что и строки
// таблицы (переиспользование обработчиков), а в панели есть переключатель
// режима отображения (.view-switch).
func TestPageList_TilesView(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Цена", Type: metadata.FieldTypeNumber},
		},
	}
	rows := []map[string]any{
		{"id": "11111111-1111-1111-1111-111111111111", "Наименование": "Болт М6", "Цена": "12.5"},
	}
	data := map[string]any{
		"Entity":           ent,
		"Rows":             rows,
		"Params":           storage.ListParams{},
		"RefFilterOptions": map[string]any{},
		"IsAdmin":          true,
		"CanWrite":         true,
		"Lang":             "ru",
		"TilesView":        true,
		"Total":            1,
		"Page":             1,
		"TotalPages":       1,
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-list", data); err != nil {
		t.Fatalf("ExecuteTemplate page-list (tiles): %v", err)
	}
	html := buf.String()

	for _, want := range []string{"tile-grid", "tile-card", "Болт М6", "view-switch", "data-open-url=", "data-ob-list-row"} {
		if !strings.Contains(html, want) {
			t.Errorf("плиточный режим: в выводе нет ожидаемого фрагмента %q", want)
		}
	}
}
