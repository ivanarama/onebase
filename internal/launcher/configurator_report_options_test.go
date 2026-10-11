package launcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"gopkg.in/yaml.v3"
)

// Options не редактируются в форме: HTTP round-trip должен сохранить их по имени.
func TestConfiguratorSaveReport_OptionsRoundTrip(t *testing.T) {
	const original = `name: Продажи
title: Продажи за период
params:
  - {name: Статус, type: select, options: [Открыт, Закрыт], default: Закрыт}
  - {name: Категория, type: select, options: ["", " A ", "A: B", "Ёж", "Ёж"]}
  - {name: Пустой, type: select, options: []}
  - {name: БезВариантов, type: select}
  - {name: Удаляемый, type: select, options: [Удалён]}
query: ВЫБРАТЬ 1 КАК Один
`
	for _, source := range []string{"file", "database"} {
		for _, action := range []string{"title", "reorder", "rename-delete-add", "delete-all"} {
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
				if form.Get("param.0.name") != "Статус" || form.Get("param.0.type") != "select" {
					t.Fatalf("не получены controls параметра select: %v", form)
				}
				form.Set("title", "Новый заголовок")
				want := map[string][]string{
					"Статус":       {"Открыт", "Закрыт"},
					"Категория":    {"", " A ", "A: B", "Ёж", "Ёж"},
					"Пустой":       {},
					"БезВариантов": nil,
					"Удаляемый":    {"Удалён"},
				}
				wantNames := []string{"Статус", "Категория", "Пустой", "БезВариантов", "Удаляемый"}
				switch action {
				case "reorder":
					form.Set("param.0.name", "Категория")
					form.Set("param.1.name", "Статус")
					wantNames[0], wantNames[1] = wantNames[1], wantNames[0]
				case "rename-delete-add":
					form.Set("param.0.name", "НовыйСтатус")
					form.Set("param.4.name", "")
					// Новая строка с разреженным индексом, как после удаления в браузере.
					form.Set("param.9.name", "Новый")
					form.Set("param.9.type", "select")
					delete(want, "Статус")
					delete(want, "Удаляемый")
					want["НовыйСтатус"] = nil
					want["Новый"] = nil
					wantNames = []string{"НовыйСтатус", "Категория", "Пустой", "БезВариантов", "Новый"}
				case "delete-all":
					for i := 0; i < 5; i++ {
						form.Set(fmt.Sprintf("param.%d.name", i), "")
					}
					want = map[string][]string{}
					wantNames = nil
				}
				req := httptest.NewRequest(http.MethodPost, "/bases/test/configurator/report", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("X-Onebase-Ajax", "1")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if ok, errText := cfgResponse(t, rec); !ok {
					t.Fatalf("POST: %s", errText)
				}
				var after struct {
					Title  string
					Params []struct {
						Name    string
						Type    string
						Options *[]string
						Default *string
					}
				}
				if err := yaml.Unmarshal(readReport(), &after); err != nil {
					t.Fatal(err)
				}
				if after.Title != "Новый заголовок" || len(after.Params) != len(want) {
					t.Fatalf("сохранённый отчёт: %+v", after)
				}
				var names []string
				for _, p := range after.Params {
					names = append(names, p.Name)
					expected, exists := want[p.Name]
					if !exists {
						t.Fatalf("неожиданный параметр %s", p.Name)
					}
					if p.Type != "select" {
						t.Errorf("параметр %s: type=%q", p.Name, p.Type)
					}
					if expected == nil {
						if p.Options != nil {
							t.Errorf("параметр %s получил чужие/отсутствовавшие options: %v", p.Name, *p.Options)
						}
					} else if p.Options == nil || !reflect.DeepEqual(*p.Options, expected) {
						t.Errorf("параметр %s: options=%v, нужны %q", p.Name, p.Options, expected)
					}
					if p.Name == "Статус" {
						if p.Default == nil || *p.Default != "Закрыт" {
							t.Error("потеряно default параметра Статус")
						}
					} else if p.Default != nil {
						t.Errorf("параметр %s получил чужой default", p.Name)
					}
				}
				if !reflect.DeepEqual(names, wantNames) {
					t.Errorf("порядок/имена параметров %q, нужны %q", names, wantNames)
				}
			})
		}
	}
}
