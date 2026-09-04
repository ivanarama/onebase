package ui

import (
	"bytes"
	"html"
	"html/template"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Состояние списка живёт в строке запроса, а панель управления списком собирает
// её в четырёх местах (переключатель вида, заголовки колонок, форма поиска,
// форма отбора). Тесты ниже держат инвариант: одно действие меняет ровно одну
// часть состояния, остальное переносится. Отзыв с демо: поиск и найденные
// строки сбрасывались при переключении «список/плитка».

func listStateEntity() *metadata.Entity {
	return &metadata.Entity{
		Name:         "Номенклатура",
		Kind:         metadata.KindCatalog,
		Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Цена", Type: metadata.FieldTypeNumber},
		},
	}
}

// renderListState рендерит страницу списка так, как её отдал бы обработчик:
// query — параметры запроса, из которых сам обработчик и получил params.
func renderListState(t *testing.T, query url.Values, params storage.ListParams, extra map[string]any) string {
	t.Helper()
	data := map[string]any{
		"Entity":           listStateEntity(),
		"Rows":             []map[string]any{{"id": "11111111-1111-1111-1111-111111111111", "Наименование": "Болт М6", "Цена": "12.5"}},
		"Params":           params,
		"RefFilterOptions": map[string]any{},
		"IsAdmin":          true,
		"CanWrite":         true,
		"Lang":             "ru",
		"Query":            query,
		"Total":            1,
		"Page":             1,
		"TotalPages":       1,
	}
	for k, v := range extra {
		data[k] = v
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-list", data); err != nil {
		t.Fatalf("ExecuteTemplate page-list: %v", err)
	}
	return buf.String()
}

var (
	viewBtnRe     = regexp.MustCompile(`<a class="view-btn[^"]*" href="([^"]*)"`)
	clearBtnRe    = regexp.MustCompile(`<a class="btn btn-sm" href="([^"]*)" style="background:#e2e8f0;color:#475569;align-self:center">`)
	sortLinkRe    = regexp.MustCompile(`<a href="(\?[^"]*sort=[^"]*)"`)
	hiddenInputRe = regexp.MustCompile(`<input type="hidden" name="([^"]*)" value="([^"]*)">`)
)

// linkQuery разбирает href из разметки: html/template отдаёт «&» как «&amp;».
func linkQuery(t *testing.T, href string) url.Values {
	t.Helper()
	u, err := url.Parse(html.UnescapeString(href))
	if err != nil {
		t.Fatalf("не разбирается href %q: %v", href, err)
	}
	return u.Query()
}

func wantParams(t *testing.T, got url.Values, where string, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if v == "" {
			if got.Has(k) {
				t.Errorf("%s: параметр %s=%q должен был исчезнуть", where, k, got.Get(k))
			}
			continue
		}
		if got.Get(k) != v {
			t.Errorf("%s: %s=%q, ожидалось %q", where, k, got.Get(k), v)
		}
	}
}

