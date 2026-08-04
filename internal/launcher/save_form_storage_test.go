package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/storage"
)

// «Сохранить формы» в конфигураторе (configuratorSaveForm) искало YAML сущности
// только на диске, собирая путь из имени объекта. При конфигурации, хранимой в
// БД, файлов на диске нет — каталог workspace пуст, пока пользователь не сделал
// выгрузку, — и кнопка отвечала «Файл сущности не найден» (issue #572).
func TestSaveFormWritesToDatabaseConfig(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "save-form.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := repo.SaveFiles(ctx, []configdb.ConfigFile{
		{Path: "catalogs/сайт_атс.yaml", Content: []byte("name: сайт_атс\nfields:\n  - {name: Наименование, type: string}\n  - {name: ТипГТП, type: string}\n")},
	}, configdb.VersionOptions{Message: "seed"}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	t.Cleanup(CloseAuthPools)

	store := newTestStore(t)
	if err := store.Add(&Base{ID: "b", Name: "b", ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"entity":    {"сайт_атс"},
		"lf.0.name": {"Наименование"},
		"lf.0.vis":  {"1"},
		"lf.1.name": {"ТипГТП"},
		"lf.1.vis":  {""}, // колонка снята — в list_form попасть не должна
	}
	req := httptest.NewRequest(http.MethodPost, "/bases/b/configurator/form",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithBaseID(req, "b")
	rec := httptest.NewRecorder()

	(&handler{store: store, runner: NewRunner()}).configuratorSaveForm(rec, req)

	if strings.Contains(rec.Body.String(), "Файл сущности не найден") {
		t.Fatalf("сохранение состава форм в БД-конфигурации отклонено с «Файл сущности не найден»")
	}

	db2, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	raw, ok, err := configdb.New(db2).ReadFile(ctx, "catalogs/сайт_атс.yaml")
	if err != nil || !ok {
		t.Fatalf("YAML сущности пропал из конфигурации: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(string(raw), "list_form") || !strings.Contains(string(raw), "Наименование") {
		t.Fatalf("list_form не записан в конфигурацию БД:\n%s", raw)
	}
	if strings.Contains(string(raw), "ТипГТП\n- ") || strings.Contains(string(raw), "- ТипГТП") {
		t.Fatalf("в list_form попала снятая колонка:\n%s", raw)
	}
}

// Имя файла сущности не обязано совпадать с именем объекта: после импорта из 1С
// или ручного переименования файла в каталоге catalogs/ лежит, например,
// site_ats.yaml с «name: сайт_атс». Прежний код собирал путь из имени объекта и
// такую сущность не находил (issue #572).
func TestSaveFormFindsEntityByNameInsideYAML(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cfgDir, "catalogs", "site_ats.yaml")
	if err := os.WriteFile(target, []byte("name: сайт_атс\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := newTestStore(t)
	if err := store.Add(&Base{ID: "b", Name: "b", ConfigSource: "file", Path: cfgDir}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"entity":    {"сайт_атс"},
		"lf.0.name": {"Наименование"},
		"lf.0.vis":  {"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/bases/b/configurator/form",
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
		t.Fatalf("состав формы не сохранён в файл с непохожим именем:\n%s", got)
	}
}
