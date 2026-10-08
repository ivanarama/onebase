package launcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

// Submit the rendered numbering editor: ru and languages absent from the
// language list have no controls, while clearing a visible control is an edit.
func TestSaveFields_StandardTitlesPreserveUnsubmittedLanguages(t *testing.T) {
	for _, storageMode := range []string{"file", "database"} {
		for _, kind := range []struct {
			dir, entity, field, id string
			kind                   metadata.Kind
		}{
			{"catalogs", "Клиенты", "Код", metadata.StandardCodeFieldID, metadata.KindCatalog},
			{"documents", "Приказ", "Номер", metadata.StandardNumberFieldID, metadata.KindDocument},
		} {
			for _, source := range []string{"numbered", "legacy", "first-numbering"} {
				for _, action := range []string{"unchanged", "edit", "clear"} {
					t.Run(storageMode+"/"+kind.dir+"/"+source+"/"+action, func(t *testing.T) {
						ctx := context.Background()
						properties := "    title: Основная подпись\n    titles: {ru: Русское имя, en: English name, zz: Hidden translation}\n    required: true\n    default: PREFIX\n    pii: true\n"
						raw := "name: " + kind.entity + "\n"
						if source == "numbered" {
							raw += "numerator:\n  length: 8\n  field:\n" + properties
						} else {
							if source == "legacy" {
								raw += "numerator: {length: 8}\n"
							}
							raw += fmt.Sprintf("fields:\n  - id: f_old\n    name: %s\n    type: string\n", kind.field) + properties
						}
						var h *handler
						var readSaved func() []byte
						if storageMode == "file" {
							var cfgDir string
							h, cfgDir = newFileBaseHandler(t)
							path := writeCfgFile(t, cfgDir, kind.dir, kind.entity+".yaml", raw)
							readSaved = func() []byte {
								content, err := os.ReadFile(path)
								if err != nil {
									t.Fatal(err)
								}
								return content
							}
						} else {
							h = dbBackedBase(t, "test")
							b, err := h.store.Get("test")
							if err != nil {
								t.Fatal(err)
							}
							db, err := OpenDB(ctx, b)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { db.Close() })
							repo := configdb.New(db)
							path := filepath.ToSlash(filepath.Join(kind.dir, kind.entity+".yaml"))
							if err := repo.SaveFiles(ctx, []configdb.ConfigFile{
								{Path: "config/app.yaml", Content: []byte("name: Test\n")},
								{Path: path, Content: []byte(raw)},
							}, configdb.VersionOptions{Message: "seed"}); err != nil {
								t.Fatal(err)
							}
							readSaved = func() []byte {
								content, found, err := repo.ReadFile(ctx, path)
								if err != nil || !found {
									t.Fatalf("ReadFile: found=%v err=%v", found, err)
								}
								return content
							}
						}
						h.runner = NewRunner()
						b, err := h.store.Get("test")
						if err != nil {
							t.Fatal(err)
						}
						wantTitles := map[string]string{"ru": "Русское имя", "en": "English name", "zz": "Hidden translation"}
						for round := 0; round < 2; round++ {
							data := h.loadCfgData(ctx, b, "tree")
							if data.Error != "" {
								t.Fatal(data.Error)
							}
							data.AvailableLangs = []i18n.Lang{{Code: "ru", Native: "Русский"}, {Code: "en", Native: "English"}}
							form := browserSubmitForEntity(t, renderCfgTree(t, data), kind.entity)
							if form.Get("numerator_field_titles_present") != "1" {
								t.Fatal("numbering translations editor missing")
							}
							for _, lang := range []string{"ru", "zz"} {
								if _, exists := form["numerator_field_titles."+lang]; exists {
									t.Fatalf("hidden language %s unexpectedly has a control", lang)
								}
							}
							if _, exists := form["numerator_field_titles.en"]; !exists {
								t.Fatal("visible English control missing")
							}
							form.Set("numerator_enabled", "1")
							switch action {
							case "edit":
								form.Set("numerator_field_titles.en", "Edited name")
								wantTitles["en"] = "Edited name"
							case "clear":
								form.Set("numerator_field_titles.en", "  ")
								delete(wantTitles, "en")
							}
							if ok, errText := cfgResponse(t, postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)); !ok {
								t.Fatal(errText)
							}
							var saved saveEntity
							content := readSaved()
							if err := yaml.Unmarshal(content, &saved); err != nil {
								t.Fatal(err)
							}
							if saved.Numerator == nil || saved.Numerator.Field == nil {
								t.Fatalf("standard properties lost:\n%s", content)
							}
							f := saved.Numerator.Field
							if !reflect.DeepEqual(f.Titles, wantTitles) || f.Title != "Основная подпись" || !f.Required || f.Default != "PREFIX" || !f.PII {
								t.Fatalf("round %d properties changed: %+v; want titles %v", round, f, wantTitles)
							}
							loadedPath := filepath.Join(t.TempDir(), "entity.yaml")
							if err := os.WriteFile(loadedPath, content, 0600); err != nil {
								t.Fatal(err)
							}
							ent, err := metadata.LoadFile(loadedPath, kind.kind)
							if err != nil {
								t.Fatal(err)
							}
							found := false
							for _, field := range ent.Fields {
								if strings.EqualFold(field.Name, kind.field) {
									found = true
									if field.ID != kind.id || field.DisplayName("ru") != "Русское имя" {
										t.Fatalf("loaded standard field changed: %+v", field)
									}
								}
							}
							if !found {
								t.Fatal("standard field missing after reload")
							}
						}
					})
				}
			}
		}
	}
}
