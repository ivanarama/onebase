package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func filtersCheckFixture() *project.Project {
	задача := &metadata.Entity{
		Name: "Задача", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Тема", Type: metadata.FieldTypeString}},
	}
	пользователи := &metadata.Entity{
		Name: "Пользователи", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	return &project.Project{
		Entities: []*metadata.Entity{задача, пользователи},
		Widgets: []*metadata.Widget{
			{
				Name: "Хорошо", Type: metadata.WidgetTypeList,
				Query:  "ВЫБРАТЬ Ссылка, Тема ИЗ Документ.Задача ГДЕ (&Тема ЕСТЬ ПУСТО ИЛИ Тема = &Тема) И (&Исполнитель ЕСТЬ ПУСТО ИЛИ Исполнитель = &Исполнитель) И (&Статус ЕСТЬ ПУСТО ИЛИ Статус = &Статус) И (&Срочно ЕСТЬ ПУСТО ИЛИ Срочно = &Срочно)",
				Params: map[string]string{"Начало": "{{today}}"},
				Filters: []metadata.WidgetFilter{
					{Name: "Тема", Type: "string", Param: "Тема"},
					{Name: "Исполнитель", Type: "reference:Пользователи", Param: "Исполнитель"},
					{Name: "Статус", Type: "select", Param: "Статус", Values: []metadata.WidgetFilterValue{{Value: "open"}, {Value: "closed"}}, Default: "open"},
					{Name: "Срочно", Type: "bool", Param: "Срочно", Default: false},
				},
			},
			{Name: "НеСписок", Type: metadata.WidgetTypeKPI, Query: "ВЫБРАТЬ 0",
				Filters: []metadata.WidgetFilter{{Name: "Тема", Type: "string", Param: "Тема"}}},
			{Name: "НетПараметра", Type: metadata.WidgetTypeList, Query: "ВЫБРАТЬ Тема ИЗ Документ.Задача",
				Filters: []metadata.WidgetFilter{{Name: "Тема", Type: "string", Param: "Чужой"}}},
			{Name: "КонфликтСтатики", Type: metadata.WidgetTypeList,
				Query:   "ВЫБРАТЬ Тема ИЗ Документ.Задача ГДЕ &Тема",
				Params:  map[string]string{"Тема": "x"},
				Filters: []metadata.WidgetFilter{{Name: "Тема2", Type: "string", Param: "Тема"}}},
			{Name: "ПлохойТип", Type: metadata.WidgetTypeList, Query: "ВЫБРАТЬ Тема ИЗ Документ.Задача ГДЕ &X",
				Filters: []metadata.WidgetFilter{{Name: "X", Type: "magic", Param: "X"}}},
			{Name: "НетСущность", Type: metadata.WidgetTypeList, Query: "ВЫБРАТЬ Тема ИЗ Документ.Задача ГДЕ &Кто",
				Filters: []metadata.WidgetFilter{{Name: "Кто", Type: "reference:НеЕсть", Param: "Кто"}}},
			{Name: "ПустойСелект", Type: metadata.WidgetTypeList, Query: "ВЫБРАТЬ Тема ИЗ Документ.Задача ГДЕ &S",
				Filters: []metadata.WidgetFilter{{Name: "S", Type: "select", Param: "S"}}},
			{Name: "ПлохоеУмолчание", Type: metadata.WidgetTypeList, Query: "ВЫБРАТЬ Тема ИЗ Документ.Задача ГДЕ &Д",
				Filters: []metadata.WidgetFilter{{Name: "Д", Type: "date", Param: "Д", Default: "01.02.2026"}}},
		},
	}
}

func TestCheckWidgetFilters(t *testing.T) {
	byObject := map[string][]string{}
	for _, is := range CheckWidgetFilters(filtersCheckFixture()) {
		byObject[is.Object] = append(byObject[is.Object], is.Message)
	}
	if msgs, ok := byObject["Хорошо"]; ok {
		t.Fatalf("valid widget reported: %v", msgs)
	}
	expect := map[string]string{
		"НеСписок":        "list",
		"НетПараметра":    "не встречается в запросе",
		"КонфликтСтатики": "совпадает со статическим",
		"ПлохойТип":       "неизвестный тип",
		"НетСущность":     "не найдена",
		"ПустойСелект":    "select требует",
		"ПлохоеУмолчание": "не является датой",
	}
	for object, substr := range expect {
		msgs, ok := byObject[object]
		if !ok {
			t.Fatalf("%s: expected issue, got none (all: %v)", object, byObject)
		}
		if !strings.Contains(strings.Join(msgs, " | "), substr) {
			t.Fatalf("%s: messages %v miss %q", object, msgs, substr)
		}
	}
}
