package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// detail_fields — поля детальной записи отчёта компоновки (аналог «Выбранных
// полей» 1С). Строка детализации показывает значения записи, строки групп и
// итогов — пустые ячейки в тех же колонках. Тест идёт через production-маршрут
// отчёта (GET /ui/report/{name}?__run=1), как пользователь.
func TestReportDetailFieldsHTTP(t *testing.T) {
	rep := &reportpkg.Report{
		Name:  "otk",
		Query: `ВЫБРАТЬ "Мастер Иванов" КАК Мастер, "СИНТ-001" КАК Номер, "Замена <платы>" КАК Наименование, 2 КАК Штраф`,
		Composition: &reportpkg.Composition{
			Groupings: []string{"Мастер"},
			Detail:    true,
			Totals:    reportpkg.Totals{Grand: true},
			Measures:  []reportpkg.Measure{{Field: "Штраф", Title: "Штраф"}},
			DetailFields: []reportpkg.DetailField{
				{Field: "Номер", Title: "Номер заявки"},
				{Field: "Наименование"},
			},
		},
	}
	s := newReportExportTestServer(t, rep)
	router := chi.NewRouter()
	s.Mount(router)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ui/report/otk?__run=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("report returned %d: %s", w.Code, w.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(w.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	var table *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if table != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "tr" {
			if cls, _ := managedHTMLAttr(n, "class"); cls == "det" {
				table = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if table == nil {
		t.Fatalf("no detail row in report: %s", w.Body.String())
	}
	cells := []string{}
	for c := table.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "td" {
			cells = append(cells, strings.TrimSpace(nodeTextOf(c)))
		}
	}
	// link/indent cell, then detail fields in declared order, then measures.
	want := []string{"", "СИНТ-001", "Замена <платы>", "2"}
	if strings.Join(cells, "|") != strings.Join(want, "|") {
		t.Fatalf("detail row cells %q, want %q", cells, want)
	}
	page := w.Body.String()
	for _, header := range []string{">Номер заявки</th>", ">Наименование</th>"} {
		if !strings.Contains(page, header) {
			t.Fatalf("header %q missing", header)
		}
	}
	if strings.Contains(page, "Замена <платы>") {
		t.Fatal("detail value is not escaped")
	}
	// Group and grand rows keep the column grid with empty detail cells.
	if !strings.Contains(page, `<td>ВСЕГО</td><td class="detail-field" style=""></td><td class="detail-field" style=""></td>`) {
		t.Fatalf("grand row does not keep empty detail columns: %s", page)
	}
}

func TestComposedRowsDetailFields(t *testing.T) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	saved := time.Local
	time.Local = moscow
	t.Cleanup(func() { time.Local = saved })
	// Как отдаёт SQLite: местная полночь 12.11 — это 21:00 UTC 11.11; момент
	// без типа колонки приходит строкой RFC3339 в UTC.
	day := time.Date(2026, 11, 11, 21, 0, 0, 0, time.UTC)
	stamp := time.Date(2026, 11, 12, 11, 5, 9, 0, time.UTC)
	spec := &reportpkg.Composition{
		Groupings:    []string{"Мастер"},
		Detail:       true,
		Measures:     []reportpkg.Measure{{Field: "Штраф"}},
		DetailFields: []reportpkg.DetailField{{Field: "Номер", Title: "Номер заявки"}, {Field: "Дата"}, {Field: "Установлен"}, {Field: "Отказ"}, {Field: "Код"}},
	}
	res, err := compose.Compose([]compose.Row{{"Мастер": "Иванов", "Номер": "СИНТ-001", "Дата": day, "Установлен": stamp, "Отказ": "2026-11-20T09:00:00Z", "Код": "2026-ТЕСТ", "Штраф": 2}}, *spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	headers, rows := composedRows(res, spec)
	if strings.Join(headers, "|") != "Мастер|Номер заявки|Дата|Установлен|Отказ|Код|Штраф" {
		t.Fatalf("headers %q", headers)
	}
	if len(rows) != 2 {
		t.Fatalf("rows %v", rows)
	}
	group, detail := rows[0], rows[1]
	for i := 1; i <= 5; i++ {
		if group[i] != nil {
			t.Fatalf("group row has detail values: %v", group)
		}
	}
	// Даты — в местном времени; строка-момент — тоже дата; текст остаётся текстом.
	if detail[1] != "СИНТ-001" || detail[2] != "12.11.2026" || detail[3] != "12.11.2026 14:05:09" ||
		detail[4] != "20.11.2026 12:00:00" || detail[5] != "2026-ТЕСТ" {
		t.Fatalf("detail row %v", detail)
	}
	if v, ok := detail[6].(float64); !ok || v != 2 {
		t.Fatalf("measure cell %#v", detail[6])
	}
	if got := fmtDetailVal("2026-11-20T09:00:00Z"); got != "20.11.2026 12:00:00" {
		t.Fatalf("HTML detail value %q", got)
	}
}

func nodeTextOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// Поле, которое одновременно detail_link (переход «→» по UUID) и колонка
// detail_fields, показывается представлением ссылки, а не UUID. Переход
// продолжает вести на UUID записи.
func TestReportDetailFieldSameAsDetailLinkShowsLabelHTTP(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	order := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	if err := db.Migrate(ctx, []*metadata.Entity{order}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, order.Name, id, map[string]any{"Наименование": "PT-R-A"}, order); err != nil {
		t.Fatal(err)
	}
	rep := &reportpkg.Report{
		Name:  "второйкруг",
		Query: `ВЫБРАТЬ З.Ссылка КАК ПервыйКругЗаявка, "Мастер" КАК Мастер, 1 КАК Штраф ИЗ Справочник.Заявка КАК З`,
		Composition: &reportpkg.Composition{
			Groupings: []string{"Мастер"}, Detail: true,
			DetailLink: "ПервыйКругЗаявка", DetailEntity: "Заявка",
			DetailFields: []reportpkg.DetailField{{Field: "ПервыйКругЗаявка", Title: "Заявка"}},
			Measures:     []reportpkg.Measure{{Field: "Штраф"}},
		},
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{order}, Reports: []*reportpkg.Report{rep}})
	s := &Server{store: db, reg: registry}
	t.Cleanup(s.Close)
	router := chi.NewRouter()
	s.Mount(router)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ui/report/"+url.PathEscape("второйкруг")+"?__run=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("report returned %d: %s", w.Code, w.Body.String())
	}
	page := w.Body.String()
	if !strings.Contains(page, `<td class="detail-field" style="">PT-R-A</td>`) {
		t.Fatalf("detail_fields column does not show the reference label: %s", page)
	}
	if strings.Contains(page, `<td class="detail-field" style="">`+id.String()) {
		t.Fatal("detail_fields column shows the UUID")
	}
	if !strings.Contains(page, `href="/ui/document/`+url.PathEscape("Заявка")+`/`+id.String()+`"`) {
		t.Fatalf("drill-down no longer links to the record UUID: %s", page)
	}
}
