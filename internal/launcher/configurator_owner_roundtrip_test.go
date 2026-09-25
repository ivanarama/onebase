package launcher

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Владелец — синтетический реквизит: пользователь видит его в настоящей
// странице конфигуратора, хотя в исходном YAML лежит только owner:. Проверка
// проходит публичный HTTP save-путь и повторную metadata.LoadFile, чтобы
// поймать потерю std_owner в любом звене round-trip.
func TestSaveFields_OwnerKeepsStableIDAcrossChangeAndRemoval(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	writeCfgFile(t, cfgDir, "catalogs", "Контрагент.yaml", "name: Контрагент\n")
	writeCfgFile(t, cfgDir, "catalogs", "Партнёр.yaml", "name: Партнёр\n")
	target := writeCfgFile(t, cfgDir, "catalogs", "Договор.yaml", "name: Договор\nowner: Контрагент\n")
	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}

	saveOwner := func(owner string) {
		t.Helper()
		data := h.loadCfgData(context.Background(), b, "tree")
		if data.Error != "" {
			t.Fatalf("конфигурация не загрузилась: %s", data.Error)
		}
		form := browserSubmitForEntity(t, renderCfgTree(t, data), "Договор")
		form.Set("owner", owner)
		rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
		if ok, errText := cfgResponse(t, rec); !ok {
			t.Fatalf("сохранение owner=%q не удалось: %s", owner, errText)
		}
	}
	assertOwner := func(wantOwner, wantRef string, wantField bool) {
		t.Helper()
		entity, err := metadata.LoadFile(target, metadata.KindCatalog)
		if err != nil {
			t.Fatalf("LoadFile после сохранения: %v", err)
		}
		if entity.Owner != wantOwner {
			t.Fatalf("Owner = %q, ожидали %q", entity.Owner, wantOwner)
		}
		var found *metadata.Field
		for i := range entity.Fields {
			if strings.EqualFold(entity.Fields[i].Name, metadata.StandardOwnerField) {
				found = &entity.Fields[i]
				break
			}
		}
		if !wantField {
			if found != nil {
				t.Fatalf("после снятия owner осталось поле %+v", *found)
			}
			return
		}
		if found == nil {
			t.Fatal("синтетический реквизит Владелец потерян")
		}
		if found.ID != metadata.StandardOwnerFieldID || found.RefEntity != wantRef {
			t.Fatalf("Владелец = %+v, ожидали id=%q ref=%q", *found, metadata.StandardOwnerFieldID, wantRef)
		}
	}

	saveOwner("Контрагент")
	assertOwner("Контрагент", "Контрагент", true)

	saveOwner("Партнёр")
	assertOwner("Партнёр", "Партнёр", true)

	saveOwner("")
	assertOwner("", "", false)
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), metadata.StandardOwnerFieldID) {
		t.Fatalf("после снятия owner в YAML остался %s:\n%s", metadata.StandardOwnerFieldID, raw)
	}
}
