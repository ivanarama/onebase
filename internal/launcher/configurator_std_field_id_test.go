package launcher

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// formHasFieldNamed — есть ли в снятой с страницы форме строка реквизита с
// таким именем. Редактор шлёт имя скрытым полем field.N.name.
func formHasFieldNamed(form url.Values, name string) bool {
	for key, vals := range form {
		if !strings.HasPrefix(key, "field.") || !strings.HasSuffix(key, ".name") {
			continue
		}
		for _, v := range vals {
			if strings.EqualFold(strings.TrimSpace(v), name) {
				return true
			}
		}
	}
	return false
}

// «Код» справочника с блоком numerator платформа синтезирует при загрузке и в
// YAML не пишет (metadata/yaml.go): в файле поля нет, а в редакторе реквизитов
// оно есть наравне с обычными. Раньше сохранение выдавало ему свежий f_xxxx —
// имени «Код» в прежнем состоянии файла не находилось, — и при следующем старте
// колонка `код` числилась за std_code, а занимало её поле с чужим id. Миграция
// останавливалась сторожем коллизии (#1161).
//
// Путь теста — публичный: страница конфигуратора рисуется целиком, форма
// снимается с неё так, как её отправил бы браузер, и уходит в тот же обработчик
// «Сохранить типы полей». Юнит на ensureFieldIDs остался бы зелёным при потере
// связи в любом другом звене (#611).
func TestSaveFields_CatalogCodeKeepsStandardID(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "Ученики.yaml", `name: Ученики
numerator:
    length: 8
    period: none
    unique: true
fields:
    - id: f_4b7b017c
      name: Фамилия
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
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "Ученики")
	// Форма обязана содержать синтезированный «Код» — иначе тест проверяет не
	// тот сценарий и молча зеленеет.
	if !formHasFieldNamed(form, metadata.StandardCodeField) {
		t.Fatalf("в форме реквизитов нет синтезированного «Кода»: %v", form)
	}

	// Единственное действие пользователя — добавить посторонний реквизит.
	form.Set("new_field.1.name", "Имя")
	form.Set("new_field.1.type", "string")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	assertStandardFieldSaved(t, p, metadata.KindCatalog, metadata.StandardCodeField, metadata.StandardCodeFieldID)

	if after := h.loadCfgData(context.Background(), b, "tree"); after.Error != "" {
		t.Fatalf("после сохранения конфигурация не загружается: %s", after.Error)
	}
}

// То же для «Номера» документа: поле синтезируется тем же кодом и тем же
// образом получало чужой id.
func TestSaveFields_DocumentNumberKeepsStandardID(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "documents", "Приказ.yaml", `name: Приказ
numerator:
    prefix: ПР-
    length: 8
    period: year
fields:
    - id: f_4b7b017c
      name: Дата
      type: date
`)

	b, err := h.store.Get("test")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatalf("конфигурация не загрузилась: %s", data.Error)
	}
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "Приказ")
	if !formHasFieldNamed(form, metadata.StandardNumberField) {
		t.Fatalf("в форме реквизитов нет синтезированного «Номера»: %v", form)
	}

	form.Set("new_field.1.name", "Комментарий")
	form.Set("new_field.1.type", "string")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	assertStandardFieldSaved(t, p, metadata.KindDocument, metadata.StandardNumberField, metadata.StandardNumberFieldID)

	if after := h.loadCfgData(context.Background(), b, "tree"); after.Error != "" {
		t.Fatalf("после сохранения конфигурация не загружается: %s", after.Error)
	}
}

// Снятие автонумерации тем же сохранением: «Код» остаётся в fields обычным
// реквизитом, но колонка в базе по-прежнему числится за std_code — свежий
// f_xxxx дал бы ту же коллизию, от которой чинимся. Поэтому засев смотрит и на
// прежнее состояние файла, а не только на приходящее.
func TestSaveFields_CodeKeepsStandardIDWhenNumeratorTurnedOff(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "Ученики.yaml", `name: Ученики
numerator:
    length: 8
fields:
    - id: f_4b7b017c
      name: Фамилия
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
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "Ученики")
	if form.Get("numerator_present") != "1" {
		t.Fatalf("форма не несёт маркер нумерации, снимать нечего: %v", form)
	}
	// Пользователь снял галочку «Выдавать код автоматически».
	form.Del("numerator_enabled")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("чтение %s: %v", p, err)
	}
	if strings.Contains(string(raw), "numerator:") {
		t.Fatalf("нумерация не снялась — тест проверяет не тот сценарий:\n%s", raw)
	}
	assertStandardFieldSaved(t, p, metadata.KindCatalog, metadata.StandardCodeField, metadata.StandardCodeFieldID)
}

// Негативный: без блока numerator «Код» — обычный пользовательский реквизит, и
// служебный id ему не положен. Иначе фикс сам испортил бы соответствие полей
// колонкам там, где стандартного поля нет вовсе.
func TestSaveFields_PlainCodeFieldGetsOwnID(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "Студенты.yaml", `name: Студенты
fields:
    - name: Код
      type: string
    - name: Фамилия
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
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "Студенты")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("чтение %s: %v", p, err)
	}
	if strings.Contains(string(raw), metadata.StandardCodeFieldID) {
		t.Fatalf("пользовательский «Код» без numerator получил служебный id:\n%s", raw)
	}
}

// Реквизит документа с именем «Код» — пользовательский всегда: стандартное поле
// документа зовётся «Номер». Служебный id ему не положен даже при включённой
// нумерации, иначе засев привязал бы его к чужой колонке.
func TestSaveFields_DocumentCodeFieldGetsOwnID(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "documents", "Заявка.yaml", `name: Заявка
numerator:
    length: 8
fields:
    - name: Код
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
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "Заявка")

	rec := postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("чтение %s: %v", p, err)
	}
	if strings.Contains(string(raw), metadata.StandardCodeFieldID) {
		t.Fatalf("«Код» документа получил id справочного «Кода»:\n%s", raw)
	}
	// А «Номер» документа — стандартный, засев ему полагается.
	assertStandardFieldSaved(t, p, metadata.KindDocument, metadata.StandardNumberField, metadata.StandardNumberFieldID)
}

// assertStandardFieldSaved проверяет главное следствие фикса: стандартное поле
// уехало в YAML с устойчивым id, дубля не возникло, и повторная загрузка через
// metadata видит ровно одно поле с тем же id — то есть миграция не увидит
// коллизии.
func assertStandardFieldSaved(t *testing.T, path string, kind metadata.Kind, fieldName, wantID string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s: %v", path, err)
	}
	// Загрузка идёт тем же LoadFile, которым объект читает платформа: именно он
	// синтезирует стандартное поле, и именно его результат ложится в основу
	// плана миграции.
	ent, err := metadata.LoadFile(path, kind)
	if err != nil {
		t.Fatalf("повторная загрузка %s: %v\n%s", path, err, raw)
	}
	var seen []metadata.Field
	for _, f := range ent.Fields {
		if strings.EqualFold(f.Name, fieldName) {
			seen = append(seen, f)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("поле «%s» встречается %d раз(а), ожидалось одно:\n%s", fieldName, len(seen), raw)
	}
	if seen[0].ID != wantID {
		t.Fatalf("поле «%s» получило id %q вместо %q:\n%s", fieldName, seen[0].ID, wantID, raw)
	}
}
