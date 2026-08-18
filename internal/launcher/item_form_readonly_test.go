package launcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Конфигуратор должен ПОКАЗЫВАТЬ признак «только просмотр» (#1011). Иначе
// круг получается односторонним: в YAML признак есть, в редакторе состава форм
// его не видно, и первое же «Сохранить формы» его стирает.
func TestConfiguratorShowsReadonlyItemFormFlag(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "name: Клиенты\n" +
		"fields:\n" +
		"  - {name: Наименование, type: string}\n" +
		"  - {name: ТелефоныНорм, type: string}\n" +
		"  - {name: Комментарий, type: string}\n" +
		"item_form:\n" +
		"  - Наименование\n" +
		"  - name: ТелефоныНорм\n" +
		"    readonly: true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "catalogs", "клиенты.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	store := newTestStore(t)
	base := &Base{ID: "b", Name: "b", ConfigSource: "file", Path: cfgDir}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}

	h := &handler{store: store, runner: NewRunner()}
	data := h.loadCfgData(context.Background(), base, "tree")

	var found bool
	for _, e := range data.Entities {
		if e.Name != "Клиенты" {
			continue
		}
		for _, f := range e.Fields {
			switch f.Name {
			case "ТелефоныНорм":
				found = true
				if !f.FormItemReadOnly {
					t.Error("реквизит «только просмотр» показан обычным")
				}
				if f.FormItemHidden {
					t.Error("реквизит «только просмотр» помечен скрытым")
				}
			case "Наименование":
				if f.FormItemReadOnly {
					t.Error("признак «только просмотр» протёк на соседний реквизит")
				}
			case "Комментарий":
				if !f.FormItemHidden {
					t.Error("реквизит вне item_form не помечен скрытым")
				}
			}
		}
	}
	if !found {
		t.Fatalf("сущность или реквизит не загружены: %+v", data.Entities)
	}
}
