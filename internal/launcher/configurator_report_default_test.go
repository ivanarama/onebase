package launcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/report"
	"gopkg.in/yaml.v3"
)

// Форму получаем через GET и отправляем через HTTP: default в ней не редактируется.
func TestConfiguratorSaveReport_DefaultRoundTrip(t *testing.T) {
	const original = `name: Продажи
title: Продажи за период
params:
  - {name: Дата, type: date, default: "{{today}}"}
  - {name: Количество, type: number, default: 0}
  - {name: Флаг, type: bool, default: false}
  - {name: Текст, type: string, default: "Умолчание"}
  - {name: Пустой, type: string, default: ""}
query: ВЫБРАТЬ 1 КАК Один
`
	for _, source := range []string{"file", "database"} {
		for _, action := range []string{"title", "reorder", "rename-delete", "delete-all"} {
			t.Run(source+"/"+action, func(t *testing.T) {
				h, cfgDir := newFileBaseHandler(t)
				h.runner = NewRunner()
				var readReport func() []byte
				if source == "file" {
					p := writeCfgFile(t, cfgDir, "reports", "продажи.yaml", original)
					readReport = func() []byte {
						t.Helper()
						raw, err := os.ReadFile(p)
						if err != nil {
							t.Fatal(err)
						}
						return raw
					}
				} else {
					base, err := h.store.Get("test")
					if err != nil {
						t.Fatal(err)
					}
					base.ConfigSource = "database"
					base.DBType = "sqlite"
					base.DBPath = filepath.Join(t.TempDir(), "config.db")
					if err := h.store.Update(base); err != nil {
						t.Fatal(err)
					}
					db, err := OpenDB(context.Background(), base)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { db.Close() })
					repo := configdb.New(db)
					if err := repo.EnsureSchema(context.Background()); err != nil {
						t.Fatal(err)
					}
					if err := repo.SaveFiles(context.Background(), []configdb.ConfigFile{
						{Path: "config/app.yaml", Content: []byte("name: Тест\n")},
						{Path: "reports/продажи.yaml", Content: []byte(original)},
					}, configdb.VersionOptions{Message: "seed report"}); err != nil {
						t.Fatal(err)
					}
					readReport = func() []byte {
						t.Helper()
						raw, found, err := repo.ReadFile(context.Background(), "reports/продажи.yaml")
						if err != nil || !found {
							t.Fatalf("ReadFile: found=%v err=%v", found, err)
						}
						return raw
					}
				}
				router := chi.NewRouter()
				router.Get("/bases/{id}/configurator", h.configuratorPage)
				router.Post("/bases/{id}/configurator/report", h.configuratorSaveReport)
				get := httptest.NewRecorder()
				router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/bases/test/configurator?tab=tree", nil))
				if get.Code != http.StatusOK {
					t.Fatalf("GET: %d %s", get.Code, get.Body.String())
				}
				form := browserSubmit(t, get.Body.String(), "/configurator/report")
				form.Set("title", "Новый заголовок")
				want := map[string]string{"Дата": "{{today}}", "Количество": "0", "Флаг": "false", "Текст": "Умолчание", "Пустой": ""}
				switch action {
				case "reorder":
					form.Set("param.0.name", "Текст")
					form.Set("param.0.type", "string")
					form.Set("param.3.name", "Дата")
					form.Set("param.3.type", "date")
				case "rename-delete":
					form.Set("param.0.name", "НоваяДата")
					form.Set("param.1.name", "")
					delete(want, "Дата")
					delete(want, "Количество")
					want["НоваяДата"] = ""
				case "delete-all":
					for i := 0; i < 5; i++ {
						form.Set(fmt.Sprintf("param.%d.name", i), "")
					}
					want = map[string]string{}
				}
				req := httptest.NewRequest(http.MethodPost, "/bases/test/configurator/report", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("X-Onebase-Ajax", "1")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if ok, errText := cfgResponse(t, rec); !ok {
					t.Fatalf("POST: %s", errText)
				}
				raw := readReport()
				var after report.Report
				if err := yaml.Unmarshal(raw, &after); err != nil {
					t.Fatal(err)
				}
				if after.Title != "Новый заголовок" || len(after.Params) != len(want) {
					t.Fatalf("сохранённый отчёт: %+v", after)
				}
				for _, p := range after.Params {
					expected, exists := want[p.Name]
					if !exists || p.Default != expected {
						t.Errorf("параметр %s: default=%q, нужен %q (exists=%v)", p.Name, p.Default, expected, exists)
					}
					delete(want, p.Name)
				}
				if len(want) != 0 {
					t.Errorf("потерянные параметры: %v", want)
				}
				var fields struct {
					Params []struct {
						Name    string
						Default *string
					}
				}
				if err := yaml.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				for _, p := range fields.Params {
					if p.Name == "Пустой" && (p.Default == nil || *p.Default != "") {
						t.Error("потеряно явно пустое default")
					}
					if p.Name == "НоваяДата" && p.Default != nil {
						t.Error("default перенесён при переименовании")
					}
				}
			})
		}
	}
}
