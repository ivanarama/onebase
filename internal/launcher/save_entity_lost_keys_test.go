package launcher

import (
	"net/url"
	"testing"
)

// Приёмка этапа 118A через ПУБЛИЧНУЮ точку входа: пользователь добавляет
// реквизит справочника в конфигураторе, и остальной YAML обязан уцелеть.
//
// До фикса правка реквизитов молча стирала tile_view, indexes, list_mode,
// list_refresh_on, notify_changes и description — saveEntity о них не знала, а
// сохранение идёт round-trip'ом Unmarshal → Marshal через неё. Ничего не
// падало: следующий запуск просто работал по умолчаниям.
func TestSaveFields_KeepsKeysConfiguratorDoesNotEdit(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "Номенклатура.yaml", `name: Номенклатура
title: Номенклатура
description: Товары и услуги
list_mode: tiles
list_refresh_on:
  - ПоступлениеТоваров
notify_changes: true
presentation:
  - Артикул
  - Наименование
indexes:
  - fields: [Артикул]
    unique: true
tile_view:
  image: Фото
  title: Наименование
  fields: []
detail_panel:
  title: Наименование
  width: 444
  tabs:
    - name: Медиа
      titles: {en: Media}
      fields: [Фото]
fields:
  - name: Наименование
    type: string
  - name: Артикул
    type: string
  - name: Фото
    type: image
`)

	form := url.Values{}
	form.Set("entity", "Номенклатура")
	form.Set("entity_kind", "Справочник")
	form.Set("field.0.name", "Наименование")
	form.Set("field.0.type", "string")
	form.Set("field.1.name", "Артикул")
	form.Set("field.1.type", "string")
	form.Set("field.2.name", "Фото")
	form.Set("field.2.type", "image")
	// пользователь добавил реквизит — ровно то действие, после которого ключи и пропадали
	form.Set("new_field.1.name", "Поставщик")
	form.Set("new_field.1.type", "string")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	assertFileContains(t, p,
		"Поставщик", // правка применилась
		"description: Товары и услуги",
		"list_mode: tiles",
		"ПоступлениеТоваров", // list_refresh_on
		"notify_changes: true",
		"presentation:",
		"- Артикул",
		"- Наименование",
		"unique: true", // indexes
		"image: Фото",  // tile_view
		"detail_panel:",
		"width: 444",
		"en: Media",
		"name: Медиа",
	)
	// tile_view.fields: [] значит «в плитке только картинка и заголовок» —
	// отличие от отсутствия ключа обязано пережить round-trip.
	assertFileContains(t, p, "fields: []")
}

func TestSaveFields_KeepsExplicitEmptyDetailPanelFields(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "ПустаяПанель.yaml", `name: ПустаяПанель
detail_panel:
  title: Наименование
  width: 320
  fields: []
fields:
  - name: Наименование
    type: string
`)

	form := url.Values{}
	form.Set("entity", "ПустаяПанель")
	form.Set("entity_kind", "Справочник")
	form.Set("field.0.name", "Наименование")
	form.Set("field.0.type", "string")
	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}
	assertFileContains(t, p, "detail_panel:", "width: 320", "fields: []")
}

// Ключ presentation (#846) переживает правку реквизитов из конфигуратора — и в
// скалярной форме, и списком.
//
// Скалярная форма важна отдельно: зеркальный []string превратил бы
// `presentation: Артикул` в список из одного элемента, то есть переписал бы
// строку, которую автор не трогал. Поэтому ключ проводится сырым узлом.
func TestSaveFields_KeepsPresentationKey(t *testing.T) {
	cases := map[string]struct{ yaml, want string }{
		"строка": {"presentation: Артикул\n", "presentation: Артикул"},
		"список": {"presentation: [Артикул, Наименование]\n", "presentation: [Артикул, Наименование]"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, cfgDir := newFileBaseHandler(t)
			h.runner = NewRunner()
			p := writeCfgFile(t, cfgDir, "catalogs", "Номенклатура.yaml",
				"name: Номенклатура\n"+tc.yaml+`fields:
  - name: Наименование
    type: string
  - name: Артикул
    type: string
`)

			form := url.Values{}
			form.Set("entity", "Номенклатура")
			form.Set("entity_kind", "Справочник")
			form.Set("field.0.name", "Наименование")
			form.Set("field.0.type", "string")
			form.Set("field.1.name", "Артикул")
			form.Set("field.1.type", "string")
			form.Set("new_field.1.name", "Поставщик")
			form.Set("new_field.1.type", "string")

			rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
			if ok, errText := cfgResponse(t, rec); !ok {
				t.Fatalf("сохранение не удалось: %s", errText)
			}
			assertFileContains(t, p, "Поставщик", tc.want)
		})
	}
}