// TestListViewSwitchKeepsSearch — переключение «список/плитка» не сбрасывает
// поиск, отбор и сортировку: меняется только view.
func TestListViewSwitchKeepsSearch(t *testing.T) {
	query := url.Values{
		"q":         {"Болт"},
		"sort":      {"Цена"},
		"dir":       {"desc"},
		"f.Цена":    {"10"},
		"subsystem": {"Склад"},
		"parent":    {"22222222-2222-2222-2222-222222222222"},
		"page":      {"3"},
	}
	params := storage.ListParams{Search: "Болт", Sort: "Цена", Dir: "desc"}

	page := renderListState(t, query, params, map[string]any{"ParentStr": "22222222-2222-2222-2222-222222222222"})
	links := viewBtnRe.FindAllStringSubmatch(page, -1)
	if len(links) < 2 {
		t.Fatalf("на панели нет переключателя вида: найдено %d ссылок", len(links))
	}

	tiles := linkQuery(t, links[1][1])
	wantParams(t, tiles, "ссылка «Плитка»", map[string]string{
		"view":      "tiles",
		"q":         "Болт",
		"sort":      "Цена",
		"dir":       "desc",
		"f.Цена":    "10",
		"subsystem": "Склад",
		"parent":    "22222222-2222-2222-2222-222222222222",
		"page":      "", // набор строк другой — страница сбрасывается
	})

	// Обратный переход: из плитки в список, поиск по-прежнему на месте.
	tilesQuery := url.Values{"q": {"Болт"}, "view": {"tiles"}, "f.Цена": {"10"}}
	page = renderListState(t, tilesQuery, storage.ListParams{Search: "Болт"}, map[string]any{"TilesView": true})
	links = viewBtnRe.FindAllStringSubmatch(page, -1)
	if len(links) < 2 {
		t.Fatalf("в режиме плитки нет переключателя вида: найдено %d ссылок", len(links))
	}
	list := linkQuery(t, links[0][1])
	wantParams(t, list, "ссылка «Список»", map[string]string{
		"q":      "Болт",
		"f.Цена": "10",
		"view":   "",
	})
}

// TestListSearchFormKeepsView — обратная сторона того же инварианта: поиск не
// выкидывает из плитки и из папки. Форма владеет только q.
func TestListSearchFormKeepsView(t *testing.T) {
	query := url.Values{
		"view":      {"tiles"},
		"parent":    {"22222222-2222-2222-2222-222222222222"},
		"sort":      {"Цена"},
		"dir":       {"desc"},
		"f.Цена":    {"10"},
		"subsystem": {"Склад"},
		"q":         {"Болт"},
		"page":      {"2"},
	}
	page := renderListState(t, query, storage.ListParams{Search: "Болт", Sort: "Цена", Dir: "desc"}, map[string]any{"TilesView": true})

	searchForm := sectionBetween(t, page, `<form method="GET" style="display:flex`, "</form>")
	hidden := map[string]string{}
	for _, m := range hiddenInputRe.FindAllStringSubmatch(searchForm, -1) {
		hidden[html.UnescapeString(m[1])] = html.UnescapeString(m[2])
	}
	for k, want := range map[string]string{
		"view":      "tiles",
		"parent":    "22222222-2222-2222-2222-222222222222",
		"sort":      "Цена",
		"dir":       "desc",
		"f.Цена":    "10",
		"subsystem": "Склад",
	} {
		if hidden[k] != want {
			t.Errorf("форма поиска не переносит %s: %q, ожидалось %q", k, hidden[k], want)
		}
	}
	if _, ok := hidden["q"]; ok {
		t.Error("форма поиска дублирует q скрытым полем — значение поля ввода потерялось бы")
	}
	if _, ok := hidden["page"]; ok {
		t.Error("форма поиска тащит номер страницы: по новому запросу строк меньше")
	}

	// Крестик очистки убирает только поиск.
	clear := clearBtnRe.FindStringSubmatch(page)
	if clear == nil {
		t.Fatal("при активном поиске нет кнопки очистки")
	}
	wantParams(t, linkQuery(t, clear[1]), "кнопка «✕»", map[string]string{
		"q":      "",
		"view":   "tiles",
		"parent": "22222222-2222-2222-2222-222222222222",
	})
}

