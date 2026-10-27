package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Помеченные на удаление в выборе ссылок. Ввод данных их скрывает, поиск по
// истории (отбор списка, параметр отчёта) показывает с отметкой; ключ
// choice_show_deleted перекрывает правило в обе стороны. Уже выбранное
// значение в документе показывается как есть и записывается.

type hideDeletedFixture struct {
	s               *Server
	db              *storage.DB
	ref, doc        *metadata.Entity
	active, deleted uuid.UUID
}

func newHideDeletedFixture(t *testing.T, refKey, fieldKey *bool) *hideDeletedFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ref := &metadata.Entity{Name: "Мастера", Kind: metadata.KindCatalog, ChoiceShowDeleted: refKey,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	doc := &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, ListFilters: []string{"Мастер"}, Fields: []metadata.Field{
		{Name: "Мастер", Type: metadata.FieldType("reference:Мастера"), RefEntity: "Мастера", ChoiceShowDeleted: fieldKey},
	}}
	f := &hideDeletedFixture{db: db, ref: ref, doc: doc, active: uuid.New(), deleted: uuid.New()}
	f.s = newSubmitTestServerOn(t, db, []*metadata.Entity{ref, doc})
	f.s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ref, doc}, Reports: []*report.Report{{
		Name: "ДолгиМастеров", Query: "ВЫБРАТЬ 1 КАК Х",
		Params: []report.Param{{Name: "Мастер", Type: "reference:Мастера"}},
	}}})
	if err := db.Upsert(ctx, ref.Name, f.active, map[string]any{"Наименование": "Действующий"}, ref); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ref.Name, f.deleted, map[string]any{"Наименование": "Уволенный"}, ref); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkForDeletion(ctx, ref.Name, f.deleted, true); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *hideDeletedFixture) options(t *testing.T, query string) map[string]string {
	t.Helper()
	rec := serveRefOptions(t, f.s, f.ref.Name, query, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: code %d %s", query, rec.Code, rec.Body.String())
	}
	var got struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, it := range got.Items {
		id, _ := it["id"].(string)
		label, _ := it["_label"].(string)
		labels[id] = label
	}
	if got.Total != len(labels) {
		t.Fatalf("%s: total %d, а строк %d — счётчик разошёлся с выдачей", query, got.Total, len(labels))
	}
	return labels
}

func TestRefOptions_DeletedHiddenOnInputShownOnSearch(t *testing.T) {
	f := newHideDeletedFixture(t, nil, nil)
	cases := []struct {
		query       string
		wantDeleted bool
	}{
		{"", false},                  // поле ввода
		{"search=1", true},           // отбор списка, параметр отчёта
		{"marked=1", true},           // ключ поля true
		{"search=1&marked=0", false}, // ключ поля false на поиске
	}
	for _, c := range cases {
		labels := f.options(t, c.query)
		if labels[f.active.String()] == "" {
			t.Errorf("%q: нет действующего элемента", c.query)
		}
		label, shown := labels[f.deleted.String()]
		if shown != c.wantDeleted {
			t.Errorf("%q: помеченный показан=%v, ждали %v", c.query, shown, c.wantDeleted)
		}
		if shown && !strings.Contains(label, "помечен на удаление") {
			t.Errorf("%q: у помеченного нет отметки: %q", c.query, label)
		}
	}
}

// Отметка — на языке интерфейса, а не на языке отката словаря. С настоящим
// словарём выдача подбора без языка в контексте отдавала «(marked for
// deletion)» русскому интерфейсу (проверка cc2 на 1027b).
func TestRefOptions_DeletedMarkUsesInterfaceLanguage(t *testing.T) {
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		cfgLang, accept, want string
	}{
		{"", "", "Уволенный (помечен на удаление)"},
		{"ru", "", "Уволенный (помечен на удаление)"},
		{"", "en-US,en;q=0.9", "Уволенный (marked for deletion)"},
	} {
		f := newHideDeletedFixture(t, nil, nil)
		f.s.cfg.Bundle = bundle
		f.s.cfg.Lang = c.cfgLang
		target := "/ui/_ref-options/" + f.ref.Name + "?search=1"
		req := reqWithChi(http.MethodGet, target, nil, map[string]string{"entity": f.ref.Name})
		if c.accept != "" {
			req.Header.Set("Accept-Language", c.accept)
		}
		rec := httptest.NewRecorder()
		f.s.refOptionsJSON(rec, req)
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("язык базы %q, Accept-Language %q: ждали %q, ответ %s", c.cfgLang, c.accept, c.want, rec.Body.String())
		}
	}
}

