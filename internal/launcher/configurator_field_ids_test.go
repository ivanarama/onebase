package launcher

import (
	"strings"
	"testing"
)

// Редактор реквизитов пересобирает список полей из формы, поэтому id обязан
// переноситься из прежнего состояния файла — иначе каждое сохранение в
// конфигураторе разрывало бы связь поля с колонкой (план 81).
func TestEnsureFieldIDsKeepsExisting(t *testing.T) {
	prev := []saveField{
		{ID: "f_aaa", Name: "Наименование", Type: "string"},
		{ID: "f_bbb", Name: "Цена", Type: "number"},
	}
	next := []saveField{
		{Name: "Наименование", Type: "string"},
		{Name: "Цена", Type: "number(15,2)"}, // тип поменяли — id тот же
		{Name: "Комментарий", Type: "string"},
	}

	got := ensureFieldIDs(prev, next)
	if got[0].ID != "f_aaa" || got[1].ID != "f_bbb" {
		t.Fatalf("существующие id не перенесены: %+v", got)
	}
	if got[2].ID == "" {
		t.Fatal("новому реквизиту должен выдаваться id")
	}
	if got[2].ID == "f_aaa" || got[2].ID == "f_bbb" {
		t.Fatalf("новый id совпал с занятым: %s", got[2].ID)
	}
	if !strings.HasPrefix(got[2].ID, "f_") {
		t.Fatalf("неожиданный формат id: %s", got[2].ID)
	}
}

// Сохранение объекта, у которого id ещё нет, проставляет их всем реквизитам:
// это и есть путь, которым существующие конфигурации переходят на устойчивые
// идентификаторы.
func TestEnsureFieldIDsAssignsToAll(t *testing.T) {
	next := []saveField{
		{Name: "Наименование", Type: "string"},
		{Name: "Цена", Type: "number"},
	}
	got := ensureFieldIDs(nil, next)
	seen := map[string]bool{}
	for _, f := range got {
		if f.ID == "" {
			t.Fatalf("реквизит %s остался без id", f.Name)
		}
		if seen[f.ID] {
			t.Fatalf("id %s выдан дважды", f.ID)
		}
		seen[f.ID] = true
	}
}

// Явно заданный в форме id не перетирается.
func TestEnsureFieldIDsRespectsIncoming(t *testing.T) {
	prev := []saveField{{ID: "f_old", Name: "Цена", Type: "number"}}
	next := []saveField{{ID: "f_new", Name: "Цена", Type: "number"}}
	if got := ensureFieldIDs(prev, next); got[0].ID != "f_new" {
		t.Fatalf("пришедший id заменён на %s", got[0].ID)
	}
}
