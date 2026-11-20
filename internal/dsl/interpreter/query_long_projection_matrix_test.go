package interpreter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestQueryLongProjectionGuard_Matrix(t *testing.T) {
	const longName = "НейтральныйСтроковыйРеквизитДляПроверкиВыборки"
	short := metadata.SQLIdent(strings.ToLower(longName))
	first := &metadata.Entity{Name: "Первый", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: short, Type: metadata.FieldTypeString}}}
	second := &metadata.Entity{Name: "Второй", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: longName, Type: metadata.FieldTypeString}}}
	entities := []*metadata.Entity{first, second}
	require.NoError(t, metadata.Validate(entities, nil))
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		require.NoError(t, db.Migrate(ctx, entities))
		id := uuid.New()
		for _, e := range entities {
			require.NoError(t, db.Upsert(ctx, e.Name, id, map[string]any{e.Fields[0].Name: "пример1234"}, e))
		}
		for _, selection := range []string{"П.*", "П." + short, "П." + short + " КАК " + longName} {
			for _, policy := range []string{"hide", "mask_tail", "full"} {
				t.Run(selection+"/"+policy, func(t *testing.T) {
					user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{first.Name: {"read"}, second.Name: {"read"}}, FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{first.Name: {short: {Read: policy, Keep: 4}}}}}}}}
					guard := func(_ context.Context, res query.Result, rows []map[string]any) (interpreter.GuardedColumns, error) {
						plan := access.QueryMaskPlanFor(user, res, func(_, name string) *metadata.Entity {
							for _, e := range entities {
								if strings.EqualFold(e.Name, name) {
									return e
								}
							}
							return nil
						})
						tracked, err := plan.ApplyTracked(rows)
						return interpreter.GuardedColumns(tracked), err
					}
					key := short
					if strings.Contains(selection, " КАК ") {
						key = longName
					}
					script := fmt.Sprintf(`Процедура Тест()
     Запрос=Новый Запрос;
     Запрос.Текст="ВЫБРАТЬ %s ИЗ Справочник.Первый КАК П ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Второй КАК Д ПО П.Ссылка=Д.Ссылка";
     Возврат Запрос.Выполнить()[0].%s;
    КонецПроцедуры`, selection, key)
					prog, err := parser.New(lexer.New(script, "projection.os")).ParseProgram()
					require.NoError(t, err)
					factory := interpreter.NewQueryFactoryGuarded(ctx, db, &entityReg{entities: entities}, nil, guard)
					var result any
					require.NoError(t, interpreter.New().RunWithResult(prog.Procedures[0], runtime.NewObject("Проверка", metadata.KindDocument), &result, map[string]any{"__factory_Запрос": factory}))
					want := any("пример1234")
					if policy == "hide" {
						want = nil
					}
					if policy == "mask_tail" {
						want = "••••••1234"
					}
					require.Equal(t, want, result)
				})
			}
		}
	})
}
