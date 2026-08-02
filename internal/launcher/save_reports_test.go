package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/storage"
)

// dbBackedBase поднимает базу с конфигурацией в БД (таблица _onebase_config
// создана, но пустая) и возвращает готовый handler.
func dbBackedBase(t *testing.T, id string) *handler {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), id+".db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := configdb.New(db).EnsureSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	t.Cleanup(CloseAuthPools)

	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := store.save([]*Base{{
		ID: id, Name: id, ConfigSource: "database", DBType: "sqlite", DBPath: dbPath,
	}}); err != nil {
		t.Fatal(err)
	}
	return &handler{store: store, runner: NewRunner()}
}

func postConfigForm(t *testing.T, id string, form url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/bases/"+id+"/configurator/save",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return requestWithBaseID(req, id)
}

// Сохранение константы ищет файл конфигурации, в котором она объявлена. Когда
// поиск ничего не находил, saveErr оставался nil — и пользователь получал
// отметку «сохранено», хотя не было записано ничего. Тот же путь давал ту же
// отметку при сбое Query и при сбойном Scan, пропустившем файл-кандидат.
func TestSaveConstantReportsWhenNothingWasWritten(t *testing.T) {
	h := dbBackedBase(t, "constsave")

	req := postConfigForm(t, "constsave", url.Values{
		"const_name": {"СтавкаНДС"},
		"label":      {"Ставка НДС"},
		"type":       {"number"},
	})
	rec := httptest.NewRecorder()

	h.configuratorSaveConstant(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "не найдена в конфигурации") {
		t.Fatalf("сохранение без записи должно сообщать об ошибке; тело: %s", truncate(body, 600))
	}
}

// То же для отчёта: путь поиска общий по форме, и «сохранено» без записи
// возникало там по тем же трём причинам.
func TestSaveReportReportsWhenNothingWasWritten(t *testing.T) {
	h := dbBackedBase(t, "repsave")

	req := postConfigForm(t, "repsave", url.Values{
		"report_name": {"ОСВ"},
		"title":       {"Оборотно-сальдовая"},
		"query":       {"ВЫБРАТЬ 1"},
	})
	rec := httptest.NewRecorder()

	h.configuratorSaveReport(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "не найден в конфигурации") {
		t.Fatalf("сохранение без записи должно сообщать об ошибке; тело: %s", truncate(body, 600))
	}
}
