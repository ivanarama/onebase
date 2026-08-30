package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

// Умолчание параметра отчёта должно работать и по REST, иначе API расходится с
// экраном на том самом дефекте, ради которого умолчание заводилось: отчёт с
// необязательной датой приходит ПУСТЫМ, потому что «Срок < NULL» не выбирает
// ничего. Правило то же, что в UI: умолчание подставляется, только когда
// параметра в запросе нет вовсе; явно переданное пустое значение — выбор
// клиента.
func TestAPIV2_ReportParamDefault(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Задача",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Срок", Type: metadata.FieldTypeDate},
		},
	}
	rep := &reportpkg.Report{
		Name:   "ПросроченныеЗадачи",
		Query:  `ВЫБРАТЬ Наименование ИЗ Справочник.Задача ГДЕ Срок < &НаДату УПОРЯДОЧИТЬ ПО Наименование`,
		Params: []reportpkg.Param{{Name: "НаДату", Type: "date", Default: "{{today}}"}},
	}
	h, ctx := newAPITestHandlerWithReports(t, []*metadata.Entity{cat}, []*reportpkg.Report{rep}, nil)

	now := time.Now()
	for name, срок := range map[string]time.Time{
		"Просрочена": now.AddDate(0, 0, -3),
		"Будущая":    now.AddDate(0, 0, 3),
	} {
		if err := h.store.Upsert(ctx, "Задача", uuid.New(), map[string]any{
			"Наименование": name, "Срок": срок,
		}, cat); err != nil {
			t.Fatal(err)
		}
	}

	run := func(query string) []map[string]any {
		t.Helper()
		r := reqWithEntity("GET", "/api/v2/report/ПросроченныеЗадачи"+query, nil,
			map[string]string{"name": "ПросроченныеЗадачи"}, nil)
		w := httptest.NewRecorder()
		h.runReportV2().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("запрос %q: код %d, тело %s", query, w.Code, w.Body.String())
		}
		var resp struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Data
	}

	// Параметр не передан — подставляется умолчание {{today}}, и отчёт выбирает
	// просроченную задачу. Без умолчания «Срок < NULL» вернул бы пусто.
	rows := run("")
	if len(rows) != 1 || rows[0]["наименование"] != "Просрочена" {
		t.Fatalf("без параметра ожидалась одна просроченная задача, получено %#v", rows)
	}

	// Значение клиента важнее умолчания.
	прошлое := now.AddDate(0, 0, -10).Format("2006-01-02")
	if rows := run("?" + url.QueryEscape("НаДату") + "=" + прошлое); len(rows) != 0 {
		t.Fatalf("с датой %s ожидался пустой отчёт, получено %#v", прошлое, rows)
	}

	// Явно переданное пустое значение — тоже выбор клиента: умолчание его не
	// перебивает, отбор снимается (и «Срок < NULL» не выбирает ничего).
	if rows := run("?" + url.QueryEscape("НаДату") + "="); len(rows) != 0 {
		t.Fatalf("с пустым параметром ожидался пустой отчёт, получено %#v", rows)
	}
}
