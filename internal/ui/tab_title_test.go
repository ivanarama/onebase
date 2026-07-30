package ui

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// #481: представление записи для заголовка вкладки/карточки.
func TestRecordCardTitle(t *testing.T) {
	catalog := &metadata.Entity{
		Name: "Клиенты",
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}
	document := &metadata.Entity{
		Name: "Продажа",
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}

	cases := []struct {
		name   string
		entity *metadata.Entity
		values any
		want   string
	}{
		{"каталог по Наименованию", catalog, map[string]string{"Наименование": "ООО Ромашка"}, "ООО Ромашка"},
		{"обрезка пробелов", catalog, map[string]string{"Наименование": "  ООО Ромашка  "}, "ООО Ромашка"},
		{"пустое Наименование → пусто", catalog, map[string]string{"Наименование": "   "}, ""},
		{"документ без Наименования → Номер", document, map[string]string{"Номер": "000000123"}, "000000123"},
		{"values как map[string]any", catalog, map[string]any{"Наименование": "Иванов И.И."}, "Иванов И.И."},
		{"nil entity → пусто", nil, map[string]string{"Наименование": "x"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := recordCardTitle(c.entity, c.values); got != c.want {
				t.Errorf("recordCardTitle = %q, want %q", got, c.want)
			}
		})
	}
}

// #481: карточка существующей записи через реальный обработчик формы отдаёт
// <meta name="ob-tab-title"> с представлением записи, а h1 показывает его
// вместо «Редактировать — <Сущность>».
func TestManagedForm_TabTitleMetaFromRecord(t *testing.T) {
	srv, ent, id := setupDeleteActionServer(t) // запись с Наименование="X"
	body := renderObjectFormGET(t, srv, ent, id)

	if !strings.Contains(body, `<meta name="ob-tab-title" content="X">`) {
		t.Errorf("нет <meta ob-tab-title content=\"X\"> на карточке записи")
	}
	if strings.Contains(body, "Редактировать —") {
		t.Errorf("h1 карточки всё ещё «Редактировать — <Сущность>», а не представление записи")
	}
}
