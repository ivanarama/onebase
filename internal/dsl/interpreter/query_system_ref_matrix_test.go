package interpreter_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// The system target is absent from QueryRegistry.Entities(), but the result
// must have the same reference contract as ТекущийПользователь().Ссылка.
func TestQueryRefAttrDereference_SystemUsersReference(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := auth.NewRepo(db)
		require.NoError(t, repo.EnsureSchema(ctx))
		user, err := repo.Create(ctx, "query-user", "test-password-123", "Test user", true)
		require.NoError(t, err)
		ents := refAttrDerefEntities()[1:]
		employee, task := ents[0], ents[1]
		employee.Fields[1] = metadata.Field{ID: "s_acc", Name: "УчётнаяЗапись",
			Type: "reference:" + metadata.SystemUsersEntity, RefEntity: metadata.SystemUsersEntity}
		require.NoError(t, db.Migrate(ctx, ents))
		employeeID := uuid.New()
		require.NoError(t, db.Upsert(ctx, employee.Name, employeeID,
			map[string]any{"Наименование": "Первый", "УчётнаяЗапись": user.ID}, employee))
		require.NoError(t, db.Upsert(ctx, task.Name, uuid.New(),
			map[string]any{"Номер": "З-1", "Исполнитель": employeeID.String()}, task))

		for _, qualified := range []bool{false, true} {
			path, from := "Исполнитель.УчётнаяЗапись", "Документ.ЗадачаДереф"
			if qualified {
				path, from = "З.Исполнитель.УчётнаяЗапись", "Документ.ЗадачаДереф КАК З"
			}
			for _, alias := range []struct{ sql, field string }{
				{" КАК Учётка", "Учётка"},
				{" КАК Ссылка", "Ссылка"},
				{" AS Reference", "Reference"},
				{"", "УчётнаяЗапись_id"},
			} {
				t.Run(fmt.Sprintf("qualified=%t/alias=%s", qualified, alias.field), func(t *testing.T) {
					src := fmt.Sprintf(`Процедура Тест()
						Запрос = Новый Запрос;
						Запрос.Текст = "ВЫБРАТЬ %s%s ИЗ %s";
						Ссыл = Запрос.Выполнить()[0].%s;
						Втор = Новый Запрос;
						Втор.Текст = "ВЫБРАТЬ Номер ИЗ Документ.ЗадачаДереф ГДЕ Исполнитель.УчётнаяЗапись = &У";
						Втор.УстановитьПараметр("У", Ссыл);
						Если Втор.Выполнить()[0].Номер <> "З-1" Тогда
							ВызватьИсключение "Reference parameter lost its UUID";
						КонецЕсли;
						Если ТипЗнч(Ссыл) <> "Ссылка" Тогда
							ВызватьИсключение "System reference lost its type";
						КонецЕсли;
						Возврат Ссыл;
					КонецПроцедуры`, path, alias.sql, from, alias.field)
					ref, ok := runOnDBEntities(t, db, ents, src).(*interpreter.Ref)
					require.True(t, ok, "system reference must not remain a UUID string")
					assert.Equal(t, metadata.SystemUsersEntity, ref.Type)
					assert.Empty(t, ref.Kind, "same kind as the current user reference")
					assert.Equal(t, user.ID, ref.UUID)
					assert.Equal(t, user.ID, ref.String())
					assert.Equal(t, "Ссылка", ref.TypeName())
				})
			}
		}

		const guardedSrc = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Исполнитель.УчётнаяЗапись КАК Учётка ИЗ Документ.ЗадачаДереф";
			Возврат Запрос.Выполнить()[0].Учётка;
		КонецПроцедуры`
		prog, err := parser.New(lexer.New(guardedSrc, "test.os")).ParseProgram()
		require.NoError(t, err)
		for _, mode := range []string{"masked", "hidden", "nil", "denied"} {
			t.Run(mode, func(t *testing.T) {
				guard := func(_ context.Context, _ query.Result, rows []map[string]any) (interpreter.GuardedColumns, error) {
					switch mode {
					case "masked":
						rows[0]["учётка"] = "[hidden]"
					case "hidden":
						delete(rows[0], "учётка")
					case "nil":
						rows[0]["учётка"] = nil
					case "denied":
						return nil, fmt.Errorf("read denied")
					}
					return interpreter.GuardedColumns{{"учётка": {}}}, nil
				}
				factory := interpreter.NewQueryFactoryGuarded(ctx, db, &entityReg{entities: ents}, nil, guard)
				var result any
				err := interpreter.New().RunWithResult(prog.Procedures[0],
					runtime.NewObject("Test", metadata.KindDocument), &result,
					map[string]any{"__factory_Запрос": factory})
				if mode == "denied" {
					require.ErrorContains(t, err, "read denied")
				} else {
					require.NoError(t, err)
					if mode == "masked" {
						assert.Equal(t, "[hidden]", result)
					} else {
						assert.Nil(t, result, "security guard output must not recreate a reference")
					}
				}
			})
		}
	})
}
