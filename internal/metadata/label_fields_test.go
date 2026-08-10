package metadata

import "testing"

// Представление объекта выбиралось по ПОЗИЦИИ — первый строковый реквизит, —
// а порядок реквизитов задают пользователь и конвертер выгрузки 1С. Конвертер
// кладёт «Код» перед «Наименованием», поэтому у каждой импортированной из 1С
// конфигурации в пикерах ссылок, списках и глобальном поиске показывался код,
// вопреки 1С, где основное представление по умолчанию — наименование
// (план 117, решение №3).

func fieldsOf(names ...string) []Field {
	out := make([]Field, 0, len(names))
	for _, n := range names {
		out = append(out, Field{Name: n, Type: FieldTypeString})
	}
	return out
}

func TestLabelFields_ПредпочитаетНаименованиеПередКодом(t *testing.T) {
	e := &Entity{Name: "Номенклатура", Kind: KindCatalog, Fields: fieldsOf("Код", "Наименование", "Артикул")}
	got := LabelFields(e)
	if len(got) == 0 || got[0].Name != "Наименование" {
		t.Fatalf("представление = %v, ожидалось Наименование первым", namesOfFields(got))
	}
	// Код не выбрасывается — он остаётся кандидатом, но последним.
	if got[len(got)-1].Name != "Код" {
		t.Errorf("Код должен быть последним кандидатом: %v", namesOfFields(got))
	}
}

// Порядок предпочтения задаёт список имён, а не YAML: Description раньше Имени,
// даже если в файле он объявлен позже.
func TestLabelFields_ПорядокПредпочтенияНеЗависитОтYAML(t *testing.T) {
	e := &Entity{Name: "X", Kind: KindCatalog, Fields: fieldsOf("Имя", "Description")}
	if got := LabelFields(e); len(got) == 0 || got[0].Name != "Description" {
		t.Errorf("представление = %v, ожидался Description", namesOfFields(got))
	}
}

// Нет канонических имён — берём первый строковый по порядку объявления, как и
// раньше: правило сужает поведение только там, где выбор очевиден.
func TestLabelFields_БезКанонических_ПервыйСтроковый(t *testing.T) {
	e := &Entity{Name: "X", Kind: KindCatalog, Fields: fieldsOf("Артикул", "Описание")}
	if got := LabelFields(e); len(got) == 0 || got[0].Name != "Артикул" {
		t.Errorf("представление = %v, ожидался Артикул", namesOfFields(got))
	}
}

// У документа «Номер» предпочтительнее произвольного строкового реквизита.
func TestLabelFields_ДокументПредпочитаетНомер(t *testing.T) {
	e := &Entity{Name: "Реализация", Kind: KindDocument, Fields: fieldsOf("Комментарий", "Номер")}
	if got := LabelFields(e); len(got) == 0 || got[0].Name != "Номер" {
		t.Errorf("представление = %v, ожидался Номер", namesOfFields(got))
	}
}

// RowLabel — то, что реально видит пользователь вместо UUID.
func TestRowLabel_ИмпортИз1С_ПоказываетНаименованиеАНеКод(t *testing.T) {
	e := &Entity{Name: "Контрагенты", Kind: KindCatalog, Fields: fieldsOf("Код", "Наименование")}
	row := map[string]any{"Код": "К-000007", "Наименование": "ООО Ромашка", "id": "u"}
	if got := RowLabel(row, e); got != "ООО Ромашка" {
		t.Errorf("RowLabel = %q, ожидалось \"ООО Ромашка\"", got)
	}
}

// Если наименование пустое, код остаётся разумным запасным вариантом — лучше,
// чем голый UUID.
func TestRowLabel_ПустоеНаименование_ПадаетНаКод(t *testing.T) {
	e := &Entity{Name: "Контрагенты", Kind: KindCatalog, Fields: fieldsOf("Код", "Наименование")}
	row := map[string]any{"Код": "К-000007", "id": "u"}
	if got := RowLabel(row, e); got != "К-000007" {
		t.Errorf("RowLabel = %q, ожидалось \"К-000007\"", got)
	}
}

func namesOfFields(fs []Field) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}
