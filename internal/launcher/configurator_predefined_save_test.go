package launcher

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

// Новая строка «Предопределённые элементы» молча не сохранялась (issue #671):
// элемент виден в конфигураторе, сохранение отвечает успехом, но ни в YAML, ни в
// режиме предприятия его нет.
//
// Клиент называет поля новой строки индексом из счётчика, начинающегося с 10000
// (`cfgAddPreRow`, static/configurator.js), а обработчик перебирал индексы
// подряд от нуля, останавливаясь на первом пропуске. Существующие строки при
// этом сохранялись, поэтому и ошибки не было — форма просто записывала прежний
// список.

const predefinedCatalogYAML = `name: СтавкаНДС
fields:
  - {name: Наименование, type: string}
  - {name: Ставка, type: number}
predefined:
  - name: БезНДС
    fields: {Наименование: "Без НДС", Ставка: "0"}
`

// Индекс новой строки приходит разреженным — ровно так, как его формирует
// клиент. Элемент обязан сохраниться, а существующий — уцелеть.
func TestConfiguratorSavePredefined_SparseRowIndexIsSaved(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "ставка_ндс.yaml", predefinedCatalogYAML)

	rec := postCfg(t, "test", "/bases/test/configurator/predefined", url.Values{
		"entity":                       {"СтавкаНДС"},
		"pre_field_names":              {"Наименование", "Ставка"},
		"pre.0.name":                   {"БезНДС"},
		"pre.0.field.Наименование":     {"Без НДС"},
		"pre.0.field.Ставка":           {"0"},
		"pre.10001.name":               {"НДС22"},
		"pre.10001.field.Наименование": {"НДС 22%"},
		"pre.10001.field.Ставка":       {"22"},
	}, h.configuratorSavePredefined)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	assertFileContains(t, p, "БезНДС", "НДС22", "НДС 22%")
}

// Порядок строк задаёт пользователь перетаскиванием и удалением, поэтому
// разреженность бывает и в середине: удалили строку — в индексах дыра. Прежний
// обработчик обрывался на ней и терял всё, что после.
func TestConfiguratorSavePredefined_GapInIndexesKeepsAllRows(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "ставка_ндс.yaml", predefinedCatalogYAML)

	rec := postCfg(t, "test", "/bases/test/configurator/predefined", url.Values{
		"entity":          {"СтавкаНДС"},
		"pre_field_names": {"Наименование", "Ставка"},
		"pre.0.name":      {"БезНДС"},
		// индекс 1 удалён пользователем
		"pre.2.name": {"НДС20"},
		"pre.3.name": {"НДС22"},
	}, h.configuratorSavePredefined)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	assertFileContains(t, p, "БезНДС", "НДС20", "НДС22")
}

// Пустой список — законное состояние: пользователь удалил все предопределённые.
// Ключ predefined при этом должен исчезнуть, а не остаться прежним.
func TestConfiguratorSavePredefined_EmptyListRemovesKey(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "ставка_ндс.yaml", predefinedCatalogYAML)

	rec := postCfg(t, "test", "/bases/test/configurator/predefined", url.Values{
		"entity":          {"СтавкаНДС"},
		"pre_field_names": {"Наименование", "Ставка"},
	}, h.configuratorSavePredefined)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if got := string(raw); strings.Contains(got, "predefined:") {
		t.Errorf("ключ predefined не удалён:\n%s", got)
	}
}

// Сохранение предопределённых круглило файл сущности через map[string]any,
// поэтому порядок ключей и комментарии терялись при каждом нажатии «Сохранить»
// — тот же класс, что #656 у app.yaml. Файл пользовательский и лежит под git.
func TestConfiguratorSavePredefined_KeepsFileOrderAndComments(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	const src = `# Ставки НДС для торговли
name: СтавкаНДС
title: Ставка НДС
fields:
  - {name: Наименование, type: string}
  - {name: Ставка, type: number}
`
	p := writeCfgFile(t, cfgDir, "catalogs", "ставка_ндс.yaml", src)

	rec := postCfg(t, "test", "/bases/test/configurator/predefined", url.Values{
		"entity":          {"СтавкаНДС"},
		"pre_field_names": {"Наименование", "Ставка"},
		"pre.10001.name":  {"НДС22"},
	}, h.configuratorSavePredefined)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "# Ставки НДС для торговли") {
		t.Errorf("комментарий потерян:\n%s", got)
	}
	if strings.Index(got, "name: СтавкаНДС") > strings.Index(got, "fields:") {
		t.Errorf("порядок ключей переставлен:\n%s", got)
	}
	if !strings.Contains(got, "НДС22") {
		t.Errorf("предопределённый не сохранён:\n%s", got)
	}
}
