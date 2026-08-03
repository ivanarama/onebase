package launcher

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configuratorSaveForm читает YAML сущности, дописывает в него list_form/
// item_form и записывает обратно. Имя сущности приходит из формы, а
// nameToFilename только приводит его к нижнему регистру — разделители пути он
// не вычищает. filepath.Join при этом схлопывает «..», поэтому «entity=../../X»
// уводило и чтение, и ЗАПИСЬ за пределы каталога проекта: из подкаталога
// catalogs/ два уровня вверх — это уже каталог рядом с проектом.
//
// Соседние обработчики конфигуратора (модуль, обработка, отчёт, макет) проверяют
// имя через validObjectName; в этом его не было. Тест держит границу: файл рядом
// с каталогом проекта не должен ни прочитаться, ни измениться.
func TestSaveFormRejectsTraversingEntityName(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "проект")
	if err := os.MkdirAll(filepath.Join(cfgDir, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Цель обхода: валидный YAML СНАРУЖИ каталога проекта.
	outside := filepath.Join(root, "снаружи.yaml")
	const original = "name: Снаружи\n"
	if err := os.WriteFile(outside, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	store := newTestStore(t)
	if err := store.Add(&Base{ID: "b", Name: "b", ConfigSource: "file", Path: cfgDir}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"entity":    {"../../снаружи"},
		"lf.0.name": {"Наименование"},
		"lf.0.vis":  {"1"},
		"ef.0.name": {"Наименование"},
		"ef.0.vis":  {"1"},
		"list_form": {"1"},
		"item_form": {"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/bases/b/configurator/form/save",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithBaseID(req, "b")
	rec := httptest.NewRecorder()

	(&handler{store: store, runner: NewRunner()}).configuratorSaveForm(rec, req)

	got, err := os.ReadFile(outside) //nolint:gosec // G304: путь собран здесь же, в тесте
	if err != nil {
		t.Fatalf("файл снаружи проекта исчез: %v", err)
	}
	if string(got) != original {
		t.Fatalf("файл снаружи каталога проекта перезаписан:\n%s", got)
	}
}

// Обычное имя по-прежнему сохраняется — проверка не должна ломать штатный путь.
func TestSaveFormAcceptsNormalEntityName(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cfgDir, "catalogs", "контрагент.yaml")
	if err := os.WriteFile(target, []byte("name: Контрагент\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := newTestStore(t)
	if err := store.Add(&Base{ID: "b", Name: "b", ConfigSource: "file", Path: cfgDir}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"entity":    {"Контрагент"},
		"lf.0.name": {"Наименование"},
		"lf.0.vis":  {"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/bases/b/configurator/form/save",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithBaseID(req, "b")
	rec := httptest.NewRecorder()

	(&handler{store: store, runner: NewRunner()}).configuratorSaveForm(rec, req)

	got, err := os.ReadFile(target) //nolint:gosec // G304: путь собран здесь же, в тесте
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "list_form") {
		t.Errorf("штатное сохранение не сработало:\n%s", got)
	}
}
