package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func testJournal() *metadata.Journal {
	return &metadata.Journal{
		Name:  "Журнал",
		Title: "Журнал",
		Columns: []metadata.JournalColumn{
			{Field: "Номер", Label: "Номер"},
			{Field: "Контрагент", Label: "Контрагент"},
			{Field: "Сумма", Label: "Сумма"},
		},
	}
}

func TestEffectiveJournalColumns(t *testing.T) {
	j := testJournal()
	st := &JournalUserSettings{Columns: []JournalColumnSetting{
		{Field: "сумма", Visible: true},
		{Field: "Номер", Visible: false},
		{Field: "НетТакого", Visible: true},
		{Field: "Сумма", Visible: false},
	}}

	visible := effectiveJournalColumns(j, st)
	if len(visible) != 2 || visible[0].Field != "Сумма" || visible[1].Field != "Контрагент" {
		t.Fatalf("visible columns: %+v", visible)
	}
	panel := journalSettingsColumns(j, st)
	if len(panel) != 3 {
		t.Fatalf("panel columns: %+v", panel)
	}
	if panel[0].Column.Field != "Сумма" || !panel[0].Visible {
		t.Fatalf("first panel column should be visible Сумма, got %+v", panel[0])
	}
	if panel[1].Column.Field != "Номер" || panel[1].Visible {
		t.Fatalf("second panel column should be hidden Номер, got %+v", panel[1])
	}
	if panel[2].Column.Field != "Контрагент" || !panel[2].Visible {
		t.Fatalf("new/unseen YAML column should be appended visible, got %+v", panel[2])
	}
}

func TestJournalSettingsJSONCanonical(t *testing.T) {
	j := testJournal()
	raw := journalSettingsJSON(j, &JournalUserSettings{Columns: []JournalColumnSetting{
		{Field: "сумма", Visible: true},
		{Field: "Номер", Visible: false},
		{Field: "НетТакого", Visible: true},
	}})
	for _, want := range []string{`"field":"Сумма"`, `"field":"Номер"`, `"visible":false`, `"field":"Контрагент"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("canonical settings do not contain %q: %s", want, raw)
		}
	}
	if strings.Contains(raw, "НетТакого") {
		t.Fatalf("canonical settings must drop unknown fields: %s", raw)
	}
}

func TestJournalSettingsPanelRender(t *testing.T) {
	j := testJournal()
	st := &JournalUserSettings{Columns: []JournalColumnSetting{{Field: "Сумма", Visible: true}, {Field: "Номер", Visible: false}}}
	var buf bytes.Buffer
	data := map[string]any{
		"Journal":                j,
		"JournalColumns":         effectiveJournalColumns(j, st),
		"JournalSettingsColumns": journalSettingsColumns(j, st),
		"JournalSettingsJSON":    journalSettingsJSON(j, st),
		"JournalSettingsActive":  true,
		"Rows":                   []map[string]any{{"_doc_kind": "Док", "id": "1", "Сумма": "10", "Номер": "N1", "Контрагент": "К"}},
		"Total":                  1,
		"Params":                 storage.ListParams{Filters: map[string]storage.FilterValue{}},
		"FilterOptions":          map[string][]map[string]any{},
		"ColFormats":             map[string]string{},
		"RequestURI":             "/ui/journal/журнал",
		"Cfg":                    Config{},
		"Lang":                   "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-journal", data); err != nil {
		t.Fatalf("execute page-journal: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`data-block="journal-settings"`, `name="__journal_settings"`, `id="jl-columns"`, `data-field="Номер"`, `data-ob-jl-before-submit`, `data-ob-jl-move="-1"`, `data-ob-journal-open-url="/ui/document/%d0%b4%d0%be%d0%ba/1"`, "изменено"} {
		if !strings.Contains(out, want) {
			t.Fatalf("journal settings panel missing %q:\n%s", want, out)
		}
	}
	for _, old := range []string{
		`onsubmit="return jlBeforeSubmit(event)"`,
		`onclick="jlMove(this,-1)"`,
		`onclick="jlMove(this,1)"`,
		`window.jlMove=function`,
		`window.jlBeforeSubmit=function`,
		`onclick="if(event.target.tagName`,
	} {
		if strings.Contains(out, old) {
			t.Fatalf("journal template still contains inline handler/runtime %q:\n%s", old, out)
		}
	}
	for _, want := range []string{"function jlCollect", "function obInitJournalDelegates"} {
		if !strings.Contains(string(uiJS), want) {
			t.Fatalf("/static/ui.js does not contain %q", want)
		}
	}
	if !strings.Contains(out, `<th>Сумма</th>`) {
		t.Fatalf("visible column should be rendered in data table:\n%s", out)
	}
	if strings.Contains(out, `<th>Номер</th>`) {
		t.Fatalf("hidden column should not be rendered in data table:\n%s", out)
	}
}

