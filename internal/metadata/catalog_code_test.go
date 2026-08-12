package metadata

// Стандартный «Код» справочника (план 117B, issue #658).
//
// Дефект из заявки: блок `numerator:` у справочника ПАРСИЛСЯ И МОЛЧА НИЧЕГО НЕ
// ДЕЛАЛ — объявил, а кода нет и автонумерации нет. Тот же класс тихого
// обещания, что мы чинили весь день.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCatalog(t *testing.T, body string) *Entity {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "контрагенты.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	return e
}

// numerator: у справочника синтезирует «Код» — как numerator: у документа
// синтезирует «Номер».
func TestCatalogNumerator_SynthesizesCode(t *testing.T) {
	e := writeCatalog(t, `name: Контрагенты
numerator:
  prefix: "К-"
  length: 6
fields:
  - {name: Наименование, type: string}
`)
	var code *Field
	for i := range e.Fields {
		if e.Fields[i].Name == StandardCodeField {
			code = &e.Fields[i]
		}
	}
	if code == nil {
		t.Fatal("«Код» не синтезирован — numerator у справочника снова ничего не делает")
	}
	if code.Type != FieldTypeString {
		t.Errorf("тип «Кода» = %s, ожидалась строка", code.Type)
	}
	if code.ID != StandardCodeFieldID {
		t.Errorf("у «Кода» нет устойчивого ID: %q", code.ID)
	}
	if e.Fields[0].Name != StandardCodeField {
		t.Errorf("«Код» должен идти первым, порядок: %s", e.Fields[0].Name)
	}
}

// Явно объявленный «Код» не подменяется: авторская метаданность важнее синтеза.
func TestCatalogNumerator_KeepsExplicitCode(t *testing.T) {
	e := writeCatalog(t, `name: Контрагенты
numerator: {prefix: "К-"}
fields:
  - {name: Код, type: string, title: Артикул}
  - {name: Наименование, type: string}
`)
	n := 0
	for _, f := range e.Fields {
		if f.Name == StandardCodeField {
			n++
			if f.Title != "Артикул" {
				t.Errorf("явный «Код» подменён синтезированным: title=%q", f.Title)
			}
		}
	}
	if n != 1 {
		t.Errorf("«Код» объявлен %d раз, ожидался один", n)
	}
}

// Без numerator: справочник остаётся как был — 117B не меняет существующие
// конфигурации.
func TestCatalogWithoutNumerator_Unchanged(t *testing.T) {
	e := writeCatalog(t, `name: Контрагенты
fields:
  - {name: Наименование, type: string}
`)
	for _, f := range e.Fields {
		if f.Name == StandardCodeField {
			t.Fatal("«Код» появился без numerator:")
		}
	}
}

// Период по умолчанию зависит от вида: документ — год, справочник — none.
// Сброс счётчика у справочника выдал бы уже занятый код.
func TestNumeratorPeriodDefaultByKind(t *testing.T) {
	cat := writeCatalog(t, "name: Контрагенты\nnumerator: {prefix: \"К-\"}\nfields:\n  - {name: Наименование, type: string}\n")
	if cat.Numerator.Period != "none" {
		t.Errorf("период справочника = %q, ожидался none", cat.Numerator.Period)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "заказ.yaml")
	if err := os.WriteFile(path, []byte("name: Заказ\nnumerator: {prefix: \"З-\"}\nfields:\n  - {name: Сумма, type: number}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadFile(path, KindDocument)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Numerator.Period != "year" {
		t.Errorf("период документа = %q, ожидался year", doc.Numerator.Period)
	}
}

// Новые ключи блока читаются: без них 117D и 117E не к чему прицепить.
func TestNumeratorNewKeysParsed(t *testing.T) {
	e := writeCatalog(t, `name: Контрагенты
numerator:
  prefix: "К-"
  base_prefix: true
  unique: true
fields:
  - {name: Наименование, type: string}
`)
	if !e.Numerator.BasePrefix || !e.Numerator.Unique {
		t.Errorf("base_prefix/unique не разобраны: %+v", e.Numerator)
	}
}

// Валидация блока: сброс у справочника, чужой scope и подмена типа «Кода».
func TestValidateNumerator(t *testing.T) {
	base := func(mut func(*Entity)) []*Entity {
		e := &Entity{
			Name: "Контрагенты", Kind: KindCatalog,
			Fields:    []Field{{Name: "Код", Type: FieldTypeString}, {Name: "Наименование", Type: FieldTypeString}},
			Numerator: &Numerator{Prefix: "К-", Period: "none"},
		}
		mut(e)
		// Организации нужны, чтобы ссылка в кейсе «Код ссылкой» доходила до
		// нашей проверки, а не отсекалась раньше как неизвестная сущность.
		return []*Entity{{Name: "Организации", Kind: KindCatalog,
			Fields: []Field{{Name: "Наименование", Type: FieldTypeString}}}, e}
	}
	cases := []struct {
		name string
		mut  func(*Entity)
		want string
	}{
		{"валидный", func(e *Entity) {}, ""},
		{"сброс у справочника", func(e *Entity) { e.Numerator.Period = "year" }, "уже занятый код"},
		{"неизвестный период", func(e *Entity) { e.Numerator.Period = "квартал" }, "допустимы none"},
		{"чужой scope", func(e *Entity) { e.Numerator.Scope = "Нет" }, "неизвестный реквизит"},
		{"Код числом", func(e *Entity) { e.Fields[0].Type = FieldTypeNumber }, "обязан быть строкой"},
		{"Код ссылкой", func(e *Entity) { e.Fields[0].RefEntity = "Организации" }, "не может быть ссылкой"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(base(c.mut), nil)
			if c.want == "" {
				if err != nil {
					t.Fatalf("ожидалось без ошибки: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ожидалась ошибка про %q, получено: %v", c.want, err)
			}
		})
	}
}

// Регрессия: идентификатор синтезированного «Кода» обязан проходить валидацию
// ID. С точкой («std.code») ЛЮБОЙ справочник с numerator: переставал грузиться —
// project.Load падал ещё до всякой работы. Поймано тестом команды renumber,
// закреплено здесь.
func TestCatalogCode_IDPassesValidation(t *testing.T) {
	e := writeCatalog(t, `name: Контрагенты
numerator: {prefix: "К-", length: 6}
fields:
  - {name: Наименование, type: string}
`)
	if err := Validate([]*Entity{e}, nil); err != nil {
		t.Fatalf("справочник с numerator: не проходит валидацию: %v", err)
	}
}
