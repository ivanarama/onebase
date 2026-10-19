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
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

// Exercise the same rendered form and HTTP save handler as a user disabling
// numbering, then save again and re-enable it without losing hidden properties.
func TestSaveFields_DisablingNumberingPreservesSubmittedProperties(t *testing.T) {
	for _, mode := range []string{"file", "database"} {
		for _, kind := range []struct {
			dir, entity, field, id string
			kind                   metadata.Kind
		}{
			{"catalogs", "Клиенты", "Код", metadata.StandardCodeFieldID, metadata.KindCatalog},
			{"documents", "Приказ", "Номер", metadata.StandardNumberFieldID, metadata.KindDocument},
		} {
			for _, spelling := range []string{"title", "label"} {
				for _, action := range []string{"unchanged", "edit", "clear-translation", "clear-title", "missing-title", "missing-editor"} {
					t.Run(mode+"/"+kind.dir+"/"+spelling+"/"+action, func(t *testing.T) {
						ctx := context.Background()
						raw := fmt.Sprintf("name: %s\nnumerator:\n  length: 8\n  field:\n    %s: Original title\n    titles: {ru: Русское имя, en: Original English, zz: Hidden translation}\n    required: true\n    default: PREFIX\n    pii: true\n", kind.entity, spelling)
						h, readSaved := standardDisableFixture(t, mode, kind.dir, kind.entity, raw)
						h.runner = NewRunner()
						b, err := h.store.Get("test")
						if err != nil {
							t.Fatal(err)
						}
						renderForm := func() url.Values {
							t.Helper()
							data := h.loadCfgData(ctx, b, "tree")
							if data.Error != "" {
								t.Fatal(data.Error)
							}
							data.AvailableLangs = []i18n.Lang{{Code: "ru", Native: "Русский"}, {Code: "en", Native: "English"}}
							return browserSubmitForEntity(t, renderCfgTree(t, data), kind.entity)
						}
						wantTitle, wantLabel := "Original title", ""
						if spelling == "label" {
							wantTitle, wantLabel = "", "Original title"
						}
						wantTitles := map[string]string{"ru": "Русское имя", "en": "Original English", "zz": "Hidden translation"}
						for round := 0; round < 3; round++ {
							form := renderForm()
							if form.Get("numerator_field_present") != "1" || form.Get("numerator_field_title") != wantTitle+wantLabel {
								t.Fatalf("round %d editor: %v", round, form)
							}
							if _, exists := form["numerator_field_titles.en"]; !exists {
								t.Fatal("English control missing")
							}
							for _, lang := range []string{"ru", "zz"} {
								if _, exists := form["numerator_field_titles."+lang]; exists {
									t.Fatalf("hidden language %s has a control", lang)
								}
							}
							form.Del("numerator_enabled")
							if round == 2 {
								form.Set("numerator_enabled", "1")
							}
							if round == 0 {
								switch action {
								case "edit":
									form.Set("numerator_field_title", "Edited title")
									form.Set("numerator_field_titles.en", "Edited English")
									wantTitle, wantLabel = "Edited title", ""
									wantTitles["en"] = "Edited English"
								case "clear-translation":
									form.Set("numerator_field_titles.en", "  ")
									delete(wantTitles, "en")
								case "clear-title":
									form.Set("numerator_field_title", "  ")
									wantTitle, wantLabel = "", ""
								case "missing-title":
									form.Del("numerator_field_title")
								case "missing-editor":
									for key := range form {
										if key == "numerator_field_present" || key == "numerator_field_title" || key == "numerator_field_titles_present" || key == "numerator_field_titles.en" {
											form.Del(key)
										}
									}
								}
							}
							if ok, errText := cfgResponse(t, postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)); !ok {
								t.Fatal(errText)
							}
							content := readSaved()
							var saved saveEntity
							if err := yaml.Unmarshal(content, &saved); err != nil {
								t.Fatal(err)
							}
							var props *saveStandardField
							if round < 2 {
								if saved.Numerator != nil || len(saved.Fields) != 1 {
									t.Fatalf("round %d disabled shape:\n%s", round, content)
								}
								f := saved.Fields[0]
								if f.Name != kind.field || f.ID != kind.id || f.Type != "string" {
									t.Fatalf("identity changed: %+v", f)
								}
								props = standardProperties(f)
							} else {
								if saved.Numerator == nil || len(saved.Fields) != 0 {
									t.Fatalf("re-enabled shape:\n%s", content)
								}
								props = saved.Numerator.Field
							}
							if props == nil || props.Title != wantTitle || props.Label != wantLabel || !reflect.DeepEqual(props.Titles, wantTitles) || !props.Required || props.Default != "PREFIX" || !props.PII {
								t.Fatalf("round %d properties = %+v; want title=%q label=%q titles=%v", round, props, wantTitle, wantLabel, wantTitles)
							}
							path := filepath.Join(t.TempDir(), "entity.yaml")
							if err := os.WriteFile(path, content, 0600); err != nil {
								t.Fatal(err)
							}
							loaded, err := metadata.LoadFile(path, kind.kind)
							if err != nil {
								t.Fatal(err)
							}
							if len(loaded.Fields) != 1 {
								t.Fatalf("duplicate/missing standard field: %+v", loaded.Fields)
							}
							f := loaded.Fields[0]
							if f.ID != kind.id || f.Name != kind.field || f.Type != metadata.FieldTypeString || f.Title != wantTitle+wantLabel || !reflect.DeepEqual(f.Titles, wantTitles) || !f.Required || f.Default != "PREFIX" || !f.PII {
								t.Fatalf("round %d loaded properties changed: %+v", round, f)
							}
						}
					})
				}
			}
		}
	}
}

func standardDisableFixture(t *testing.T, mode, dir, entity, raw string) (*handler, func() []byte) {
	t.Helper()
	if mode == "file" {
		h, cfgDir := newFileBaseHandler(t)
		path := writeCfgFile(t, cfgDir, dir, entity+".yaml", raw)
		return h, func() []byte {
			t.Helper()
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			return content
		}
	}
	h := dbBackedBase(t, "test")
	b, err := h.store.Get("test")
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenDB(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := configdb.New(db)
	path := filepath.ToSlash(filepath.Join(dir, entity+".yaml"))
	if err := repo.SaveFiles(context.Background(), []configdb.ConfigFile{{Path: "config/app.yaml", Content: []byte("name: Test\n")}, {Path: path, Content: []byte(raw)}}, configdb.VersionOptions{Message: "seed"}); err != nil {
		t.Fatal(err)
	}
	return h, func() []byte {
		t.Helper()
		content, found, err := repo.ReadFile(context.Background(), path)
		if err != nil || !found {
			t.Fatalf("ReadFile: found=%v err=%v", found, err)
		}
		return content
	}
}
