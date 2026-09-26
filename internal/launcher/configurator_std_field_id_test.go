package launcher

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
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

// «Код» справочника с блоком numerator платформа синтезирует при загрузке.
// Конфигуратор редактирует его свойства в секции нумерации и не пишет строку
// в fields. Раньше сохранение выдавало ему свежий f_xxxx и оставляло второй
// источник истины рядом с numerator (#1161, #1358).
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
	// Стандартное поле теперь редактируется в нумерации, а не в общем списке.
	if formHasFieldNamed(form, metadata.StandardCodeField) || form.Get("numerator_field_present") != "1" {
		t.Fatalf("Код не вынесен в секцию нумерации: %v", form)
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

func TestNumberedEntityWithoutOrdinaryFieldsCanAddField(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	writeCfgFile(t, cfgDir, "catalogs", "Пустой.yaml", "name: Пустой\nnumerator: {length: 6}\n")
	b, err := h.store.Get("test")
	if err != nil {
		t.Fatal(err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatal(data.Error)
	}
	html := renderCfgTree(t, data)
	if !strings.Contains(html, `id="ft-Пустой"`) || !strings.Contains(html, `cfgAddField('ft-Пустой'`) {
		t.Fatalf("numbered entity with only a standard field lost its add-field control")
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
	if formHasFieldNamed(form, metadata.StandardNumberField) || form.Get("numerator_field_present") != "1" {
		t.Fatalf("Номер не вынесен в секцию нумерации: %v", form)
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

func TestSaveFields_LegacyCodePropertiesMoveToNumerator(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	path := writeCfgFile(t, cfgDir, "catalogs", "Клиенты.yaml", `name: Клиенты
numerator:
  length: 7
  period: none
fields:
  - id: f_legacy
    name: Код
    type: string
    label: Код клиента
    titles: {en: Customer code}
    required: true
    default: PREFIX
    pii: true
  - {name: Наименование, type: string}
`)
	b, err := h.store.Get("test")
	if err != nil {
		t.Fatal(err)
	}
	data := h.loadCfgData(context.Background(), b, "tree")
	if data.Error != "" {
		t.Fatal(data.Error)
	}
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "Клиенты")
	if formHasFieldNamed(form, "Код") || form.Get("numerator_field_title") != "Код клиента" {
		t.Fatalf("legacy Code not represented in numerator editor: %v", form)
	}
	form.Set("new_field.1.name", "Телефон")
	form.Set("new_field.1.type", "string")
	if ok, errText := cfgResponse(t, postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)); !ok {
		t.Fatal(errText)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved saveEntity
	if err := yaml.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Numerator == nil || saved.Numerator.Length != 7 || saved.Numerator.Field == nil {
		t.Fatalf("numerator lost: %+v", saved.Numerator)
	}
	f := saved.Numerator.Field
	if f.Title != "" || f.Label != "Код клиента" || f.Titles["en"] != "Customer code" || !f.Required || f.Default != "PREFIX" || !f.PII {
		t.Fatalf("legacy properties lost: %+v", f)
	}
	assertStandardFieldSaved(t, path, metadata.KindCatalog, metadata.StandardCodeField, metadata.StandardCodeFieldID)
	data = h.loadCfgData(context.Background(), b, "tree")
	form = browserSubmitForEntity(t, renderCfgTree(t, data), "Клиенты")
	if ok, errText := cfgResponse(t, postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)); !ok {
		t.Fatal(errText)
	}
	again, err := os.ReadFile(path)
	if err != nil || string(again) != string(raw) {
		t.Fatalf("second save changed canonical YAML: %v\nfirst:\n%s\nsecond:\n%s", err, raw, again)
	}
	form.Set("numerator_field_title", "Код договора")
	if ok, errText := cfgResponse(t, postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)); !ok {
		t.Fatal(errText)
	}
	changed, err := os.ReadFile(path)
	saved = saveEntity{}
	if err != nil || yaml.Unmarshal(changed, &saved) != nil || saved.Numerator.Field.Title != "Код договора" || saved.Numerator.Field.Label != "" {
		t.Fatalf("edited title did not supersede legacy label: %v\n%s", err, changed)
	}
}

func TestSaveFields_DatabaseModeMovesLegacyCode(t *testing.T) {
	h := dbBackedBase(t, "standardfielddb")
	ctx := context.Background()
	b, err := h.store.Get("standardfielddb")
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenDB(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	repo := configdb.New(db)
	if err := repo.SaveFiles(ctx, []configdb.ConfigFile{
		{Path: "config/app.yaml", Content: []byte("name: Test\n")},
		{Path: "catalogs/Клиенты.yaml", Content: []byte("name: Клиенты\nnumerator:\n  length: 6\nfields:\n  - {id: f_old, name: Код, type: string, title: Customer code}\n  - {name: Наименование, type: string}\n")},
	}, configdb.VersionOptions{Message: "seed"}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	data := h.loadCfgData(ctx, b, "tree")
	if data.Error != "" {
		t.Fatal(data.Error)
	}
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "Клиенты")
	if ok, errText := cfgResponse(t, postCfg(t, b.ID, "/bases/"+b.ID+"/configurator/fields", form, h.configuratorSaveFields)); !ok {
		t.Fatal(errText)
	}
	db, err = OpenDB(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, found, err := configdb.New(db).ReadFile(ctx, "catalogs/Клиенты.yaml")
	if err != nil || !found {
		t.Fatalf("ReadFile: found=%v err=%v", found, err)
	}
	var saved saveEntity
	if err := yaml.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Numerator == nil || saved.Numerator.Field == nil || saved.Numerator.Field.Title != "Customer code" {
		t.Fatalf("database round-trip lost standard properties: %s", raw)
	}
	for _, f := range saved.Fields {
		if f.Name == "Код" {
			t.Fatalf("database round-trip retained duplicate Code: %s", raw)
		}
	}
}

// Снятие автонумерации материализует «Код» в fields как обычный реквизит с
// прежним std_code: колонка и её данные не меняются.
func TestSaveFields_CodeKeepsStandardIDWhenNumeratorTurnedOff(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "catalogs", "Ученики.yaml", `name: Ученики
numerator:
    length: 8
    field:
        title: Код ученика
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

	// Включение обратно поглощает материализованный реквизит, не меняя ID
	// и введённую пользователем подпись.
	again := h.loadCfgData(context.Background(), b, "tree")
	if again.Error != "" {
		t.Fatalf("повторная загрузка: %s", again.Error)
	}
	form = browserSubmitForEntity(t, renderCfgTree(t, again), "Ученики")
	if form.Get("numerator_field_title") != "Код ученика" {
		t.Fatalf("подпись стандартного поля потеряна: %v", form)
	}
	form.Set("numerator_enabled", "1")
	rec = postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("повторное включение не удалось: %s", errText)
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
	// А «Номер» документа — стандартный, с каноническим ID.
	assertStandardFieldSaved(t, p, metadata.KindDocument, metadata.StandardNumberField, metadata.StandardNumberFieldID)
}

// assertStandardFieldSaved проверяет обе стороны нового контракта: при активной
// нумерации стандартной строки в fields нет, но LoadFile видит её с std_* ID.
// После отключения нумерации та же строка материализуется с прежним ID.
func assertStandardFieldSaved(t *testing.T, path string, kind metadata.Kind, fieldName, wantID string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s: %v", path, err)
	}
	var saved struct {
		Numerator *saveNumerator `yaml:"numerator"`
		Fields    []struct {
			Name string `yaml:"name"`
			ID   string `yaml:"id"`
		} `yaml:"fields"`
	}
	if err := yaml.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, f := range saved.Fields {
		if strings.EqualFold(f.Name, fieldName) {
			rows++
			if saved.Numerator == nil && f.ID != wantID {
				t.Fatalf("материализованное поле получило ID %q вместо %q", f.ID, wantID)
			}
		}
	}
	if saved.Numerator != nil && rows != 0 || saved.Numerator == nil && rows != 1 {
		t.Fatalf("стандартное поле представлено в fields %d раз при numerator=%v:\n%s", rows, saved.Numerator != nil, raw)
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
