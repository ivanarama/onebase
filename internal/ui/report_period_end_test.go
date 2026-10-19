package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// «По» с period_end берёт весь день: запись этого дня в 15:00 попадает в
// отчёт, запись следующего дня — нет. Без ключа «По» — полночь, и запись дня
// выпадает (прежнее поведение). Пользовательский путь — POST формы отчёта,
// на обоих диалектах: сравнение даты со временем считает СУБД.
func TestОтчёт_ПараметрPeriodEndБерётВесьДень(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := &metadata.Entity{
			Name: "СобытиеПериода",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Момент", Type: metadata.FieldTypeDate},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
			t.Fatal(err)
		}
		day := time.Date(2026, time.October, 12, 0, 0, 0, 0, time.Local)
		for _, row := range []map[string]any{
			{"Наименование": "ДнёмТогоДня", "Момент": day.Add(15 * time.Hour)},
			{"Наименование": "СледующийДень", "Момент": day.Add(25 * time.Hour)},
		} {
			if err := db.Upsert(ctx, cat.Name, uuid.New(), row, cat); err != nil {
				t.Fatal(err)
			}
		}
		run := func(periodEnd bool) string {
			rep := &reportpkg.Report{
				Name:   "СобытияПоДату",
				Params: []reportpkg.Param{{Name: "По", Type: "date", PeriodEnd: periodEnd}},
				Query:  `ВЫБРАТЬ Наименование ИЗ Справочник.СобытиеПериода ГДЕ Момент <= &По`,
			}
			registry := runtime.NewRegistry()
			registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{cat}, Reports: []*reportpkg.Report{rep}})
			s := &Server{store: db, reg: registry, interp: interpreter.New(), lockMgr: runtime.NewLockManager(),
				messages: NewMessageStore(), ops: newOperationLimiter()}
			form := url.Values{}
			form.Set("По", "2026-10-12")
			w := httptest.NewRecorder()
			s.reportRun(w, reqWithChi("POST", "/ui/report/СобытияПоДату", form, map[string]string{"name": "СобытияПоДату"}))
			if w.Code != 200 {
				t.Fatalf("код %d: %s", w.Code, w.Body.String())
			}
			return w.Body.String()
		}
		body := run(true)
		if !strings.Contains(body, "ДнёмТогоДня") || strings.Contains(body, "СледующийДень") {
			t.Fatalf("period_end: запись дня должна попасть, следующего — нет")
		}
		if body := run(false); strings.Contains(body, "ДнёмТогоДня") {
			t.Fatalf("без period_end «По» — полночь: запись дня в 15:00 не попадает")
		}
	})
}