func TestJournalFilterDelegatedRefPicker(t *testing.T) {
	j := testJournal()
	j.Filters = []metadata.JournalFilter{{Field: "Контрагент", Type: "reference:Контрагент"}}
	var buf bytes.Buffer
	data := map[string]any{
		"Journal":                j,
		"JournalColumns":         effectiveJournalColumns(j, nil),
		"JournalSettingsColumns": journalSettingsColumns(j, nil),
		"JournalSettingsJSON":    journalSettingsJSON(j, nil),
		"Rows":                   []map[string]any{},
		"Total":                  0,
		"Params":                 storage.ListParams{Filters: map[string]storage.FilterValue{}},
		"FilterOptions":          map[string][]map[string]any{"Контрагент": {{"id": "c1", "_label": "Ромашка"}}},
		"FilterRefEntities":      map[string]string{"Контрагент": "Контрагент"},
		"ColFormats":             map[string]string{},
		"RequestURI":             "/ui/journal/журнал",
		"Cfg":                    Config{},
		"Lang":                   "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-journal", data); err != nil {
		t.Fatalf("execute page-journal: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `data-ob-ref-picker="jflt-Контрагент"`) {
		t.Fatalf("journal filter missing delegated ref picker:\n%s", out)
	}
	if strings.Contains(out, `onclick="openRefPicker('jflt-`) {
		t.Fatalf("journal filter still contains inline ref picker:\n%s", out)
	}
}

func TestJournalSettingsSaveErrorSurfaces(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "journal-settings-err.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close() // закрытое хранилище: сохранение обязано вернуть 500, а не молчаливый redirect

	j := testJournal()
	registry := runtime.NewRegistry()
	registry.LoadJournals([]*metadata.Journal{j})
	s := &Server{store: db, reg: registry}

	form := url.Values{"__journal_settings": {`{"columns":[{"field":"Сумма","visible":false}]}`}}
	r := reqWithChi(http.MethodPost, "/ui/journal/Журнал/settings/save", form, map[string]string{"name": "Журнал"})
	w := httptest.NewRecorder()
	s.journalSettingsSave(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on store error, got %d (location=%q)", w.Code, w.Header().Get("Location"))
	}
}

func TestJournalSettingsSaveReset(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "journal-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	j := testJournal()
	registry := runtime.NewRegistry()
	registry.LoadJournals([]*metadata.Journal{j})
	s := &Server{store: db, reg: registry}

	form := url.Values{
		"__journal_settings": {`{"columns":[{"field":"Сумма","visible":true},{"field":"Номер","visible":false}]}`},
		"__return":           {"/ui/journal/журнал?f.Номер=42"},
	}
	r := reqWithChi(http.MethodPost, "/ui/journal/Журнал/settings/save", form, map[string]string{"name": "Журнал"})
	w := httptest.NewRecorder()
	s.journalSettingsSave(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save status: got %d body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/ui/journal/") || !strings.Contains(strings.ToLower(loc), "f.%d0%9d%d0%be%d0%bc%d0%b5%d1%80=42") {
		t.Fatalf("save redirect: %q", loc)
	}
	got, _ := db.GetJournalUserSettings(ctx, "Журнал", "")
	if !strings.Contains(got, `"field":"Сумма"`) || !strings.Contains(got, `"field":"Контрагент"`) || strings.Contains(got, "НетТакого") {
		t.Fatalf("saved canonical settings: %s", got)
	}

	r2 := reqWithChi(http.MethodPost, "/ui/journal/Журнал/settings/reset", url.Values{"__return": {"/ui/journal/журнал"}}, map[string]string{"name": "Журнал"})
	w2 := httptest.NewRecorder()
	s.journalSettingsReset(w2, r2)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("reset status: got %d body=%s", w2.Code, w2.Body.String())
	}
	if got, _ := db.GetJournalUserSettings(ctx, "Журнал", ""); got != "" {
		t.Fatalf("settings after reset: %s", got)
	}
}
