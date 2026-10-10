package launcher

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestConfiguratorSearchPathRoundTrip(t *testing.T) {
	for _, keys := range []string{"", "search_fields: []\nfulltext: []\n", "search_fields: [Наименование, Контакты.Значение]\nfulltext: [Наименование]\n"} {
		t.Run(strings.TrimSpace(keys), func(t *testing.T) {
			h, dir := newFileBaseHandler(t)
			h.runner = NewRunner()
			path := writeCfgFile(t, dir, "catalogs", "Клиенты.yaml", `name: Клиенты
fields:
  - {name: Наименование, type: string}
tableparts:
  - name: Контакты
    fields:
      - {name: Значение, type: string}
`+keys)
			before, err := metadata.LoadFile(path, metadata.KindCatalog)
			if err != nil {
				t.Fatal(err)
			}
			b, err := h.store.Get("test")
			if err != nil {
				t.Fatal(err)
			}
			data := h.loadCfgData(context.Background(), b, "tree")
			if data.Error != "" {
				t.Fatal(data.Error)
			}
			form := browserSubmitForEntity(t, renderCfgTree(t, data), "Клиенты")
			form.Set("new_field.1.name", "Комментарий")
			form.Set("new_field.1.type", "string")
			rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
			if ok, errText := cfgResponse(t, rec); !ok {
				t.Fatal(errText)
			}
			after, err := metadata.LoadFile(path, metadata.KindCatalog)
			if err != nil {
				t.Fatal(err)
			}
			if after.SearchSet != before.SearchSet || after.FullTextSet != before.FullTextSet || !reflect.DeepEqual(after.Search, before.Search) || !reflect.DeepEqual(after.FullText, before.FullText) {
				t.Fatalf("search keys lost: before %+v %+v, after %+v %+v", before.Search, before.FullText, after.Search, after.FullText)
			}
			if err := metadata.Validate([]*metadata.Entity{after}, nil); err != nil {
				t.Fatal(err)
			}
			paths, err := metadata.SearchFieldPaths(after)
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) > 0 && strings.Contains(keys, "Контакты.") && (len(paths) != 2 || paths[1].TablePart == nil) {
				t.Fatalf("dotted path lost: %+v", paths)
			}
			if len(after.Fields) != 2 {
				t.Fatal("browser edit was not saved")
			}
		})
	}
}
