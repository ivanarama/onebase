package metadata

import "testing"

// Блок `search_fields:` задаёт состав реквизитов для поиска подстроки в списке
// и в подборе ссылки. Умолчание (ключа нет) обязано совпадать с историческим
// поведением: все строковые реквизиты шапки.

func searchTestEntity(search []string, set bool) *Entity {
	return &Entity{
		Name: "Номенклатура",
		Kind: KindCatalog,
		Fields: []Field{
			{Name: "Наименование", Type: FieldTypeString},
			{Name: "Артикул", Type: FieldTypeNumber},
			{Name: "Поставщик", Type: "reference:Контрагент", RefEntity: "Контрагент"},
		},
		Search:    search,
		SearchSet: set,
	}
}

func fieldNames(fs []Field) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}

func TestSearchFields_УмолчаниеВсеСтроковые(t *testing.T) {
	got := fieldNames(SearchFields(searchTestEntity(nil, false)))
	if len(got) != 1 || got[0] != "Наименование" {
		t.Errorf("умолчание должно давать строковые реквизиты, получено %v", got)
	}
}

func TestSearchFields_ЯвныйСписокРазрешаетЧисловой(t *testing.T) {
	got := fieldNames(SearchFields(searchTestEntity([]string{"Артикул"}, true)))
	if len(got) != 1 || got[0] != "Артикул" {
		t.Errorf("явный список должен пускать числовой реквизит, получено %v", got)
	}
}

func TestSearchFields_ПустойСписокОтличимОтОтсутствия(t *testing.T) {
	if got := SearchFields(searchTestEntity([]string{}, true)); len(got) != 0 {
		t.Errorf("`search_fields: []` — поиск выключен, получено %v", fieldNames(got))
	}
}

// Регистронезависимость: метаданные и DSL регистр не различают, поэтому
// `search_fields: [наименование]` обязан находить реквизит «Наименование».
func TestSearchFields_ИмяБезУчётаРегистра(t *testing.T) {
	got := fieldNames(SearchFields(searchTestEntity([]string{"наименование"}, true)))
	if len(got) != 1 || got[0] != "Наименование" {
		t.Errorf("имя реквизита должно искаться без учёта регистра, получено %v", got)
	}
}

func TestValidateSearchFields_НеизвестныйРеквизит(t *testing.T) {
	err := validateSearchFields(searchTestEntity([]string{"Штрихкод"}, true))
	if err == nil {
		t.Fatal("несуществующий реквизит в search_fields должен быть ошибкой конфигурации")
	}
}

// Ссылка отклоняется: в колонке UUID, поиск подстроки по нему всегда пуст.
// Пропустить её молча значит оставить автора с неработающим поиском.
func TestValidateSearchFields_СсылкаОтклоняется(t *testing.T) {
	err := validateSearchFields(searchTestEntity([]string{"Поставщик"}, true))
	if err == nil {
		t.Fatal("ссылочный реквизит в search_fields должен быть ошибкой")
	}
}

func TestValidateSearchFields_Дубль(t *testing.T) {
	err := validateSearchFields(searchTestEntity([]string{"Артикул", "артикул"}, true))
	if err == nil {
		t.Fatal("повтор реквизита в search_fields должен быть ошибкой")
	}
}

func TestValidateSearchFields_БезБлокаНеПроверяется(t *testing.T) {
	if err := validateSearchFields(searchTestEntity(nil, false)); err != nil {
		t.Fatalf("без блока search_fields проверять нечего: %v", err)
	}
}
