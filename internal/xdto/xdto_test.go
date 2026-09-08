package xdto_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/xdto"
)

func catalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Должности",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ДатаВвода", Type: metadata.FieldTypeDate},
			{Name: "ВведенаВШтатноеРасписание", Type: metadata.FieldTypeBool},
			{Name: "ПроцентНадбавки", Type: metadata.FieldTypeNumber},
			{Name: "ТарифнаяСетка", Type: metadata.FieldTypeString, RefEntity: "ТарифныеСетки"},
		},
	}
}

func document() *metadata.Entity {
	return &metadata.Entity{
		Name: "БольничныйЛист",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Сотрудник", Type: metadata.FieldTypeString, RefEntity: "Сотрудники"},
		},
		TableParts: []metadata.TablePart{{
			Name: "Начисления",
			Fields: []metadata.Field{
				{Name: "Результат", Type: metadata.FieldTypeNumber},
				{Name: "ОтработаноДней", Type: metadata.FieldTypeNumber},
				{Name: "ДатаНачала", Type: metadata.FieldTypeDate},
			},
		}},
	}
}

// Эталон снят с живой 1С (ЗУП 3.1, платформа 8.5.1) вызовом
// СериализаторXDTO.ЗаписатьXML: корень с тремя пространствами имён, стандартные
// реквизиты английскими именами, ссылка голым UUID, незаполненная дата
// значением, а не пустым тегом.
func TestWriteCatalogMatchesLiveFormat(t *testing.T) {
	ent := catalog()
	obj := runtime.NewObject(ent.Name, ent.Kind)
	obj.ID = uuid.MustParse("52bb35d6-457d-11e0-94a1-00241dd1a434")
	obj.Set("Наименование", "слесарь-жестянщик")
	obj.Set("Код", "000000188")
	obj.Set("ДатаВвода", time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC))
	obj.Set("ВведенаВШтатноеРасписание", true)
	obj.Set("ПроцентНадбавки", "0")

	got, err := xdto.Write(ent, obj, xdto.Options{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := `<CatalogObject.Должности xmlns="http://v8.1c.ru/8.1/data/enterprise/current-config" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
	<Ref>52bb35d6-457d-11e0-94a1-00241dd1a434</Ref>
	<DeletionMark>false</DeletionMark>
	<Description>слесарь-жестянщик</Description>
	<Code>000000188</Code>
	<ДатаВвода>2010-01-01T00:00:00</ДатаВвода>
	<ВведенаВШтатноеРасписание>true</ВведенаВШтатноеРасписание>
	<ПроцентНадбавки>0</ПроцентНадбавки>
	<ТарифнаяСетка>00000000-0000-0000-0000-000000000000</ТарифнаяСетка>
</CatalogObject.Должности>`
	if got != want {
		t.Fatalf("формат разошёлся с эталоном 1С\nполучено:\n%s\n\nожидалось:\n%s", got, want)
	}
}

func TestWriteDocumentWithTablePart(t *testing.T) {
	ent := document()
	obj := runtime.NewObject(ent.Name, ent.Kind)
	obj.EnsureTableParts(ent)
	obj.ID = uuid.MustParse("97b437cf-ee79-11e6-8144-00155d0a6606")
	obj.Set("Дата", time.Date(2017, 1, 9, 8, 51, 18, 0, time.UTC))
	obj.Set("Номер", "02 - П")
	obj.TablePartRows["Начисления"] = []map[string]any{
		{"Результат": "599.07", "ОтработаноДней": "3", "ДатаНачала": time.Date(2017, 5, 10, 0, 0, 0, 0, time.UTC)},
		{"Результат": "0", "ОтработаноДней": "0"},
	}

	got, err := xdto.Write(ent, obj, xdto.Options{Posted: true})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := `<DocumentObject.БольничныйЛист xmlns="http://v8.1c.ru/8.1/data/enterprise/current-config" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
	<Ref>97b437cf-ee79-11e6-8144-00155d0a6606</Ref>
	<DeletionMark>false</DeletionMark>
	<Date>2017-01-09T08:51:18</Date>
	<Number>02 - П</Number>
	<Posted>true</Posted>
	<Сотрудник>00000000-0000-0000-0000-000000000000</Сотрудник>
	<Начисления>
		<Результат>599.07</Результат>
		<ОтработаноДней>3</ОтработаноДней>
		<ДатаНачала>2017-05-10T00:00:00</ДатаНачала>
	</Начисления>
	<Начисления>
		<Результат>0</Результат>
		<ОтработаноДней>0</ОтработаноДней>
		<ДатаНачала>0001-01-01T00:00:00</ДатаНачала>
	</Начисления>
</DocumentObject.БольничныйЛист>`
	if got != want {
		t.Fatalf("документ разошёлся с эталоном\nполучено:\n%s\n\nожидалось:\n%s", got, want)
	}
}

// Незаполненные значения 1С пишет по-разному в зависимости от типа: дата и
// ссылка — значением, строка и число — по-своему. Приёмник различает «пусто» и
// «нет поля», поэтому опускать реквизиты нельзя.
func TestEmptyValuesFollowTypeRules(t *testing.T) {
	ent := catalog()
	obj := runtime.NewObject(ent.Name, ent.Kind)

	got, err := xdto.Write(ent, obj, xdto.Options{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Ref у нового объекта не нулевой: платформа выдаёт id сразу при создании,
	// до записи. Проверяем прочие типы.
	for _, want := range []string{
		"<Description/>",
		"<Code/>",
		"<ДатаВвода>0001-01-01T00:00:00</ДатаВвода>",
		"<ВведенаВШтатноеРасписание>false</ВведенаВШтатноеРасписание>",
		"<ПроцентНадбавки>0</ПроцентНадбавки>",
		"<ТарифнаяСетка>00000000-0000-0000-0000-000000000000</ТарифнаяСетка>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("пустой объект: нет %q\nполучено:\n%s", want, got)
		}
	}
}

func TestReadRestoresObject(t *testing.T) {
	ent := document()
	src := runtime.NewObject(ent.Name, ent.Kind)
	src.EnsureTableParts(ent)
	src.ID = uuid.MustParse("97b437cf-ee79-11e6-8144-00155d0a6606")
	src.Set("Дата", time.Date(2017, 1, 9, 8, 51, 18, 0, time.UTC))
	src.Set("Номер", "02 - П")
	src.Set("Сотрудник", "0c1eba7e-53ba-11e6-8129-00155d0a6606")
	src.TablePartRows["Начисления"] = []map[string]any{
		{"Результат": "599.07", "ОтработаноДней": "3", "ДатаНачала": time.Date(2017, 5, 10, 0, 0, 0, 0, time.UTC)},
	}

	text, err := xdto.Write(ent, src, xdto.Options{Posted: true})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, opts, err := xdto.Read(text, func(name string) *metadata.Entity {
		if name == ent.Name {
			return ent
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ID != src.ID {
		t.Errorf("Ref: получено %v, ожидалось %v", got.ID, src.ID)
	}
	if !opts.Posted {
		t.Error("Posted потерян при разборе")
	}
	if got.Get("Номер") != "02 - П" {
		t.Errorf("Номер: %v", got.Get("Номер"))
	}
	if got.Get("Сотрудник") != "0c1eba7e-53ba-11e6-8129-00155d0a6606" {
		t.Errorf("ссылка: %v", got.Get("Сотрудник"))
	}
	date, ok := got.Get("Дата").(time.Time)
	if !ok || !date.Equal(time.Date(2017, 1, 9, 8, 51, 18, 0, time.UTC)) {
		t.Errorf("Дата: %v", got.Get("Дата"))
	}
	rows := got.TablePartRows["Начисления"]
	if len(rows) != 1 {
		t.Fatalf("строк ТЧ: %d, ожидалась 1", len(rows))
	}
	if rows[0]["Результат"] != "599.07" {
		t.Errorf("строка ТЧ: %v", rows[0])
	}
}

// Пустая ссылка и пустая дата должны вернуться пустыми, а не строкой из нулей:
// иначе объект после круга «записал → прочитал» ссылается на несуществующую
// запись с нулевым UUID.
func TestReadEmptyValuesComeBackEmpty(t *testing.T) {
	ent := catalog()
	text, err := xdto.Write(ent, runtime.NewObject(ent.Name, ent.Kind), xdto.Options{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _, err := xdto.Read(text, func(string) *metadata.Entity { return ent })
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v := got.Get("ТарифнаяСетка"); v != nil && v != "" {
		t.Errorf("пустая ссылка вернулась как %#v", v)
	}
	if v := got.Get("ДатаВвода"); v != nil {
		if t2, ok := v.(time.Time); !ok || !t2.IsZero() {
			t.Errorf("пустая дата вернулась как %#v", v)
		}
	}
}

func TestReadRejectsUnknownRootAndEntity(t *testing.T) {
	ent := catalog()
	if _, _, err := xdto.Read(`<Обмен><Что/></Обмен>`, func(string) *metadata.Entity { return ent }); err == nil {
		t.Error("чужой корень принят без ошибки")
	}
	if _, _, err := xdto.Read(`<CatalogObject.Нет><Ref/></CatalogObject.Нет>`, func(string) *metadata.Entity { return nil }); err == nil {
		t.Error("объект вне конфигурации принят без ошибки")
	}
	text, _ := xdto.Write(ent, runtime.NewObject(ent.Name, ent.Kind), xdto.Options{})
	doc := document()
	if _, _, err := xdto.Read(text, func(string) *metadata.Entity { return doc }); err == nil {
		t.Error("расхождение вида объекта принято без ошибки")
	}
}

// Спецсимволы обязаны экранироваться: наименование с «&» или «<» иначе рвёт
// разбор всего документа у приёмника.
func TestWriteEscapesSpecialCharacters(t *testing.T) {
	ent := catalog()
	obj := runtime.NewObject(ent.Name, ent.Kind)
	obj.Set("Наименование", `Иванов & <Сын>`)

	text, err := xdto.Write(ent, obj, xdto.Options{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if strings.Contains(text, "<Сын>") {
		t.Errorf("спецсимволы не экранированы:\n%s", text)
	}
	got, _, err := xdto.Read(text, func(string) *metadata.Entity { return ent })
	if err != nil {
		t.Fatalf("Read после экранирования: %v", err)
	}
	if got.Get("Наименование") != `Иванов & <Сын>` {
		t.Errorf("после круга: %v", got.Get("Наименование"))
	}
}
