package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/page"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// TestPageCustomTemplate отрисовывает шаблон page-custom со всеми типами блоков.
// Ловит ошибки доступа к полям и вызовов FuncMap (pageRaw/pageChart/echartsJSON)
// без поднятия полноценного сервера.
func TestPageCustomTemplate(t *testing.T) {
	b := interpreter.NewPageBuilder()
	b.CallMethod("заголовок", []any{"Заголовок"})
	b.CallMethod("абзац", []any{"Абзац"})
	b.CallMethod("показатель", []any{"KPI", 42.0, "number"})
	b.CallMethod("кнопка", []any{"Кнопка", "/ui/"})
	b.CallMethod("разделитель", nil)
	b.CallMethod("добавитьсыройhtml", []any{"<b>ok</b>"})

	tbl := b.CallMethod("таблица", []any{"Таблица"}).(*interpreter.DSLPageTable)
	tbl.CallMethod("колонки", []any{"A"})
	row := tbl.CallMethod("добавитьстроку", nil).(*interpreter.DSLPageRow)
	row.CallMethod("установить", []any{"A", "x"})
	row.CallMethod("ссылка", []any{"A", "/ui/catalog/Товар/1"})

	lst := b.CallMethod("список", []any{"Список"}).(*interpreter.DSLPageList)
	lst.CallMethod("пункт", []any{"Пункт", "/ui/"})

	ch := b.CallMethod("график", []any{"График", "line"}).(*interpreter.DSLPageChart)
	ch.CallMethod("категории", []any{"Янв", "Фев"})
	ch.CallMethod("серия", []any{"S", interpreter.NewArray([]any{1.0, 2.0})})

	var buf bytes.Buffer
	data := map[string]any{
		"PageTitle":    "Тест",
		"PageBlocks":   b.Blocks(),
		"PageHasChart": true,
		"Cfg":          Config{},
		"Lang":         "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-custom", data); err != nil {
		t.Fatalf("execute page-custom: %v", err)
	}
	out := buf.String()
	// URL в href нормализуется html/template (кириллица percent-кодируется),
	// поэтому проверяем ASCII-префикс пути ячейки-ссылки.
	for _, want := range []string{"Заголовок", "<b>ok</b>", "/ui/catalog/", "data-pagechart", `id="ob-page-charts"`, "echarts.min.js"} {
		if !strings.Contains(out, want) {
			t.Errorf("в выводе нет %q", want)
		}
	}
	if strings.Contains(out, `window.__obPageCharts`) {
		t.Errorf("page-custom снова генерирует inline JS для графиков")
	}
	// Сырой HTML не должен пройти экранирование (pageRaw), а текст блоков —
	// должен (нет «живого» тега из текста).
	if strings.Contains(out, "&lt;b&gt;ok") {
		t.Errorf("сырой HTML был экранирован")
	}
}

// TestPageCustomActionButton проверяет рендер кнопки-действия (план 66): она
// должна стать POST-формой на /ui/page/<имя>/action/<действие> (с сохранением
// query string), а обычная кнопка-ссылка — остаться <a href>.
func TestPageCustomActionButton(t *testing.T) {
	b := interpreter.NewPageBuilder()
	b.CallMethod("кнопка", []any{"Открыть", "/ui/catalog/Товар"})
	b.CallMethod("кнопкадействие", []any{"Пересчитать", "ПересчитатьИтоги"})

	var buf bytes.Buffer
	data := map[string]any{
		"PageTitle":      "Тест",
		"PageBlocks":     b.Blocks(),
		"PageActionBase": "/ui/page/Панель/action/",
		"PageQuery":      "?период=2026",
		"Cfg":            Config{},
		"Lang":           "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-custom", data); err != nil {
		t.Fatalf("execute page-custom: %v", err)
	}
	out := buf.String()
	// Кнопка-действие — POST-форма на /action/ (ASCII-часть пути не кодируется).
	if !strings.Contains(out, `method="post"`) {
		t.Errorf("кнопка-действие должна рендериться POST-формой:\n%s", out)
	}
	if !strings.Contains(out, "/action/") {
		t.Errorf("в action-URL нет сегмента /action/:\n%s", out)
	}
	// Обычная кнопка осталась ссылкой.
	if !strings.Contains(out, `<a href="/ui/catalog/`) {
		t.Errorf("кнопка-ссылка должна остаться <a href>:\n%s", out)
	}
}

