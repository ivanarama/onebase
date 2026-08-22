package launcher

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
)

// Правка, после которой конфигурация не загружается, обязана быть отклонена, а
// файл — остаться прежним. До гейта Конфигуратор писал первым, а узнавал вторым:
// пользователь получал сообщение об ошибке, когда испорченный YAML уже лежал на
// диске, и дальше база не запускалась, а дерево конфигурации было пустым (#1090).
//
// Ломаем связь между объектом и его блоком отображения так, как это делает
// интерфейс: пользователь удаляет реквизит «Фото» кнопкой ×, а tile_view.image
// продолжает на него смотреть. Форма при этом безупречна — проверить такое
// можно только по факту загрузки всей конфигурации.
func TestSaveFields_RejectsEditThatBreaksConfig(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	const original = `name: Номенклатура
title: Номенклатура
fields:
    - name: Наименование
      type: string
    - name: Фото
      type: image
tile_view:
    image: Фото
    title: Наименование
`
	p := writeCfgFile(t, cfgDir, "catalogs", "Номенклатура.yaml", original)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}

	form := url.Values{}
	form.Set("entity", "Номенклатура")
	form.Set("entity_kind", "Справочник")
	form.Set("field.0.name", "Наименование")
	form.Set("field.0.type", "string")
	// Строки «Фото» в форме нет — реквизит удалён.

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	ok, errText := cfgResponse(t, rec)
	if ok {
		t.Fatalf("правка принята, хотя ломает конфигурацию")
	}
	// Сообщение обязано отличать «не сохранилось» от «сохранили и вернули как
	// было»: во втором случае пользователю важно знать, что файл цел.
	if !strings.Contains(errText, "откачена") {
		t.Errorf("текст ошибки не говорит об откате: %s", errText)
	}
	if !strings.Contains(errText, "tile_view") {
		t.Errorf("текст ошибки не называет причину: %s", errText)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("чтение %s: %v", p, err)
	}
	if string(got) != original {
		t.Errorf("файл изменился, хотя правка отклонена:\n%s", got)
	}
	if err := h.configLoadError(context.Background(), b); err != nil {
		t.Errorf("после отклонённой правки конфигурация не загружается: %v", err)
	}
}

// Обратная сторона гейта: конфигурация, сломанная до правки (руками, чужим
// инструментом, неудачным слиянием), не должна запирать Конфигуратор. Иначе
// единственный способ починить объект — текстовый редактор, а гейт из защиты
// превращается в ловушку.
func TestSaveFields_AllowsEditWhenConfigAlreadyBroken(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	// Сломан посторонний объект: tile_view смотрит на несуществующий реквизит.
	writeCfgFile(t, cfgDir, "catalogs", "Контрагент.yaml", `name: Контрагент
fields:
    - name: Наименование
      type: string
tile_view:
    image: Логотип
`)
	p := writeCfgFile(t, cfgDir, "catalogs", "Склад.yaml", `name: Склад
fields:
    - name: Наименование
      type: string
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if err := h.configLoadError(context.Background(), b); err == nil {
		t.Fatal("подготовка теста: конфигурация обязана быть сломанной")
	}

	form := url.Values{}
	form.Set("entity", "Склад")
	form.Set("entity_kind", "Справочник")
	form.Set("field.0.name", "Наименование")
	form.Set("field.0.type", "string")
	form.Set("new_field.1.name", "Адрес")
	form.Set("new_field.1.type", "string")

	postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	assertFileContains(t, p, "Адрес")
}

// Обычная правка гейт не трогает: сохранение работает и файл меняется.
func TestSaveFields_GuardKeepsNormalEditWorking(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "Склад.yaml", `name: Склад
fields:
    - name: Наименование
      type: string
`)

	form := url.Values{}
	form.Set("entity", "Склад")
	form.Set("entity_kind", "Справочник")
	form.Set("field.0.name", "Наименование")
	form.Set("field.0.type", "string")
	form.Set("new_field.1.name", "Адрес")
	form.Set("new_field.1.type", "string")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("обычная правка не прошла: %s", errText)
	}
	assertFileContains(t, p, "Адрес")
}

// Гейт стоит не только на реквизитах объекта: те же грабли у регистров,
// перечислений, констант и предопределённых элементов — все они пишут YAML,
// который читает загрузка. Проверяем на константе, что обёртка подключена и
// ведёт себя так же.
//
// Регистры для этой проверки не подошли: висячая ссылка измерения на
// несуществующий справочник загрузку НЕ ломает — metadata/validate.go проверяет
// ссылки у констант и реквизитов объектов, а у измерений и ресурсов регистра
// не проверяет вовсе. Гейт такую правку пропускает, и это не его недосмотр:
// он сторожит загружаемость, а не полноту проверок.
func TestSaveConstant_RejectsEditThatBreaksConfig(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	writeCfgFile(t, cfgDir, "catalogs", "Склад.yaml", `name: Склад
fields:
    - name: Наименование
      type: string
`)
	const original = `constants:
    - name: ОсновнойСклад
      type: reference:Склад
      label: Основной склад
`
	p := writeCfgFile(t, cfgDir, "constants", "константы.yaml", original)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if err := h.configLoadError(context.Background(), b); err != nil {
		t.Fatalf("подготовка теста: конфигурация обязана загружаться, а не %v", err)
	}

	form := url.Values{}
	form.Set("const_name", "ОсновнойСклад")
	form.Set("label", "Основной склад")
	form.Set("type", "reference")
	form.Set("ref", "НетТакогоСправочника")

	rec := postCfg(t, "test", "/bases/test/configurator/constant", form, h.configuratorSaveConstant)
	ok, errText := cfgResponse(t, rec)
	if ok {
		t.Fatalf("правка принята, хотя ломает конфигурацию")
	}
	if !strings.Contains(errText, "откачена") {
		t.Errorf("текст ошибки не говорит об откате: %s", errText)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("чтение %s: %v", p, err)
	}
	if string(got) != original {
		t.Errorf("файл изменился, хотя правка отклонена:\n%s", got)
	}
	if err := h.configLoadError(context.Background(), b); err != nil {
		t.Errorf("после отклонённой правки конфигурация не загружается: %v", err)
	}
}