// TestListFilterFormKeepsSearch — форма отбора владеет только f.*, а «Сбросить»
// сбрасывает отбор, а не весь список.
func TestListFilterFormKeepsSearch(t *testing.T) {
	query := url.Values{
		"q":      {"Болт"},
		"view":   {"tiles"},
		"f.Цена": {"10"},
	}
	page := renderListState(t, query, storage.ListParams{
		Search:  "Болт",
		Filters: map[string]storage.FilterValue{"Цена": {Value: "10"}},
	}, map[string]any{"TilesView": true})

	filterForm := sectionBetween(t, page, `<form method="GET" action="">`, "</form>")
	hidden := map[string]string{}
	for _, m := range hiddenInputRe.FindAllStringSubmatch(filterForm, -1) {
		hidden[html.UnescapeString(m[1])] = html.UnescapeString(m[2])
	}
	if hidden["q"] != "Болт" {
		t.Errorf("применение отбора теряет поиск: q=%q", hidden["q"])
	}
	if hidden["view"] != "tiles" {
		t.Errorf("применение отбора выкидывает из плитки: view=%q", hidden["view"])
	}
	for k := range hidden {
		if strings.HasPrefix(k, "f.") {
			t.Errorf("форма отбора дублирует своё же поле %s скрытым полем", k)
		}
	}

	reset := regexp.MustCompile(`<a class="btn btn-sm" href="([^"]*)" style="background:#e2e8f0;color:#475569">`).FindStringSubmatch(page)
	if reset == nil {
		t.Fatal("в панели отбора нет ссылки «Сбросить»")
	}
	wantParams(t, linkQuery(t, reset[1]), "ссылка «Сбросить»", map[string]string{
		"f.Цена": "",
		"q":      "Болт",
		"view":   "tiles",
	})
}

// TestListSortLinkKeepsSearchAndFolder — сортировка по колонке меняет только
// sort/dir: поиск и текущая папка остаются.
func TestListSortLinkKeepsSearchAndFolder(t *testing.T) {
	query := url.Values{
		"q":         {"Болт"},
		"parent":    {"22222222-2222-2222-2222-222222222222"},
		"subsystem": {"Склад"},
		"sort":      {"Наименование"},
		"dir":       {"asc"},
	}
	page := renderListState(t, query, storage.ListParams{Search: "Болт", Sort: "Наименование", Dir: "asc"},
		map[string]any{"ParentStr": "22222222-2222-2222-2222-222222222222"})

	links := sortLinkRe.FindAllStringSubmatch(page, -1)
	if len(links) == 0 {
		t.Fatal("в шапке таблицы нет ссылок сортировки")
	}
	first := linkQuery(t, links[0][1])
	wantParams(t, first, "заголовок «Наименование»", map[string]string{
		"sort":      "Наименование",
		"dir":       "desc", // повторный клик по текущей колонке переворачивает порядок
		"q":         "Болт",
		"parent":    "22222222-2222-2222-2222-222222222222",
		"subsystem": "Склад",
	})
}

// TestListURLHelpers — семантика самих помощников: пустое значение убирает
// параметр, "f.*" убирает группу, отсутствующий запрос не роняет рендер.
func TestListURLHelpers(t *testing.T) {
	funcs := templateFuncs(nil)
	listURL, ok := funcs["listURL"].(func(any, ...string) template.URL)
	if !ok {
		t.Fatal("шаблонам недоступен listURL — ссылки списка снова собираются по кускам")
	}
	listHidden, ok := funcs["listHidden"].(func(any, ...string) template.HTML)
	if !ok {
		t.Fatal("шаблонам недоступен listHidden")
	}

	src := url.Values{
		"q":           {"Болт"},
		"f.Цена":      {"10"},
		"f.Дата.from": {"2026-01-01"},
		"page":        {"5"},
		"submit":      {"shadow-native-form-method"},
		"method":      {"POST"},
		"unknown":     {"not-list-state"},
	}
	got := linkQuery(t, string(listURL(src, "view", "tiles", "f.*", "")))
	wantParams(t, got, "listURL", map[string]string{
		"q":           "Болт",
		"view":        "tiles",
		"f.Цена":      "",
		"f.Дата.from": "",
		"page":        "",
	})
	for _, key := range []string{"submit", "method", "unknown"} {
		if got.Has(key) {
			t.Errorf("listURL перенёс посторонний параметр %q", key)
		}
	}

	if u := listURL(src, "q", ""); strings.Contains(string(u), "q=") {
		t.Errorf("listURL: пустое значение не убрало параметр: %s", u)
	}
	// Исходные параметры не должны меняться под ногами у следующего вызова.
	if src.Get("q") != "Болт" || src.Get("page") != "5" {
		t.Error("listURL правит переданные параметры запроса вместо копии")
	}
	if u := listURL(nil); u != "?" {
		t.Errorf("listURL без запроса дал %q, ожидалось \"?\"", u)
	}
	if h := listHidden(nil, "q"); h != "" {
		t.Errorf("listHidden без запроса дал %q", h)
	}
	// Значение с кавычкой не должно вырваться из атрибута.
	h := string(listHidden(url.Values{"q": {`"><script>`}}))
	if strings.Contains(h, `"><script>`) {
		t.Errorf("listHidden не экранирует значение: %s", h)
	}
	poisoned := string(listHidden(url.Values{"q": {"Болт"}, "submit": {"x"}, "action": {"/elsewhere"}}))
	if strings.Contains(poisoned, `name="submit"`) || strings.Contains(poisoned, `name="action"`) {
		t.Errorf("listHidden допускает DOM clobbering через постороннее имя поля: %s", poisoned)
	}
}

