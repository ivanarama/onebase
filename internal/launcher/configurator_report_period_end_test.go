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
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/report"
	"gopkg.in/yaml.v3"
)

// period_end не редактируется формой: GET → POST должен сохранить его по имени.
func TestConfiguratorSaveReport_PeriodEndRoundTrip(t *testing.T) {
	const original = `name: Продажи
title: Продажи за период
params:
  - {name: По, type: date, period_end: true}
  - {name: С, type: date, period_end: false}
  - {name: БезФлага, type: date}
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

				if form.Get("param.0.name") != "По" || form.Get("param.0.type") != "date" {
					t.Fatalf("не получены controls параметра По: %v", form)
				}
				form.Set("title", "Новый заголовок")
				yes, no := true, false
				want := map[string]*bool{"По": &yes, "С": &no, "БезФлага": nil}
				wantNames := []string{"По", "С", "БезФлага"}
				switch action {
				case "reorder":
					form.Set("param.0.name", "С")
					form.Set("param.1.name", "По")
					wantNames[0], wantNames[1] = wantNames[1], wantNames[0]
				case "rename-delete-add":
					form.Set("param.0.name", "НоваяДата")
					form.Set("param.1.name", "")
					form.Set("param.9.name", "Новый")
					form.Set("param.9.type", "date")
					want = map[string]*bool{"НоваяДата": nil, "БезФлага": nil, "Новый": nil}
					wantNames = []string{"НоваяДата", "БезФлага", "Новый"}
				case "delete-all":
					for i := 0; i < 3; i++ {
						form.Set(fmt.Sprintf("param.%d.name", i), "")
					}
					want = map[string]*bool{}
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
				raw := readReport()
				after, err := report.ParseBytes(raw)
				if err != nil {
					t.Fatal(err)
				}
				if after.Title != "Новый заголовок" || len(after.Params) != len(want) {
					t.Fatalf("сохранённый отчёт: %+v", after)
				}
				var fields struct {
					Params []struct {
						Name      string
						PeriodEnd *bool `yaml:"period_end"`
					}
				}
				if err := yaml.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				var names []string
				for _, p := range fields.Params {
					names = append(names, p.Name)
					expected, exists := want[p.Name]
					if !exists || !reflect.DeepEqual(p.PeriodEnd, expected) {
						t.Errorf("параметр %s: period_end=%v, нужен %v (exists=%v)", p.Name, p.PeriodEnd, expected, exists)
					}
				}
				if !reflect.DeepEqual(names, wantNames) {
					t.Errorf("порядок/имена %q, нужны %q", names, wantNames)
				}
				// Проверяем эффект сохранённого флага через общий публичный парсер формы/API.
				for _, p := range after.Params {
					expected, exists := want[p.Name]
					if !exists || p.Type != "date" {
						t.Fatalf("неожиданный параметр: %+v", p)
					}
					hour, minute, second := 0, 0, 0
					if expected != nil && *expected {
						hour, minute, second = 23, 59, 59
					}
					for _, mode := range []report.ParamParseMode{report.ParamParseForm, report.ParamParseAPI} {
						value, err := report.ParseParamValue("2026-10-12", p, mode)
						if err != nil {
							t.Fatal(err)
						}
						wantDate := time.Date(2026, time.October, 12, hour, minute, second, 0, time.Local)
						date, ok := value.(time.Time)
						if !ok || !date.Equal(wantDate) {
							t.Errorf("параметр %s, mode=%v: %v, нужно %v", p.Name, mode, value, wantDate)
						}
					}
				}
			})
		}
	}
}
