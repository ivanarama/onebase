package interpreter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestQueryLongReferenceLabels_Matrix(t *testing.T) {
	names := []string{"Связь", "НейтральноеДлинноеПолеСсылкиДляПроверкиРезультата"}
	for n := 60; n <= 66; n++ {
		name := strings.Repeat("Я", n/2)
		if n%2 != 0 {
			name += "X"
		}
		names = append(names, name)
	}
	for _, name := range names {
		t.Run(fmt.Sprintf("bytes-%d", len(name)), func(t *testing.T) {
			target := &metadata.Entity{Name: "Назначение", Kind: metadata.KindCatalog}
			owner := &metadata.Entity{Name: "Владелец", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: name, Type: "reference:Назначение", RefEntity: target.Name}}}
			entities := []*metadata.Entity{target, owner}
			require.NoError(t, metadata.Validate(entities, nil))
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				require.NoError(t, db.Migrate(ctx, entities))
				targetID := uuid.New()
				require.NoError(t, db.Upsert(ctx, target.Name, targetID, nil, target))
				require.NoError(t, db.Upsert(ctx, owner.Name, uuid.New(), map[string]any{name: targetID}, owner))
				for _, alias := range []string{"", "Проверка", "НейтральныйДлинныйПсевдонимВыбраннойСсылки"} {
					t.Run("alias-"+alias, func(t *testing.T) {
						selection, key := "К."+name, name+"_id"
						if alias != "" {
							selection += " КАК " + alias
							key = alias
						}
						script := fmt.Sprintf(`Процедура Тест()
	Запрос = Новый Запрос;
	Запрос.Текст = "ВЫБРАТЬ %s ИЗ Справочник.Владелец КАК К";
	Возврат Запрос.Выполнить()[0].%s;
КонецПроцедуры`, selection, key)
						prog, err := parser.New(lexer.New(script, "reference-label.os")).ParseProgram()
						require.NoError(t, err)
						factory := interpreter.NewQueryFactory(ctx, db, &entityReg{entities: entities})
						for range 2 {
							var result any
							require.NoError(t, interpreter.New().RunWithResult(prog.Procedures[0], runtime.NewObject("Проверка", metadata.KindDocument), &result, map[string]any{"__factory_Запрос": factory}))
							require.Equal(t, targetID.String(), fmt.Sprint(result))
						}
					})
				}
			})
		})
	}
}
