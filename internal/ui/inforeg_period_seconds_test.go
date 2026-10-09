package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Период записи регистра сведений — момент с точностью до секунды, как дата
// документа и как в 1С («12.08.2024 15:33:22»). Форма новой записи рисовала
// поле только даты, а список выводил период без времени: записи одного дня
// были неразличимы, а время, заданное кодом, на экране пропадало.
func TestInfoRegPeriodKeepsSeconds(t *testing.T) {
	ir := &metadata.InfoRegister{
		Name:       "ЦеныСоВременем",
		Periodic:   true,
		Dimensions: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
	}
	s, ctx := newSubmitTestServer(t, nil)
	if err := s.store.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
	router := chi.NewRouter()
	s.Mount(router)
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)
	formURL := ts.URL + "/ui/inforeg/" + url.PathEscape(ir.Name) + "/new"

	resp, err := http.Get(formURL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	input := regexp.MustCompile(`<input[^>]*name="period"[^>]*>`).FindString(string(page))
	if !strings.Contains(input, `type="datetime-local"`) || !strings.Contains(input, `step="1"`) {
		t.Fatalf("поле периода не дата со временем и секундами: %s", input)
	}
	if !regexp.MustCompile(`value="\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d"`).MatchString(input) {
		t.Fatalf("период новой записи по умолчанию — без секунд: %s", input)
	}

	for _, rec := range []struct{ product, period string }{
		{"С временем", "2026-08-12T15:33:22"},
		{"В полночь", "2026-08-13T00:00:00"},
	} {
		code, body := postForm(t, formURL, url.Values{"period": {rec.period}, "Товар": {rec.product}, "Цена": {"10"}})
		if code != http.StatusFound {
			t.Fatalf("%s: статус = %d, тело: %s", rec.product, code, body)
		}
	}
	rows, err := s.store.InfoRegList(ctx, ir, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, row := range rows {
		got[row["Товар"].(string)] = row["period"]
	}
	if got["С временем"] != "12.08.2026 15:33:22" {
		t.Errorf("период с временем выведен как %v, ждали 12.08.2026 15:33:22", got["С временем"])
	}
	if got["В полночь"] != "13.08.2026" {
		t.Errorf("полночь выведена как %v, ждали 13.08.2026", got["В полночь"])
	}
}
