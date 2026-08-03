package launcher

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// Обработчики конфигуратора принимают исходники модулей и макетов, то есть
// заведомо непустые тела. Пока предела не было, аутентифицированный клиент мог
// заставить лаунчер прочитать в память сколько угодно данных — на один запрос.
//
// Предел объявляется в самом обработчике строкой r.Body = MaxBytesReader(...):
// правильное значение зависит от того, что обработчик принимает, и единого нет.
// За тем, что его не забыли, следит gosec (G120) — он распознаёт только
// присваивание r.Body в теле той же функции.
func TestSaveModuleRejectsOversizedBody(t *testing.T) {
	cfgDir := t.TempDir()
	store := newTestStore(t)
	if err := store.Add(&Base{ID: "b", Name: "b", ConfigSource: "file", Path: cfgDir}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"entity":      {"Контрагент"},
		"module_type": {"module"},
		"source":      {strings.Repeat("к", int(maxFormBody))},
	}
	req := httptest.NewRequest(http.MethodPost, "/bases/b/configurator/module/save",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithBaseID(req, "b")
	rec := httptest.NewRecorder()

	(&handler{store: store, runner: NewRunner()}).configuratorSaveModule(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("код = %d, ожидался 413; тело %q", rec.Code, truncate(rec.Body.String(), 200))
	}
	// И ничего не записалось.
	if entries, err := filepath.Glob(filepath.Join(cfgDir, "src", "*")); err == nil && len(entries) > 0 {
		t.Errorf("при превышении предела не должно быть записи: %v", entries)
	}
}

// Обычный размер по-прежнему проходит — предел не должен ломать штатную работу.
func TestSaveModuleAcceptsNormalBody(t *testing.T) {
	cfgDir := t.TempDir()
	store := newTestStore(t)
	if err := store.Add(&Base{ID: "b", Name: "b", ConfigSource: "file", Path: cfgDir}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"entity":      {"Контрагент"},
		"module_type": {"module"},
		"source":      {"Процедура Тест()\nКонецПроцедуры\n"},
	}
	req := httptest.NewRequest(http.MethodPost, "/bases/b/configurator/module/save",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithBaseID(req, "b")
	rec := httptest.NewRecorder()

	(&handler{store: store, runner: NewRunner()}).configuratorSaveModule(rec, req)

	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("обычная форма отвергнута как слишком большая: %q", truncate(rec.Body.String(), 200))
	}
}
