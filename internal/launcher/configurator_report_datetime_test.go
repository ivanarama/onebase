package launcher

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/report"
)

// Значение типа берём из настоящей формы: POST с вручную заданным datetime
// не обнаружил бы отсутствие option и отправку браузером первого типа string.
func TestConfiguratorSaveReport_DatetimeRoundTrip(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "reports", "продажи.yaml", `name: Продажи
title: Продажи за период
params:
  - {name: СМомента, type: datetime, label: "С момента"}
  - {name: Дата, type: date, label: "Дата"}
query: ВЫБРАТЬ 1 КАК Один
`)
	router := chi.NewRouter()
	router.Get("/bases/{id}/configurator", h.configuratorPage)
	router.Post("/bases/{id}/configurator/report", h.configuratorSaveReport)
	readForm := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bases/test/configurator?tab=tree", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	form := browserSubmit(t, readForm(), "/configurator/report")
	// Пользователь меняет только заголовок, не касаясь типов параметров.
	form.Set("title", "Реализации за период")
	req := httptest.NewRequest(http.MethodPost, "/bases/test/configurator/report", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Onebase-Ajax", "1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	after, err := report.LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "Реализации за период" || len(after.Params) != 2 {
		t.Fatalf("изменение заголовка или параметры не сохранились: %+v", after)
	}
	for i, want := range []string{"datetime", "date"} {
		if got := after.Params[i].Type; got != want {
			t.Errorf("тип параметра %s после сохранения = %q, нужен %q", after.Params[i].Name, got, want)
		}
	}
	reopened := browserSubmit(t, readForm(), "/configurator/report")
	if got := reopened.Get("param.0.type"); got != "datetime" {
		t.Errorf("повторное открытие выбрало %q вместо datetime", got)
	}
}

func TestConfiguratorAddReportParam_OffersDatetime(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the report parameter UI test")
	}
	// Исполняем обработчик кнопки «+», проверяя созданный им список типов.
	const script = `
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const source = fs.readFileSync('static/configurator.js', 'utf8');
const start = source.indexOf('function repAddParam(');
const end = source.indexOf('// ── Конструктор компоновки', start);
assert.ok(start >= 0 && end > start);
let added, focused = false;
const table = {
  querySelectorAll: () => [],
  appendChild: row => { added = row; }
};
const context = {
  T: text => text,
  window: {__cfg: {entityNames: ['Покупатель']}},
  document: {
    getElementById: () => table,
    createElement: () => ({querySelector: () => ({focus: () => { focused = true; }})})
  }
};
vm.createContext(context);
vm.runInContext(source.slice(start, end), context);
context.repAddParam('params-Продажи');
assert.ok(added && focused);
process.stdout.write(added.innerHTML);
`
	cmd := exec.Command(node, "-e", script) //nolint:gosec // test-only executable resolved by exec.LookPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("repAddParam: %v\n%s", err, out)
	}
	for _, typ := range []string{"datetime", "date", "string", "number", "select", "reference:Покупатель"} {
		if !strings.Contains(string(out), `<option value="`+typ+`">`) {
			t.Errorf("новая строка параметра не предлагает тип %q: %s", typ, out)
		}
	}
}