// TestPageAction_RunsProcAndRedirects проверяет обработчик кнопки-действия:
// POST /ui/page/{name}/action/{action} находит процедуру в .page.os, исполняет
// её (Сообщить копится в стор), затем PRG-редиректом 303 возвращает на страницу
// с сохранением Параметров (query string).
func TestPageAction_RunsProcAndRedirects(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	src := `Процедура ПриФормировании(Страница, Параметры) Экспорт
  Страница.Заголовок("Тест");
КонецПроцедуры

Процедура Отметить(Страница, Параметры) Экспорт
  Сообщить("действие за период: " + Параметры.Получить("period"));
КонецПроцедуры`
	prog := mustParse(t, src)

	registry := runtime.NewRegistry()
	registry.LoadPages([]*page.Page{{Name: "Тест"}})
	registry.Load(runtime.LoadOptions{
		PagePrograms: map[string]*ast.Program{"Тест": prog},
	})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	s := &Server{
		store:    db,
		reg:      registry,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}

	// Имя страницы/действия — percent-encoded (как в ссылках меню), чтобы заодно
	// проверить decodePathParam; query — ASCII, чтобы ассерт на Location был чистым.
	req := httptest.NewRequest("POST", "/ui/page/%D0%A2%D0%B5%D1%81%D1%82/action/%D0%9E%D1%82%D0%BC%D0%B5%D1%82%D0%B8%D1%82%D1%8C?period=2026", nil)
	// URL-параметры маршрута задаём вручную (без поднятия роутера chi).
	// Значения percent-encoded: «Тест» и «Отметить».
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "%D0%A2%D0%B5%D1%81%D1%82")
	rctx.URLParams.Add("action", "%D0%9E%D1%82%D0%BC%D0%B5%D1%82%D0%B8%D1%82%D1%8C")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	s.pageAction(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303 See Other, получен %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "period=2026") {
		t.Errorf("редирект должен сохранять Параметры (query): %q", loc)
	}
	// Сообщить из действия — в сторе сообщений (бар поллит /ui/messages).
	msgs := s.messages.List("_anonymous")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "действие за период: 2026") {
		t.Errorf("Сообщить действия не собрано: %+v", msgs)
	}
}

// TestPageAction_ExportGate проверяет экспорт-гейт действий страниц (ревью #10):
// POST на ВНУТРЕННЮЮ (не помеченную «Экспорт») процедуру модуля .page.os должен
// отклоняться (404) — её побочные эффекты нельзя дёрнуть из обхода назначения, —
// а на ЭКСПОРТНУЮ кнопку-действие проходить и исполняться.
func TestPageAction_ExportGate(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// ВнутрПересчёт — без «Экспорт» (внутренняя вспомогательная), ПубличноеДействие
	// — экспортная кнопка-действие.
	src := `Процедура ПриФормировании(Страница, Параметры) Экспорт
  Страница.Заголовок("Тест");
КонецПроцедуры

Процедура ВнутрПересчёт(Страница, Параметры)
  Сообщить("внутренняя процедура вызвана");
КонецПроцедуры

Процедура ПубличноеДействие(Страница, Параметры) Экспорт
  Сообщить("публичное действие вызвано");
КонецПроцедуры`
	prog := mustParse(t, src)

	registry := runtime.NewRegistry()
	registry.LoadPages([]*page.Page{{Name: "Тест"}})
	registry.Load(runtime.LoadOptions{
		PagePrograms: map[string]*ast.Program{"Тест": prog},
	})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	s := &Server{
		store:    db,
		reg:      registry,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}

	call := func(action string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/ui/page/Тест/action/"+action, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("name", "Тест")
		rctx.URLParams.Add("action", action)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		s.pageAction(rec, req)
		return rec
	}

	// Внутренняя процедура — отказ (404), без исполнения.
	if rec := call("ВнутрПересчёт"); rec.Code != http.StatusNotFound {
		t.Fatalf("вызов внутренней процедуры: код %d, ожидался 404", rec.Code)
	}
	if msgs := s.messages.List("_anonymous"); len(msgs) != 0 {
		t.Fatalf("внутренняя процедура НЕ должна исполняться: %+v", msgs)
	}

	// Экспортная кнопка-действие — проходит (303) и исполняется.
	if rec := call("ПубличноеДействие"); rec.Code != http.StatusSeeOther {
		t.Fatalf("вызов экспортного действия: код %d, ожидался 303", rec.Code)
	}
	msgs := s.messages.List("_anonymous")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "публичное действие вызвано") {
		t.Errorf("экспортное действие не исполнено: %+v", msgs)
	}
}

