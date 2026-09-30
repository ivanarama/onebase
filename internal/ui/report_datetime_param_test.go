package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Параметр отчёта со временем суток (#1204, план 160). Поле обязано принимать
// время: до типа `datetime` подстановка усекалась до даты безусловно, и
// `{{now | minus_hours:6}}` менял результат только при переходе через полночь.
func TestОтчёт_ПараметрDatetimeРендеритПолеСоВременем(t *testing.T) {
	s := &Server{reg: runtime.NewRegistry()}
	params := s.buildReportParams(context.Background(), "ru", []reportpkg.Param{
		{Name: "С", Type: "datetime"},
		{Name: "НаДату", Type: "date"},
	}, map[string]any{})
	if len(params) != 2 || !params[0].IsDateTime || params[0].IsDate {
		t.Fatalf("datetime не распознан: %+v", params)
	}
	if !params[1].IsDate || params[1].IsDateTime {
		t.Fatalf("date перестал быть датой: %+v", params[1])
	}

	out := renderReportParamsHTML(t, params, map[string]any{"С": "2026-05-05T12:30:41"})
	if !strings.Contains(out, `type="datetime-local"`) {
		t.Fatalf("нет поля datetime-local: %s", out)
	}
	if !strings.Contains(out, `step="1"`) {
		// Шаг поля по умолчанию — 60 секунд, а {{now}} отдаёт секунды: без
		// step="1" почти всякое умолчание даёт stepMismatch и форма не уходит.
		t.Fatalf("у поля datetime-local нет step=\"1\": %s", out)
	}
	if strings.Contains(out, `name="С" `) && strings.Contains(out, `type="date" name="С"`) {
		t.Fatalf("параметр datetime отрендерен как date: %s", out)
	}
	if !strings.Contains(out, "2026-05-05T12:30:41") {
		t.Fatalf("значение со временем суток не попало в поле: %s", out)
	}
}

func renderReportParamsHTML(t *testing.T, params []reportParamUI, values map[string]any) string {
	t.Helper()
	var buf strings.Builder
	data := map[string]any{
		"Report":       &reportpkg.Report{Name: "R", Title: "R"},
		"ParamValues":  values,
		"ReportParams": params,
		"Cfg":          Config{},
		"Lang":         "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-report", data); err != nil {
		t.Fatalf("execute page-report: %v", err)
	}
	return buf.String()
}

// Сквозная проверка дефекта пользовательским путём: умолчание
// `{{now | minus_hours:6}}` у параметра datetime обязано отсечь события старше
// шести часов. При усечении до даты граница уезжала на полночь, и в выборку
// попадало всё за сутки.
func TestОтчёт_УмолчаниеDatetimeОтсекаетПоВремениСуток(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "report-datetime.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cat := &metadata.Entity{
		Name: "СобытиеОтчёта",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Момент", Type: metadata.FieldTypeDate},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, row := range []map[string]any{
		{"Наименование": "Старое", "Момент": now.Add(-10 * time.Hour)},
		{"Наименование": "Свежее", "Момент": now.Add(-2 * time.Hour)},
	} {
		if err := db.Upsert(ctx, cat.Name, uuid.New(), row, cat); err != nil {
			t.Fatal(err)
		}
	}
	rep := &reportpkg.Report{
		Name:   "СобытияЗаСмену",
		Params: []reportpkg.Param{{Name: "С", Type: "datetime", Default: "{{now | minus_hours:6}}"}},
		Query:  `ВЫБРАТЬ Наименование ИЗ Справочник.СобытиеОтчёта ГДЕ Момент >= &С`,
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{cat}, Reports: []*reportpkg.Report{rep}})
	s := &Server{
		store:    db,
		reg:      registry,
		interp:   interpreter.New(),
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
		ops:      newOperationLimiter(),
	}

	// Параметра в запросе нет — работает умолчание.
	r := reqWithChi("POST", "/ui/report/СобытияЗаСмену", url.Values{}, map[string]string{"name": "СобытияЗаСмену"})
	w := httptest.NewRecorder()
	s.reportRun(w, r)
	if w.Code != 200 {
		t.Fatalf("код %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Свежее") {
		t.Fatalf("событие внутри окна не попало в отчёт: %s", body)
	}
	if strings.Contains(body, "Старое") {
		t.Fatalf("умолчание усечено до даты: событие десятичасовой давности попало в отчёт")
	}
}
