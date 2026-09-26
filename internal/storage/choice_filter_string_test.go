package storage

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Строковый реквизит в choice_filter.
//
// Подбор умел сравнивать только ссылку со ссылкой (плюс булев литерал), и связь
// «дом → улица» адресного классификатора выразить было нечем: ВладелецКод дома
// хранит ИД записи иерархии, а не ссылку. Ссылочная связь там тоже есть, но
// заполнена лишь у половины домов и ведёт в муниципальную ветку, где домов
// меньше, — то есть отбор по ней дал бы пользователю неполный список.
func TestChoicePredicateStringFieldComparesAsString(t *testing.T) {
	entity := &metadata.Entity{
		Name: "А_АдресныйКлассификатор", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "ВладелецКод", Type: metadata.FieldTypeString}},
	}
	sql, args, _, err := choicePredicateSQL(SQLiteDialect{}, entity,
		[]ChoicePredicate{{Field: "ВладелецКод", Op: metadata.FormChoiceOpEqual, Value: "26121072"}}, 1)
	if err != nil {
		t.Fatalf("строковый реквизит отвергнут: %v", err)
	}
	if !strings.Contains(sql, "владелецкод = ") {
		t.Errorf("сравнение не по колонке реквизита: %s", sql)
	}
	if len(args) != 1 || args[0] != "26121072" {
		t.Errorf("значение не доехало строкой: %#v", args)
	}
}

func TestChoicePredicateStringFieldRejectsNonString(t *testing.T) {
	entity := &metadata.Entity{
		Name: "А_АдресныйКлассификатор", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "ВладелецКод", Type: metadata.FieldTypeString}},
	}
	if _, _, _, err := choicePredicateSQL(SQLiteDialect{}, entity,
		[]ChoicePredicate{{Field: "ВладелецКод", Op: metadata.FormChoiceOpEqual, Value: 42}}, 1); err == nil {
		t.Error("нестроковое значение принято строковым реквизитом")
	}
}

func TestChoicePredicateStringFieldRejectsHierarchyOps(t *testing.T) {
	entity := &metadata.Entity{
		Name: "А_АдресныйКлассификатор", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "ВладелецКод", Type: metadata.FieldTypeString}},
	}
	if _, _, _, err := choicePredicateSQL(SQLiteDialect{}, entity,
		[]ChoicePredicate{{Field: "ВладелецКод", Op: metadata.FormChoiceOpInHierarchy, Value: "x"}}, 1); err == nil {
		t.Error("иерархический оператор принят строковым реквизитом")
	}
}
