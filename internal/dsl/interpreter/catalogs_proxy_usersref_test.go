package interpreter

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// НайтиПоРеквизиту/ПроверитьСовпадениеПоРеквизиту со ссылочным реквизитом и
// Ref-аргументом обязаны сравнивать по UUID ссылки, а не по её представлению:
// `НайтиПоРеквизиту("УчётнаяЗапись", ТекущийПользователь().Ссылка)` — базовый
// сценарий связи учётки с элементом справочника (issue #1646). Ref.Name здесь —
// «Иванов И.И.», в колонке лежит идентификатор учётки.
func TestCatalogProxy_FindByAttributeRefArgMatchesUUID(t *testing.T) {
	const userUUID = "11111111-1111-1111-1111-111111111111"
	entity := &metadata.Entity{Name: "Сотрудники", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "УчётнаяЗапись", Type: "reference:" + metadata.SystemUsersEntity, RefEntity: metadata.SystemUsersEntity},
	}}
	db := &fakeCatalogsDB{
		byField: map[string]map[string]struct{ ID, Display string }{
			"Сотрудники/УчётнаяЗапись": {
				userUUID: {ID: "22222222-2222-2222-2222-222222222222", Display: "Иванов И.И."},
			},
		},
		matchRows: map[string]map[string][]struct{ ID, Display string }{
			"Сотрудники/УчётнаяЗапись": {
				userUUID: {{ID: "22222222-2222-2222-2222-222222222222", Display: "Иванов И.И."}},
			},
		},
	}
	cp := NewCatalogProxy(entity, db, NewStaticCtx(context.Background()))

	userRef := &Ref{UUID: userUUID, Name: "ivanov", Type: metadata.SystemUsersEntity}
	v := cp.CallMethod("найтипореквизиту", []any{"УчётнаяЗапись", userRef})
	ref, ok := v.(*Ref)
	if !ok {
		t.Fatalf("ожидался *Ref, получили %T", v)
	}
	if ref.UUID != "22222222-2222-2222-2222-222222222222" || ref.Name != "Иванов И.И." {
		t.Errorf("найден не тот элемент: %+v", ref)
	}

	m := cp.CallMethod("проверитьсовпадениепореквизиту", []any{"УчётнаяЗапись", userRef}).(*Struct)
	if got := m.Get("Статус"); got != "НайденаОдна" {
		t.Errorf("Статус = %v, want НайденаОдна", got)
	}
	if got, ok := m.Get("Ссылка").(*Ref); !ok || got.UUID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("Ссылка = %#v", m.Get("Ссылка"))
	}
}