func TestTreeSearchAndFilterLeaveTreeMode(t *testing.T) {
	query := url.Values{
		"view":      {"tree"},
		"q":         {"Болт"},
		"f.Цена":    {"10"},
		"subsystem": {"Склад"},
	}
	page := renderListState(t, query, storage.ListParams{
		Search:  "Болт",
		Filters: map[string]storage.FilterValue{"Цена": {Value: "10"}},
	}, map[string]any{"TreeView": true})

	searchForm := sectionBetween(t, page, `<form method="GET" style="display:flex`, "</form>")
	filterForm := sectionBetween(t, page, `<form method="GET" action="">`, "</form>")
	for name, form := range map[string]string{"поиска": searchForm, "отбора": filterForm} {
		for _, m := range hiddenInputRe.FindAllStringSubmatch(form, -1) {
			if html.UnescapeString(m[1]) == "view" {
				t.Errorf("форма %s сохранила view=tree: дерево не применяет поиск и отбор", name)
			}
		}
	}
	if !strings.Contains(searchForm, `name="f.Цена" value="10"`) {
		t.Error("форма поиска потеряла действующий отбор при выходе из дерева")
	}
	if !strings.Contains(filterForm, `name="q" value="Болт"`) {
		t.Error("форма отбора потеряла поиск при выходе из дерева")
	}
}

func TestListAutoSubmitCannotBeClobberedByNamedControl(t *testing.T) {
	js := string(uiJS)
	for _, want := range []string{
		"window.HTMLFormElement && window.HTMLFormElement.prototype",
		"nativeSubmit.call(form)",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("автосабмит списка не содержит защиту %q", want)
		}
	}
	if strings.Contains(js, "input.form.submit();") {
		t.Error("автосабмит вызывает clobberable input.form.submit() напрямую")
	}
}

func TestListPaginationAndModeKeepSort(t *testing.T) {
	query := url.Values{
		"q":         {"Болт"},
		"sort":      {"Цена"},
		"dir":       {"desc"},
		"f.Цена":    {"10"},
		"view":      {"tiles"},
		"parent":    {"22222222-2222-2222-2222-222222222222"},
		"lm":        {"pages"},
		"subsystem": {"Склад"},
		"page":      {"2"},
	}
	params := storage.ListParams{
		Search: "Болт", Sort: "Цена", Dir: "desc",
		Filters: map[string]storage.FilterValue{"Цена": {Value: "10"}},
	}
	page := renderListState(t, query, params, map[string]any{
		"TilesView": true, "Page": 2, "Total": 100, "TotalPages": 5,
		"HasPrev": true, "HasNext": true, "PrevPage": 1, "NextPage": 3,
	})

	for label, wantPage := range map[string]string{"← Назад": "1", "Вперёд →": "3"} {
		got := linkQuery(t, linkHrefByText(t, page, label))
		wantParams(t, got, label, map[string]string{
			"page": wantPage, "sort": "Цена", "dir": "desc", "q": "Болт",
			"f.Цена": "10", "view": "tiles", "parent": "22222222-2222-2222-2222-222222222222",
		})
	}
	mode := linkQuery(t, linkHrefByText(t, page, "≣ Лента"))
	wantParams(t, mode, "переключатель ленты", map[string]string{
		"lm": "feed", "page": "", "sort": "Цена", "dir": "desc", "q": "Болт",
	})

	query.Set("lm", "feed")
	query.Set("page", "1")
	feedPage := renderListState(t, query, params, map[string]any{
		"TilesView": true, "Feed": true, "Page": 1, "Total": 100, "TotalPages": 5,
		"HasNext": true, "NextPage": 2,
	})
	more := linkQuery(t, linkHrefByText(t, feedPage, "Показать ещё"))
	wantParams(t, more, "fallback ленты", map[string]string{
		"lm": "feed", "page": "2", "sort": "Цена", "dir": "desc", "q": "Болт",
	})
}

