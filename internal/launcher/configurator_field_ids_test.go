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

// Ключ `default` (план 153) редактор реквизитов не показывает и потому не
// присылает обратно. Он обязан пережить сохранение — иначе правка типа одного
// реквизита молча снимала бы значения по умолчанию со всего объекта: та же
// потеря, ради которой заведён перенос id и saveNumerator.
func TestEnsureFieldIDsKeepsDefault(t *testing.T) {
	prev := []saveField{
		{ID: "f_aaa", Name: "Дата", Type: "date", Default: "сейчас"},
		{ID: "f_bbb", Name: "Организация", Type: "reference:Организация", Default: "константа.НашаОрганизация"},
		{ID: "f_ccc", Name: "Комментарий", Type: "string"},
	}
	next := []saveField{
		{Name: "Дата", Type: "date"},
		{Name: "Организация", Type: "reference:Организация"},
		{Name: "Комментарий", Type: "string"},
		{Name: "Новый", Type: "string"},
	}

	got := ensureFieldIDs(prev, next)
	if got[0].Default != "сейчас" {
		t.Errorf("default даты потерян: %q", got[0].Default)
	}
	if got[1].Default != "константа.НашаОрганизация" {
		t.Errorf("default организации потерян: %q", got[1].Default)
	}
	if got[2].Default != "" || got[3].Default != "" {
		t.Errorf("дефолт появился там, где его не было: %+v", got[2:])
	}
}

// Значение, пришедшее из формы, главнее прежнего: иначе редактор, когда
// научится править дефолты, не смог бы их изменить.
func TestEnsureFieldIDsFormDefaultWins(t *testing.T) {
	prev := []saveField{{ID: "f_aaa", Name: "Дата", Type: "date", Default: "сейчас"}}
	next := []saveField{{Name: "Дата", Type: "date", Default: "сегодня"}}
	if got := ensureFieldIDs(prev, next); got[0].Default != "сегодня" {
		t.Errorf("default = %q, ожидалось значение из формы", got[0].Default)
	}
}
