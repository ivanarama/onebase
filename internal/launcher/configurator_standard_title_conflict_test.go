package launcher

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
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

// Both editors are collected from the real HTML form after numbering is
// disabled. A stale duplicate must not undo an edit made in the other editor.
func TestSaveFields_StandardTitleEditors(t *testing.T) {
	for _, mode := range []string{"file", "database"} {
		for _, kind := range []struct {
			dir, entity, field, id string
			kind                   metadata.Kind
		}{
			{"catalogs", "Клиенты", "Код", metadata.StandardCodeFieldID, metadata.KindCatalog},
			{"documents", "Приказ", "Номер", metadata.StandardNumberFieldID, metadata.KindDocument},
		} {
			for _, enableOnEdit := range []bool{false, true} {
				for _, action := range []string{"unchanged", "ordinary-edit", "ordinary-clear", "numerator-edit", "numerator-clear", "same-edit", "same-clear", "different-languages", "missing-ordinary", "missing-numerator", "both-missing", "conflicting-edits", "conflicting-clear"} {
					t.Run(fmt.Sprintf("%s/%s/enable=%t/%s", mode, kind.dir, enableOnEdit, action), func(t *testing.T) {
						raw := fmt.Sprintf("name: %s\nnumerator:\n  length: 8\n  field:\n    label: Original title\n    titles: {ru: Русское имя, en: Original English, de: Original German, zz: Hidden translation}\n    required: true\n    default: PREFIX\n    pii: true\n", kind.entity)
						h, readSaved := standardDisableFixture(t, mode, kind.dir, kind.entity, raw)
						h.runner = NewRunner()
						b, err := h.store.Get("test")
						if err != nil {
							t.Fatal(err)
						}
						render := func() url.Values {
							t.Helper()
							data := h.loadCfgData(context.Background(), b, "tree")
							if data.Error != "" {
								t.Fatal(data.Error)
							}
							data.AvailableLangs = []i18n.Lang{{Code: "ru", Native: "Русский"}, {Code: "en", Native: "English"}, {Code: "de", Native: "Deutsch"}}
							return browserSubmitForEntity(t, renderCfgTree(t, data), kind.entity)
						}
						submit := func(form url.Values) (bool, string) {
							t.Helper()
							return cfgResponse(t, postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields))
						}
						first := render()
						first.Del("numerator_enabled")
						if ok, msg := submit(first); !ok {
							t.Fatal(msg)
						}
						form := render()
						prefix := ""
						for key := range form {
							if strings.HasPrefix(key, "field.") && strings.HasSuffix(key, ".name") && form.Get(key) == kind.field {
								prefix = strings.TrimSuffix(key, "name") + "titles."
							}
						}
						if prefix == "" {
							t.Fatal("standard ordinary row missing")
						}
						ordinary, numerator := prefix+"en", "numerator_field_titles.en"
						for _, key := range []string{ordinary, numerator, prefix + "de", "numerator_field_titles.de"} {
							if _, ok := form[key]; !ok {
								t.Fatalf("control %s missing", key)
							}
						}
						want := map[string]string{"ru": "Русское имя", "en": "Original English", "de": "Original German", "zz": "Hidden translation"}
						switch action {
						case "ordinary-edit":
							form.Set(ordinary, "Edited English")
							want["en"] = "Edited English"
						case "ordinary-clear":
							form.Set(ordinary, "  ")
							delete(want, "en")
						case "numerator-edit":
							form.Set(numerator, "Edited English")
							want["en"] = "Edited English"
						case "numerator-clear":
							form.Set(numerator, "  ")
							delete(want, "en")
						case "same-edit":
							form.Set(ordinary, "Edited English")
							form.Set(numerator, "Edited English")
							want["en"] = "Edited English"
						case "same-clear":
							form.Set(ordinary, "  ")
							form.Set(numerator, "  ")
							delete(want, "en")
						case "different-languages":
							form.Set(ordinary, "Edited English")
							form.Set("numerator_field_titles.de", "Edited German")
							want["en"] = "Edited English"
							want["de"] = "Edited German"
						case "missing-ordinary":
							form.Del(ordinary)
							form.Set(numerator, "Edited English")
							want["en"] = "Edited English"
						case "missing-numerator":
							form.Del(numerator)
							form.Set(ordinary, "Edited English")
							want["en"] = "Edited English"
						case "both-missing":
							form.Del(ordinary)
							form.Del(numerator)
						case "conflicting-edits":
							form.Set(ordinary, "Ordinary English")
							form.Set(numerator, "Numbering English")
						case "conflicting-clear":
							form.Set(ordinary, "  ")
							form.Set(numerator, "Numbering English")
						}
						if enableOnEdit {
							form.Set("numerator_enabled", "1")
						}
						before := readSaved()
						var readVersions func() []configdb.Version
						var versionsBefore []configdb.Version
						if mode == "database" && strings.HasPrefix(action, "conflicting-") {
							db, err := OpenDB(context.Background(), b)
							if err != nil {
								t.Fatal(err)
							}
							defer db.Close()
							readVersions = func() []configdb.Version {
								t.Helper()
								versions, err := configdb.New(db).ListVersions(context.Background(), 0)
								if err != nil {
									t.Fatal(err)
								}
								return versions
							}
							versionsBefore = readVersions()
						}
						ok, msg := submit(form)
						if strings.HasPrefix(action, "conflicting-") {
							if ok || !strings.Contains(msg, "en") || !strings.Contains(msg, kind.field) {
								t.Fatalf("expected explicit conflict for %s/en, got ok=%v message=%q", kind.field, ok, msg)
							}
							if !bytes.Equal(before, readSaved()) {
								t.Fatal("rejected edit changed stored YAML")
							}
							if readVersions != nil && !reflect.DeepEqual(versionsBefore, readVersions()) {
								t.Fatal("rejected edit changed DB version history")
							}
							// A rejected form must leave both editors and every property usable.
							form = render()
							form.Set(ordinary, "Edited English")
							want["en"] = "Edited English"
							if enableOnEdit {
								form.Set("numerator_enabled", "1")
							}
							if ok, msg = submit(form); !ok {
								t.Fatal(msg)
							}
						} else if !ok {
							t.Fatal(msg)
						}
						for round := 0; round < 3; round++ {
							content := readSaved()
							var saved saveEntity
							if err := yaml.Unmarshal(content, &saved); err != nil {
								t.Fatal(err)
							}
							numbered := enableOnEdit || round == 2
							if numbered {
								if saved.Numerator == nil || len(saved.Fields) != 0 {
									t.Fatalf("duplicate/noncanonical numbered shape:\n%s", content)
								}
							} else if saved.Numerator != nil || len(saved.Fields) != 1 {
								t.Fatalf("noncanonical disabled shape:\n%s", content)
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
								t.Fatalf("duplicate standard fields: %+v", loaded.Fields)
							}
							f := loaded.Fields[0]
							if f.ID != kind.id || f.Name != kind.field || f.Type != metadata.FieldTypeString || f.Title != "Original title" || !reflect.DeepEqual(f.Titles, want) || !f.Required || f.Default != "PREFIX" || !f.PII {
								t.Fatalf("round %d properties changed: %+v; want %v", round, f, want)
							}
							var label string
							if saved.Numerator != nil {
								label = saved.Numerator.Field.Label
							} else {
								label = saved.Fields[0].Label
							}
							if label != "Original title" {
								t.Fatalf("legacy label lost: %q", label)
							}
							if round < 2 {
								again := render()
								if round == 1 {
									again.Set("numerator_enabled", "1")
								}
								if ok, msg := submit(again); !ok {
									t.Fatal(msg)
								}
								if round == 0 && !bytes.Equal(content, readSaved()) {
									t.Fatal("unchanged repeated save is not byte stable")
								}
							}
						}
					})
				}
			}
		}
	}
}
