package ui

// Выбранная пользователем сортировка списка запоминается по пользователю и
// сущности, как вид списка (#1485). Всё через публичный обработчик списка.

import (
	"context"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestListSortPersistence(t *testing.T) {
	ent := &metadata.Entity{
		Name:   "Клиент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	for i, name := range []string{"Анна", "Борис", "Вера"} {
		id := uuidMust("1111111" + string(rune('1'+i)) + "-1111-1111-1111-111111111111")
		if err := s.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": name}, ent); err != nil {
			t.Fatal(err)
		}
	}
	order := func(body string) string {
		a, b, v := strings.Index(body, "Анна"), strings.Index(body, "Борис"), strings.Index(body, "Вера")
		if a < 0 || b < 0 || v < 0 {
			t.Fatalf("в списке нет строк")
		}
		if v < b && b < a {
			return "desc"
		}
		if a < b && b < v {
			return "asc"
		}
		return "other"
	}
	u1, u2 := user1485("u1"), user1485("u2")

	if got := order(listView1485(t, s, u1, "?sort=Наименование&dir=desc")); got != "desc" {
		t.Fatalf("явная сортировка не применилась: %s", got)
	}
	// Открытие без параметра — тот же порядок, что выбрал пользователь.
	if got := order(listView1485(t, s, u1, "")); got != "desc" {
		t.Fatalf("сохранённая сортировка не восстановилась: %s", got)
	}
	// Другой пользователь — свой порядок (по умолчанию — по наименованию).
	if got := order(listView1485(t, s, u2, "")); got != "asc" {
		t.Fatalf("сортировка одного пользователя протекла другому: %s", got)
	}
	// Новый явный выбор перекрывает сохранённый.
	listView1485(t, s, u1, "?sort=Наименование&dir=asc")
	if got := order(listView1485(t, s, u1, "")); got != "asc" {
		t.Fatalf("новый выбор не перекрыл сохранённый: %s", got)
	}
	// Неизвестное поле не сохраняется и не ломает прежний выбор.
	listView1485(t, s, u1, "?sort=НетТакого&dir=desc")
	if f, d, _ := s.store.GetListSortUserSettings(context.Background(), ent.Name, "u1"); f != "Наименование" || d != "asc" {
		t.Fatalf("сохранено %q|%q, ожидалось Наименование|asc", f, d)
	}
}
