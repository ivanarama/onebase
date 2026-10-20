package launcher

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

// Форма полей не редактирует recorder: оба места хранения должны сохранить
// подчинённость регистратору и при повторном сохранении, и при правке полей.
func TestConfiguratorSaveInfoRegFields_RecorderRoundTrip(t *testing.T) {
	cases := []struct {
		name     string
		recorder string
		want     bool
		periodic bool
	}{
		{"subordinate-periodic", "recorder: true\n", true, true},
		{"subordinate-nonperiodic", "recorder: true\n", true, false},
		{"independent-explicit", "recorder: false\n", false, true},
		{"independent-default", "", false, false},
	}
	for _, source := range []string{"file", "database"} {
		for _, tc := range cases {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				periodic := ""
				if tc.periodic {
					periodic = "periodic: true\n"
				}
				original := `name: Цены
title: Цены товаров
titles: {en: Prices}
` + tc.recorder + periodic + `dimensions:
  - id: f_product
    name: Товар
    type: string
    title: Товар
    titles: {en: Product}
resources:
  - id: f_price
    name: Цена
    type: number
    titles: {en: Price}
indexes:
  - fields: [Товар]
`
				ctx := context.Background()
				var h *handler
				var readRegister func() []byte
				if source == "file" {
					var dir string
					h, dir = newFileBaseHandler(t)
					h.runner = NewRunner()
					writeCfgFileRv(t, dir, "config", "app.yaml", "name: Тест\n")
					p := writeCfgFileRv(t, dir, "inforegs", "цены.yaml", original)
					readRegister = func() []byte {
						t.Helper()
						raw, err := os.ReadFile(p)
						if err != nil {
							t.Fatal(err)
						}
						return raw
					}
				} else {
					h = dbBackedBase(t, "test")
					base, err := h.store.Get("test")
					if err != nil {
						t.Fatal(err)
					}
					db, err := OpenDB(ctx, base)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { db.Close() })
					repo := configdb.New(db)
					if err := repo.SaveFiles(ctx, []configdb.ConfigFile{
						{Path: "config/app.yaml", Content: []byte("name: Тест\n")},
						{Path: "inforegs/цены.yaml", Content: []byte(original)},
					}, configdb.VersionOptions{Message: "seed register"}); err != nil {
						t.Fatal(err)
					}
					readRegister = func() []byte {
						t.Helper()
						raw, found, err := repo.ReadFile(ctx, "inforegs/цены.yaml")
						if err != nil || !found {
							t.Fatalf("ReadFile: found=%v err=%v", found, err)
						}
						return raw
					}
				}

				var before map[string]any
				if err := yaml.Unmarshal([]byte(original), &before); err != nil {
					t.Fatal(err)
				}
				form := url.Values{
					"inforeg":         {"Цены"},
					"periodic":        {fmt.Sprint(tc.periodic)},
					"dim.0.name":      {"Товар"},
					"dim.0.type":      {"string"},
					"dim.0.titles.en": {"Product"},
					"res.0.name":      {"Цена"},
					"res.0.type":      {"number"},
					"res.0.titles.en": {"Price"},
				}
				for _, action := range []string{"unchanged", "edit"} {
					if action == "edit" {
						form.Set("titles.en", "Product prices")
						form.Set("res.1.name", "Количество")
						form.Set("res.1.type", "number")
					}
					rec := postCfgRv(t, "test", "/bases/test/configurator/inforeg-fields", form, h.configuratorSaveInfoRegFields)
					if ok, errText := cfgResponse(t, rec); !ok {
						t.Fatalf("%s POST: %s", action, errText)
					}
					raw := readRegister()
					var after map[string]any
					if err := yaml.Unmarshal(raw, &after); err != nil {
						t.Fatal(err)
					}
					if got, _ := after["recorder"].(bool); got != tc.want {
						t.Errorf("%s recorder=%v, want %v; YAML:\n%s", action, got, tc.want, raw)
					}
					for _, key := range []string{"name", "title", "dimensions", "indexes"} {
						if !reflect.DeepEqual(after[key], before[key]) {
							t.Errorf("%s lost %s: got %#v, want %#v", action, key, after[key], before[key])
						}
					}
					wantTitles := before["titles"]
					if action == "edit" {
						wantTitles = map[string]any{"en": "Product prices"}
					}
					if !reflect.DeepEqual(after["titles"], wantTitles) {
						t.Errorf("%s titles=%#v, want %#v", action, after["titles"], wantTitles)
					}
					resources := after["resources"].([]any)
					if !reflect.DeepEqual(resources[0], before["resources"].([]any)[0]) {
						t.Errorf("%s lost existing resource: %#v", action, resources[0])
					}
					if action == "edit" && (len(resources) != 2 || resources[1].(map[string]any)["name"] != "Количество") {
						t.Errorf("new resource not saved: %#v", resources)
					}
					// Проверяем тот же признак после публичной загрузки метаданных.
					p := filepath.Join(t.TempDir(), "цены.yaml")
					if err := os.WriteFile(p, raw, 0o600); err != nil {
						t.Fatal(err)
					}
					reg, err := metadata.LoadInfoRegisterFile(p)
					if err != nil {
						t.Fatal(err)
					}
					if reg.Recorder != tc.want || reg.Periodic != tc.periodic {
						t.Errorf("%s loaded recorder=%v periodic=%v, want %v/%v", action, reg.Recorder, reg.Periodic, tc.want, tc.periodic)
					}
				}
			})
		}
	}
}

// Новый поддерживаемый ключ не должен молча исчезать при сохранении полей.
func TestSaveInfoReg_CoversAllRawKeys(t *testing.T) {
	raw := yamlTagsOfStruct(t, "../metadata/yaml.go", "rawInfoRegister")
	save := yamlTagsOfStruct(t, "configurator_types.go", "saveInfoReg")
	if !reflect.DeepEqual(raw, save) {
		t.Fatalf("saveInfoReg YAML keys %v differ from rawInfoRegister %v", save, raw)
	}
}