func TestGroupNavigationKeepsListState(t *testing.T) {
	const nextParent = "11111111-1111-1111-1111-111111111111"
	query := url.Values{
		"q":         {"Болт"},
		"sort":      {"Цена"},
		"dir":       {"desc"},
		"f.Цена":    {"10"},
		"view":      {"tiles"},
		"lm":        {"feed"},
		"parent":    {"22222222-2222-2222-2222-222222222222"},
		"subsystem": {"Склад"},
		"page":      {"4"},
	}
	page := renderListState(t, query, storage.ListParams{Search: "Болт", Sort: "Цена", Dir: "desc"}, map[string]any{
		"TilesView":   true,
		"Rows":        []map[string]any{{"id": nextParent, "is_folder": true, "Наименование": "Крепёж"}},
		"Breadcrumbs": []map[string]string{{"ID": "33333333-3333-3333-3333-333333333333", "Label": "Метизы"}},
	})

	// Ссылка входа в группу собирается клиентом из шаблона контейнера: в разметке
	// лежит один data-ob-row-folder-tpl с плейсхолдером вместо идентификатора.
	folderMatch := regexp.MustCompile(`data-ob-row-folder-tpl="([^"]*)"`).FindStringSubmatch(page)
	if folderMatch == nil {
		t.Fatal("у списка нет шаблона data-ob-row-folder-tpl")
	}
	folder := linkQuery(t, strings.ReplaceAll(folderMatch[1], "__ID__", nextParent))
	wantParams(t, folder, "вход в группу", map[string]string{
		"parent": nextParent, "q": "Болт", "sort": "Цена", "dir": "desc",
		"f.Цена": "10", "view": "tiles", "lm": "feed", "subsystem": "Склад", "page": "",
	})

	root := linkQuery(t, linkHrefByText(t, page, "Корень"))
	wantParams(t, root, "breadcrumb корня", map[string]string{
		"parent": "", "q": "Болт", "sort": "Цена", "dir": "desc", "view": "tiles", "lm": "feed",
	})
	crumb := linkQuery(t, linkHrefByText(t, page, "Метизы"))
	wantParams(t, crumb, "breadcrumb группы", map[string]string{
		"parent": "33333333-3333-3333-3333-333333333333", "q": "Болт", "sort": "Цена", "dir": "desc",
	})
}

