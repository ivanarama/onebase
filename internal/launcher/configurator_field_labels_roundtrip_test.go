package launcher

import (
	"context"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Подпись реквизита (`title`, устаревший синоним `label`) и обязательность
// (`required`) редактор реквизитов не рисует вовсе: в форме есть только имя,
// тип, разрядность, ссылка и переводы, причём titles-block пропускает базовый
// язык ru. Значит, прислать эти ключи обратно неоткуда, и round-trip
// (Unmarshal → правка → Marshal) стирал их при ЛЮБОМ сохранении объекта — даже
// когда пользователь трогал совсем другой реквизит (#1207).
//
// Проверка идёт ПУБЛИЧНЫМ путём: пользователь не зовёт ensureFieldIDs, он
// нажимает «Сохранить типы полей», а по дороге к YAML стоят ещё разбор формы,
// applyFieldEdits и marshal через saveEntity. Тест на приватную функцию был бы
// зелёным и при потере ключа в любом из этих звеньев — ровно тот случай, что
// разбирался в #611.
//
// Форма снимается с реально отрисованной страницы, как её отправил бы браузер:
// значения, которых интерфейс не даёт, тест подставлять не должен — иначе он
// проверял бы перенос, которого нет, а есть присылка с формы.
func TestSaveFields_KeepsTitleLabelRequired(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "documents", "РеализацияТоваров.yaml", `name: РеализацияТоваров
fields:
    - name: Дата
      type: date
      title: Дата отгрузки
      required: true
    - name: Комментарий
      type: string
      label: Примечание
tableparts:
    - name: Товары
      fields:
        - name: Количество
          type: number
          title: Кол-во
          required: true
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatalf("конфигурация не загрузилась: %s", data.Error)
	}
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "РеализацияТоваров")

	// Единственное действие пользователя — добавить посторонний реквизит.
	// Именно оно раньше и снимало обязательность со всего объекта.
	form.Set("new_field.1.name", "Скидка")
	form.Set("new_field.1.type", "number")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	assertFileContains(t, p,
		"Скидка",               // правка применилась
		"title: Дата отгрузки", // подпись шапки уцелела
		"label: Примечание",    // и устаревший синоним тоже
		"title: Кол-во",        // подпись реквизита табличной части
	)

	// Ключи не расползаются на соседей: перенос идёт по имени реквизита, а не
	// «проставить всем». Обязательных было двое — столько и осталось.
	if got := strings.Count(readCfg(t, p), "required: true"); got != 2 {
		t.Fatalf("required: true встречается %d раз, ожидалось 2:\n%s", got, readCfg(t, p))
	}

	// Главное следствие: значения не просто лежат в файле, а доезжают до
	// загруженной модели — форма снова покажет обязательность и подпись.
	ent, err := metadata.LoadFile(p, metadata.KindDocument)
	if err != nil {
		t.Fatalf("после сохранения объект не читается: %v", err)
	}
	byName := map[string]metadata.Field{}
	for _, f := range ent.Fields {
		byName[f.Name] = f
	}
	if f := byName["Дата"]; !f.Required || f.Title != "Дата отгрузки" {
		t.Errorf("реквизит Дата: required=%v title=%q", f.Required, f.Title)
	}
	// label читается parseField в Title при пустом title — подпись обязана
	// остаться той, что видел пользователь, а не откатиться к имени поля.
	if f := byName["Комментарий"]; f.Title != "Примечание" {
		t.Errorf("реквизит Комментарий: подпись из label потеряна, title=%q", f.Title)
	}
	// Новому реквизиту переносить нечего — он не должен приобрести чужое.
	if f := byName["Скидка"]; f.Required || f.Title != "" {
		t.Errorf("новый реквизит Скидка получил чужие ключи: required=%v title=%q", f.Required, f.Title)
	}
	if len(ent.TableParts) != 1 || len(ent.TableParts[0].Fields) != 1 {
		t.Fatalf("табличная часть потерялась: %+v", ent.TableParts)
	}
	if f := ent.TableParts[0].Fields[0]; !f.Required || f.Title != "Кол-во" {
		t.Errorf("реквизит ТЧ Количество: required=%v title=%q", f.Required, f.Title)
	}
}

// Объект, у которого этих ключей не было, их не приобретает: перенос
// односторонний, но не «раздать всем, у кого сосед обязательный».
func TestSaveFields_NoTitleRequiredStaysClean(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "Склад.yaml", `name: Склад
fields:
    - name: Наименование
      type: string
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatalf("конфигурация не загрузилась: %s", data.Error)
	}
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "Склад")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	got := readCfg(t, p)
	for _, key := range []string{"required:", "title:", "label:"} {
		if strings.Contains(got, key) {
			t.Errorf("в объекте без подписей и обязательности появился ключ %q:\n%s", key, got)
		}
	}
}
