package storage

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func refTo(name, target string) metadata.Field {
	return metadata.Field{Name: name, Type: metadata.FieldType("reference:" + target), RefEntity: target}
}

func namesOf(entities []*metadata.Entity) []string {
	names := make([]string, 0, len(entities))
	for _, e := range entities {
		names = append(names, e.Name)
	}
	return names
}

func positions(t *testing.T, entities []*metadata.Entity) map[string]int {
	t.Helper()
	at := make(map[string]int, len(entities))
	for i, e := range entities {
		if _, dup := at[e.Name]; dup {
			t.Fatalf("сущность %s попала в порядок дважды: %v", e.Name, namesOf(entities))
		}
		at[e.Name] = i
	}
	return at
}

// Порядок обязан отвечать за ВСЕ ссылки, кроме названных отложенными: именно
// на это опирается CREATE TABLE с внешним ключом внутри.
func assertOrderCoversRefs(t *testing.T, entities []*metadata.Entity, ordered []*metadata.Entity, deferred []refCycleFK) {
	t.Helper()
	at := positions(t, ordered)
	if len(ordered) != len(entities) {
		t.Fatalf("порядок потерял сущности: %v из %v", namesOf(ordered), namesOf(entities))
	}
	isDeferred := make(map[string]bool, len(deferred))
	for _, fk := range deferred {
		isDeferred[fk.Entity+"."+fk.Column] = true
	}
	for _, e := range entities {
		for _, f := range e.Fields {
			if f.RefEntity == "" || f.RefEntity == e.Name {
				continue
			}
			if _, known := at[f.RefEntity]; !known {
				continue
			}
			if isDeferred[e.Name+"."+metadata.ColumnName(f)] {
				continue
			}
			if at[f.RefEntity] > at[e.Name] {
				t.Fatalf("%s.%s ссылается на %s, который создаётся позже: %v",
					e.Name, f.Name, f.RefEntity, namesOf(ordered))
			}
		}
	}
}

func TestOrderByDependency_ChainNeedsNoDeferral(t *testing.T) {
	group := &metadata.Entity{Name: "Группа", Kind: metadata.KindCatalog}
	fault := &metadata.Entity{Name: "Неисправность", Kind: metadata.KindCatalog, Fields: []metadata.Field{refTo("Группа", "Группа")}}
	request := &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, Fields: []metadata.Field{refTo("Неисправность", "Неисправность")}}

	entities := []*metadata.Entity{request, fault, group}
	ordered, deferred := orderByDependency(entities)
	if len(deferred) != 0 {
		t.Fatalf("дерево ссылок не требует отложенных ключей, отложено: %+v", deferred)
	}
	assertOrderCoversRefs(t, entities, ordered, deferred)
}

// Круг: «Обращение → Заявка → Обращение». Порядком его не разрешить — ровно
// одну ссылку приходится назвать отложенной, остальные порядок закрывает.
func TestOrderByDependency_CycleIsNamedNotHidden(t *testing.T) {
	task := &metadata.Entity{Name: "Задача", Kind: metadata.KindCatalog}
	appeal := &metadata.Entity{Name: "Обращение", Kind: metadata.KindDocument, Fields: []metadata.Field{refTo("ДополнениеКЗаявке", "Заявка")}}
	request := &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, Fields: []metadata.Field{
		refTo("Обращение", "Обращение"), refTo("Задача", "Задача"),
	}}

	for _, entities := range [][]*metadata.Entity{
		{request, appeal, task},
		{appeal, request, task},
		{task, request, appeal},
	} {
		ordered, deferred := orderByDependency(entities)
		if len(deferred) != 1 {
			t.Fatalf("порядок %v: отложено %d ссылок, ожидалась ровно одна: %+v",
				namesOf(entities), len(deferred), deferred)
		}
		if deferred[0].Ref == deferred[0].Entity {
			t.Fatalf("порядок %v: отложена самоссылка: %+v", namesOf(entities), deferred[0])
		}
		// Отложенное ребро обязано быть ребром круга: ссылка на «Задачу»
		// порядком закрывается, откладывать её незачем.
		if deferred[0].Ref == task.Name {
			t.Fatalf("порядок %v: отложена ссылка вне круга: %+v", namesOf(entities), deferred[0])
		}
		assertOrderCoversRefs(t, entities, ordered, deferred)
	}
}

func TestOrderByDependency_SelfReferenceStaysInline(t *testing.T) {
	unit := &metadata.Entity{Name: "Подразделение", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		refTo("Родитель", "Подразделение"),
	}}
	ordered, deferred := orderByDependency([]*metadata.Entity{unit})
	if len(deferred) != 0 {
		t.Fatalf("самоссылка отложена без нужды: %+v", deferred)
	}
	if len(ordered) != 1 || ordered[0] != unit {
		t.Fatalf("сущность с самоссылкой потерялась: %v", namesOf(ordered))
	}
}

// Ссылка на таблицу, которая мигрирует не здесь (служебные `_users`), порядком
// не управляется — но и сущность из-за неё пропасть не должна.
func TestOrderByDependency_UnknownTargetKeepsEntity(t *testing.T) {
	user := &metadata.Entity{Name: "Пользователи", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		refTo("УчётнаяЗапись", "_users"),
	}}
	ordered, deferred := orderByDependency([]*metadata.Entity{user})
	if len(deferred) != 0 {
		t.Fatalf("ссылка на служебную таблицу отложена: %+v", deferred)
	}
	if len(ordered) != 1 {
		t.Fatalf("сущность со ссылкой наружу потерялась: %v", namesOf(ordered))
	}
}

func TestDeferredFKColumnsGroupsByEntity(t *testing.T) {
	if got := deferredFKColumns(nil); got != nil {
		t.Fatalf("пустой список дал непустую карту: %v", got)
	}
	got := deferredFKColumns([]refCycleFK{
		{Entity: "Обращение", Column: "заявка_id", Ref: "Заявка"},
		{Entity: "Обращение", Column: "вторая_заявка_id", Ref: "Заявка"},
		{Entity: "Заявка", Column: "обращение_id", Ref: "Обращение"},
	})
	if len(got) != 2 || len(got["Обращение"]) != 2 || !got["Обращение"]["заявка_id"] || !got["Заявка"]["обращение_id"] {
		t.Fatalf("колонки сгруппированы неверно: %v", got)
	}
}

func TestForeignKeyNameIsStableAndShort(t *testing.T) {
	name := ForeignKeyName("обращение", "дополнениекзаявке_id", "заявка")
	if name != ForeignKeyName("обращение", "дополнениекзаявке_id", "заявка") {
		t.Fatal("имя ключа не воспроизводится")
	}
	if name == ForeignKeyName("заявка", "обращение_id", "обращение") {
		t.Fatalf("разные ключи получили одно имя: %s", name)
	}
	// 63 байта — предел идентификатора PostgreSQL, а имена конфигурации
	// кириллические: имя ключа не должно от них зависеть по длине.
	if len(name) > 63 {
		t.Fatalf("имя ключа длиннее предела PostgreSQL: %s", name)
	}
}
