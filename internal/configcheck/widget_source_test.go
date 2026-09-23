package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func sourceFixture() *project.Project {
	tovar := &metadata.Entity{
		Name: "Товар", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	list := func(name, query string, source *metadata.WidgetSource) *metadata.Widget {
		return &metadata.Widget{Name: name, Type: metadata.WidgetTypeList, Query: query, Source: source}
	}
	return &project.Project{
		Entities: []*metadata.Entity{tovar},
		Widgets: []*metadata.Widget{
			list("Хорошо", "ВЫБРАТЬ Ссылка, Наименование ИЗ Справочник.Товар",
				&metadata.WidgetSource{Entity: "Товар", IDField: "Ссылка"}),
			{Name: "НеСписок", Type: metadata.WidgetTypeKPI, Query: "ВЫБРАТЬ Ссылка ИЗ Справочник.Товар",
				Source: &metadata.WidgetSource{Entity: "Товар", IDField: "Ссылка"}},
			list("НетСущности", "ВЫБРАТЬ Ссылка ИЗ Справочник.Товар",
				&metadata.WidgetSource{Entity: "Контрагент", IDField: "Ссылка"}),
			list("ПустойIDField", "ВЫБРАТЬ Ссылка ИЗ Справочник.Товар",
				&metadata.WidgetSource{Entity: "Товар"}),
			list("НеСсылка", "ВЫБРАТЬ Наименование ИЗ Справочник.Товар",
				&metadata.WidgetSource{Entity: "Товар", IDField: "Наименование"}),
			list("БезSource", "ВЫБРАТЬ Наименование ИЗ Справочник.Товар", nil),
		},
	}
}

func sourceIssuesByObject(issues []Issue) map[string][]string {
	out := map[string][]string{}
	for _, is := range issues {
		out[is.Object] = append(out[is.Object], is.Message)
	}
	return out
}

func TestCheckWidgetSource(t *testing.T) {
	byObject := sourceIssuesByObject(CheckWidgetSource(sourceFixture()))

	if _, ok := byObject["Хорошо"]; ok {
		t.Fatalf("valid widget reported: %v", byObject["Хорошо"])
	}
	if _, ok := byObject["БезSource"]; ok {
		t.Fatalf("widget without source reported: %v", byObject["БезSource"])
	}
	if msgs, ok := byObject["НеСписок"]; !ok || len(msgs) != 1 || !strings.Contains(msgs[0], "list") {
		t.Fatalf("non-list source not rejected: %v", msgs)
	}
	if msgs, ok := byObject["НетСущности"]; !ok || len(msgs) != 1 || !strings.Contains(msgs[0], "не найдена") {
		t.Fatalf("missing entity not rejected: %v", msgs)
	}
	if msgs, ok := byObject["ПустойIDField"]; !ok || len(msgs) != 1 || !strings.Contains(msgs[0], "id_field") {
		t.Fatalf("empty id_field not rejected: %v", msgs)
	}
	if msgs, ok := byObject["НеСсылка"]; !ok || len(msgs) != 1 || !strings.Contains(msgs[0], "не подтверждена") {
		t.Fatalf("non-ref id_field not rejected: %v", msgs)
	}
}