// TestJournalExcelLinkStartsQuery — ссылка выгрузки журнала начинает строку
// запроса, а не продолжает чужую: с «&» отбор уезжал в путь и сервер отдавал
// 404 (маршрут /ui/journal/{name}/excel не совпадал).
func TestJournalExcelLinkStartsQuery(t *testing.T) {
	j := &metadata.Journal{
		Name:    "Заказы",
		Columns: []metadata.JournalColumn{{Label: "Номер", Field: "Номер"}},
		Filters: []metadata.JournalFilter{{Field: "Дата", Type: "date_range"}},
	}
	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, "page-journal", map[string]any{
		"Journal":                j,
		"JournalColumns":         j.Columns,
		"JournalSettingsColumns": journalSettingsColumns(j, nil),
		"JournalSettingsJSON":    journalSettingsJSON(j, nil),
		"Rows":                   []map[string]any{},
		"Total":                  0,
		"Params": storage.ListParams{Filters: map[string]storage.FilterValue{
			"Дата": {From: "2026-01-01"},
		}},
		"FilterOptions": map[string][]map[string]any{},
		"ColFormats":    map[string]string{},
		"RequestURI":    "/ui/journal/заказы",
		"Cfg":           Config{},
		"Lang":          "ru",
	})
	if err != nil {
		t.Fatalf("execute page-journal: %v", err)
	}

	link := regexp.MustCompile(`href="(/ui/journal/[^"]*excel[^"]*)"`).FindStringSubmatch(buf.String())
	if link == nil {
		t.Fatal("в журнале нет ссылки выгрузки в Excel")
	}
	href := html.UnescapeString(link[1])
	u, err := url.Parse(href)
	if err != nil {
		t.Fatalf("не разбирается href %q: %v", href, err)
	}
	if !strings.HasSuffix(u.Path, "/excel") {
		t.Errorf("отбор попал в путь ссылки: %q (путь %q)", href, u.Path)
	}
	if got := u.Query().Get("f.Дата.from"); got != "2026-01-01" {
		t.Errorf("ссылка выгрузки потеряла отбор: f.Дата.from=%q", got)
	}
}

func TestJournalFilterResetKeepsSubsystem(t *testing.T) {
	j := &metadata.Journal{
		Name:    "Заказы",
		Columns: []metadata.JournalColumn{{Label: "Номер", Field: "Номер"}},
		Filters: []metadata.JournalFilter{{Field: "Дата", Type: "date_range"}},
	}
	query := url.Values{
		"subsystem":   {"Продажи"},
		"f.Дата.from": {"2026-01-01"},
		"offset":      {"100"},
	}
	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, "page-journal", map[string]any{
		"Journal":                j,
		"JournalColumns":         j.Columns,
		"JournalSettingsColumns": journalSettingsColumns(j, nil),
		"JournalSettingsJSON":    journalSettingsJSON(j, nil),
		"Rows":                   []map[string]any{},
		"Params": storage.ListParams{Filters: map[string]storage.FilterValue{
			"Дата": {From: "2026-01-01"},
		}},
		"FilterOptions":    map[string][]map[string]any{},
		"ColFormats":       map[string]string{},
		"RequestURI":       "/ui/journal/заказы?subsystem=Продажи&f.Дата.from=2026-01-01&offset=100",
		"CurrentSubsystem": "Продажи",
		"Query":            query,
		"Cfg":              Config{},
		"Lang":             "ru",
	})
	if err != nil {
		t.Fatalf("execute page-journal: %v", err)
	}
	reset := linkQuery(t, linkHrefByText(t, buf.String(), "Сбросить"))
	wantParams(t, reset, "сброс отбора журнала", map[string]string{
		"subsystem":   "Продажи",
		"f.Дата.from": "",
		"offset":      "",
	})
}

// sectionBetween вырезает кусок разметки от start до первого следующего end.
func sectionBetween(t *testing.T, page, start, end string) string {
	t.Helper()
	i := strings.Index(page, start)
	if i < 0 {
		t.Fatalf("в разметке нет фрагмента %q", start)
	}
	rest := page[i:]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("фрагмент %q не закрыт %q", start, end)
	}
	return rest[:j]
}

func linkHrefByText(t *testing.T, page, text string) string {
	t.Helper()
	re := regexp.MustCompile(`<a[^>]*href="([^"]*)"[^>]*>[^<]*` + regexp.QuoteMeta(text) + `[^<]*</a>`)
	match := re.FindStringSubmatch(page)
	if match == nil {
		t.Fatalf("в разметке нет ссылки с текстом %q", text)
	}
	return match[1]
}