func TestRefOptions_CatalogKeyOverridesBothWays(t *testing.T) {
	yes, no := true, false
	show := newHideDeletedFixture(t, &yes, nil)
	if _, ok := show.options(t, "")[show.deleted.String()]; !ok {
		t.Error("choice_show_deleted: true у справочника — помеченный должен быть в выборе")
	}
	if _, ok := show.options(t, "marked=0")[show.deleted.String()]; ok {
		t.Error("ключ поля false главнее ключа справочника")
	}
	hide := newHideDeletedFixture(t, &no, nil)
	if _, ok := hide.options(t, "search=1")[hide.deleted.String()]; ok {
		t.Error("choice_show_deleted: false у справочника — и в отборе помеченного нет")
	}
}

func (f *hideDeletedFixture) render(t *testing.T, path string, params map[string]string, handler func(http.ResponseWriter, *http.Request)) string {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(rec, reqWithChi(http.MethodGet, path, nil, params))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: code %d %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestDocumentForm_DeletedHiddenButSelectedKeptAndSaved(t *testing.T) {
	f := newHideDeletedFixture(t, nil, nil)
	params := map[string]string{"kind": "document", "entity": f.doc.Name}
	body := f.render(t, "/ui/document/Заявка/new", params, f.s.form)
	if !strings.Contains(body, "Действующий") || strings.Contains(body, "Уволенный") {
		t.Fatal("в новом документе выбор должен предлагать только действующих")
	}
	if strings.Contains(body, `data-ref-search=`) || strings.Contains(body, `data-ref-marked=`) {
		t.Fatal("поле ввода без ключа не должно нести признаков показа помеченных")
	}

	// Документ, где уже выбран помеченный: значение видно как есть и записывается.
	ctx := context.Background()
	id := uuid.New()
	if err := f.db.Upsert(ctx, f.doc.Name, id, map[string]any{"Мастер": f.deleted.String()}, f.doc); err != nil {
		t.Fatal(err)
	}
	edit := map[string]string{"kind": "document", "entity": f.doc.Name, "id": id.String()}
	body = f.render(t, "/ui/document/Заявка/"+id.String(), edit, f.s.formEdit)
	if !strings.Contains(body, f.deleted.String()) || !strings.Contains(body, "Уволенный") {
		t.Fatal("уже выбранный помеченный мастер должен остаться в поле")
	}
	form := url.Values{"Мастер": {f.deleted.String()}, "_action": {""}}
	req := reqWithChi(http.MethodPost, "/ui/document/Заявка/"+id.String(), form, edit)
	rec := httptest.NewRecorder()
	f.s.submitEdit(rec, req)
	if rec.Code >= 400 {
		t.Fatalf("запись документа с помеченным мастером: %d %s", rec.Code, rec.Body.String())
	}
	row, err := f.db.GetByID(ctx, f.doc.Name, id, f.doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := refValueString(row["Мастер"]); got != f.deleted.String() {
		t.Fatalf("после записи мастер %q, ждали прежнего помеченного", got)
	}
}

func TestDocumentForm_FieldKeyShowsDeleted(t *testing.T) {
	yes := true
	f := newHideDeletedFixture(t, nil, &yes)
	body := f.render(t, "/ui/document/Заявка/new", map[string]string{"kind": "document", "entity": f.doc.Name}, f.s.form)
	if !strings.Contains(body, "Уволенный") || !strings.Contains(body, "помечен на удаление") {
		t.Fatal("choice_show_deleted: true у реквизита — помеченный в выборе с отметкой")
	}
	if !strings.Contains(body, `data-ref-marked="1"`) {
		t.Fatal("подбор и быстрый ввод поля должны узнать ключ: нет data-ref-marked=\"1\"")
	}
}

func TestReportParam_ShowsDeletedWithMark(t *testing.T) {
	f := newHideDeletedFixture(t, nil, nil)
	body := f.render(t, "/ui/report/ДолгиМастеров", map[string]string{"name": "ДолгиМастеров"}, f.s.reportForm)
	if !strings.Contains(body, "Уволенный (помечен на удаление)") {
		t.Fatal("параметр отчёта — поиск по истории: помеченный мастер должен быть в выборе с отметкой")
	}
	if !strings.Contains(body, `data-ref-search="1"`) {
		t.Fatal("подбор параметра отчёта должен знать, что это поиск: нет data-ref-search=\"1\"")
	}
}

func TestListFilter_ShowsDeletedWithMark(t *testing.T) {
	f := newHideDeletedFixture(t, nil, nil)
	body := f.render(t, "/ui/document/Заявка", map[string]string{"kind": "document", "entity": f.doc.Name}, f.s.list)
	if !strings.Contains(body, "Уволенный (помечен на удаление)") {
		t.Fatal("отбор списка — поиск по истории: помеченный мастер должен быть в выборе с отметкой")
	}
	if !strings.Contains(body, `data-ref-search="1"`) {
		t.Fatal("подбор в отборе списка должен знать, что это поиск: нет data-ref-search=\"1\"")
	}
}