// TestLocalizePageBlocks_TranslatesLabelsNotData проверяет авто-i18n подписей
// блоков страницы (план 66, доработка 3): статические подписи переводятся через
// Bundle, а данные из запросов (значение показателя, ячейки таблицы, URL) —
// остаются нетронутыми.
func TestLocalizePageBlocks_TranslatesLabelsNotData(t *testing.T) {
	dir := t.TempDir()
	en := `{
		"Заголовок": "Heading",
		"Позиций": "Items count",
		"Номенклатура": "Items",
		"Наименование": "Name",
		"Продажи": "Sales",
		"Быстрые ссылки": "Quick links",
		"Контрагенты": "Counterparties",
		"Открыть": "Open"
	}`
	if err := os.WriteFile(filepath.Join(dir, "en.json"), []byte(en), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle, err := i18n.Load(os.DirFS(dir), "")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	s := &Server{cfg: Config{Bundle: bundle}}

	b := interpreter.NewPageBuilder()
	b.CallMethod("заголовок", []any{"Заголовок"})
	b.CallMethod("показатель", []any{"Позиций", 42.0, "number"})
	tbl := b.CallMethod("таблица", []any{"Номенклатура"}).(*interpreter.DSLPageTable)
	tbl.CallMethod("колонки", []any{"Наименование"})
	row := tbl.CallMethod("добавитьстроку", nil).(*interpreter.DSLPageRow)
	row.CallMethod("установить", []any{"Наименование", "Болт М6"}) // данные — не переводить
	ch := b.CallMethod("график", []any{"Продажи", "line"}).(*interpreter.DSLPageChart)
	ch.CallMethod("серия", []any{"Выручка", interpreter.NewArray([]any{1.0})})
	lst := b.CallMethod("список", []any{"Быстрые ссылки"}).(*interpreter.DSLPageList)
	lst.CallMethod("пункт", []any{"Контрагенты", "/ui/catalog/Контрагент"})
	b.CallMethod("кнопка", []any{"Открыть", "/ui/"})

	blocks := b.Blocks()
	s.localizePageBlocks("en", blocks)

	checks := []struct {
		got, want, what string
	}{
		{blocks[0].Text, "Heading", "heading.Text"},
		{blocks[1].Label, "Items count", "kpi.Label"},
		{blocks[1].Value, "42", "kpi.Value (данные не трогаем)"},
		{blocks[2].Title, "Items", "table.Title"},
		{blocks[2].ColumnLabels[0], "Name", "table.ColumnLabels[0] (отображение)"},
		{blocks[2].Columns[0], "Наименование", "table.Columns[0] (ключ — не переводится)"},
		{blocks[2].Rows[0].Cells["Наименование"].Text, "Болт М6", "ячейка таблицы (данные)"},
		{blocks[3].Title, "Sales", "chart.Title"},
		{blocks[4].Title, "Quick links", "list.Title"},
		{blocks[4].Items[0].Text, "Counterparties", "item.Text"},
		{blocks[4].Items[0].URL, "/ui/catalog/Контрагент", "item.URL (данные)"},
		{blocks[5].Text, "Open", "button.Text"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, ожидалось %q", c.what, c.got, c.want)
		}
	}

	// Сквозная проверка: переведённые подписи доходят до HTML, а данные ячеек
	// (адресуются ключом «Наименование») остаются на месте при переведённом
	// заголовке колонки «Name».
	var buf bytes.Buffer
	data := map[string]any{
		"PageTitle": "Тест", "PageBlocks": blocks, "PageHasChart": true,
		"Cfg": Config{}, "Lang": "en",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-custom", data); err != nil {
		t.Fatalf("execute page-custom: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Heading", "Items count", "Name", "Болт М6", "Sales", "Quick links", "Counterparties", "Open"} {
		if !strings.Contains(out, want) {
			t.Errorf("в HTML нет %q", want)
		}
	}
}

// Русская локаль (и отсутствие словаря/Bundle) — no-op: подписи проходят без
// изменений (Bundle.T возвращает непереведённый ключ как есть).
func TestLocalizePageBlocks_Noop(t *testing.T) {
	build := func() []interpreter.PageBlock {
		b := interpreter.NewPageBuilder()
		b.CallMethod("заголовок", []any{"Сводка"})
		return b.Blocks()
	}

	bundle, _ := i18n.Load(os.DirFS(t.TempDir()), "") // нет словарей
	for _, tc := range []struct {
		name string
		s    *Server
		lang string
	}{
		{"русский", &Server{cfg: Config{Bundle: bundle}}, "ru"},
		{"nil-bundle", &Server{cfg: Config{}}, "en"},
		{"пустой lang", &Server{cfg: Config{Bundle: bundle}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocks := build()
			tc.s.localizePageBlocks(tc.lang, blocks)
			if blocks[0].Text != "Сводка" {
				t.Errorf("подпись изменилась: %q", blocks[0].Text)
			}
		})
	}
}

// TestPageNStr_DefaultsToRequestLanguage проверяет явный уровень i18n (план 66,
// п.3): НСтр("ru='…'; en='…'") в обработчике страницы без явного кода языка берёт
// язык запроса — в т.ч. для статической части склеенной строки
// (НСтр(...) + Период), которую авто-перевод подписей целиком не покрывает.
func TestPageNStr_DefaultsToRequestLanguage(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	src := `Процедура ПриФормировании(Страница, Параметры) Экспорт
  Страница.Заголовок(НСтр("ru = 'Сводка'; en = 'Summary'"));
  Страница.Абзац(НСтр("ru = 'Отчёт за '; en = 'Report for '") + Параметры.Получить("период"));
КонецПроцедуры`
	prog := mustParse(t, src)

	registry := runtime.NewRegistry()
	registry.LoadPages([]*page.Page{{Name: "Тест"}})
	registry.Load(runtime.LoadOptions{PagePrograms: map[string]*ast.Program{"Тест": prog}})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	// Bundle нужен лишь чтобы resolveLang вернул язык; сам НСтр словарём не
	// пользуется (inline-формат). Язык запроса задаём базовым (cfg.Lang).
	bundle, err := i18n.Load(os.DirFS(t.TempDir()), "")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		store:    db,
		reg:      registry,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
		cfg:      Config{Bundle: bundle, Lang: "en"},
	}

	req := httptest.NewRequest("GET", "/ui/page/Тест?период=Июнь", nil)

	var msgs []string
	builder, paramsObj, dslVars, _ := s.pageProcEnv(req, &msgs)
	proc := registry.GetPageProcedure("Тест", "ПриФормировании")
	if proc == nil {
		t.Fatal("процедура ПриФормировании не найдена")
	}
	if _, err := interp.Call(proc, builder, []any{builder, paramsObj}, dslVars); err != nil {
		t.Fatalf("interp.Call: %v", err)
	}

	blocks := builder.Blocks()
	if blocks[0].Text != "Summary" {
		t.Errorf("НСтр(...en) без кода = %q, ожидалось Summary (язык запроса)", blocks[0].Text)
	}
	if blocks[1].Text != "Report for Июнь" {
		t.Errorf("склеенная подпись = %q, ожидалось 'Report for Июнь'", blocks[1].Text)
	}
}

// TestDecodePathParam фиксирует фикс маршрута /ui/page/{name}: имя из URL должно
// декодироваться, иначе ссылка из меню (percent-encoding в нижнем регистре hex,
// при котором chi отдаёт сырой сегмент) даёт 404, хотя верхний регистр проходит.
func TestDecodePathParam(t *testing.T) {
	cases := map[string]string{
		"%d0%9f%d0%b0%d0%bd%d0%b5%d0%bb%d1%8c": "Панель", // нижний регистр — ссылки меню
		"%D0%9F%D0%B0%D0%BD%D0%B5%D0%BB%D1%8C": "Панель", // верхний регистр
		"Панель":                               "Панель", // уже декодировано (нет «%»)
		"":                                     "",       // пусто
	}
	for in, want := range cases {
		if got := decodePathParam(in); got != want {
			t.Errorf("decodePathParam(%q) = %q, хотел %q", in, got, want)
		}
	}
}
