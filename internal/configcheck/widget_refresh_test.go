package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func refreshOnFixture() *project.Project {
	entity := func(name string, notify bool) *metadata.Entity {
		return &metadata.Entity{Name: name, Kind: metadata.KindDocument, NotifyChanges: notify}
	}
	return &project.Project{
		Entities: []*metadata.Entity{
			entity("А_Задача", true),
			entity("Заказ", false),
		},
		Widgets: []*metadata.Widget{
			{Name: "Задачи", Type: metadata.WidgetTypeList, RefreshOn: []string{"данные.а_задача"}},
			{Name: "Заказы", Type: metadata.WidgetTypeKPI, RefreshOn: []string{"данные.Заказ"}},
			{Name: "Пусто", Type: metadata.WidgetTypeChart, RefreshOn: []string{"данные.несуществующая"}},
			{Name: "Кнопки", Type: metadata.WidgetTypeActions, RefreshOn: []string{"данные.а_задача"}},
			{Name: "Чистый", Type: metadata.WidgetTypeRecent},
		},
	}
}

func TestCheckWidgetRefreshOn_RejectsActions(t *testing.T) {
	issues := CheckWidgetRefreshOn(refreshOnFixture())
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly the actions error", issues)
	}
	if issues[0].Object != "Кнопки" || !strings.Contains(issues[0].Message, "actions") {
		t.Fatalf("unexpected issue: %+v", issues[0])
	}
}

func TestCheckWidgetRefreshOnPublisherWarnings(t *testing.T) {
	warnings := CheckWidgetRefreshOnPublisherWarnings(refreshOnFixture())
	// «данные.Заказ» — регистр не совпадает с публикуемым «данные.заказ»;
	// «данные.несуществующая» — сущности нет. У «данные.а_задача» издатель есть.
	if len(warnings) != 2 {
		t.Fatalf("warnings = %+v, want 2", warnings)
	}
	byObject := map[string]string{}
	for _, w := range warnings {
		byObject[w.Object] = w.Message
	}
	msg, ok := byObject["Заказы"]
	if !ok || !strings.Contains(msg, "данные.заказ") {
		t.Fatalf("case-mismatch warning missing: %+v", warnings)
	}
	msg, ok = byObject["Пусто"]
	if !ok || !strings.Contains(msg, "не найдена") {
		t.Fatalf("missing-publisher warning missing: %+v", warnings)
	}
}
