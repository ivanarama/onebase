package interpreter_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

type typedQueryRegisterRegistry struct {
	registers []*metadata.Register
}

func (r *typedQueryRegisterRegistry) Registers() []*metadata.Register { return r.registers }
func (r *typedQueryRegisterRegistry) InfoRegisters() []*metadata.InfoRegister {
	return nil
}
func (r *typedQueryRegisterRegistry) AccountRegisters() []*metadata.AccountRegister {
	return nil
}
func (r *typedQueryRegisterRegistry) Entities() []*metadata.Entity { return nil }

func typedQueryEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{
			Name: "Проекты", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{ID: "project_name", Name: "Наименование", Type: metadata.FieldTypeString}},
		},
		{
			Name: "Работы", Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{ID: "work_number", Name: "Номер", Type: metadata.FieldTypeString},
				{ID: "work_amount", Name: "Сумма", Type: metadata.FieldTypeNumber},
				{ID: "work_project", Name: "Проект", Type: metadata.FieldType("reference:Проекты"), RefEntity: "Проекты"},
			},
		},
	}
}

func TestQueryTypedEmpty_ПростаяПроекцияТипизируетNULLТолькоВДSL(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entities := typedQueryEntities()
		require.NoError(t, db.Migrate(ctx, entities))
		require.NoError(t, db.Upsert(ctx, "Работы", uuid.New(), map[string]any{"Номер": "Р-1"}, entities[1]))

		compiled, err := query.Compile(
			`ВЫБРАТЬ Сумма ИЗ Документ.Работы`,
			query.CompileOpts{Entities: entities, Dialect: db.Dialect()},
		)
		require.NoError(t, err)
		rows, _, err := query.Run(ctx, db, &compiled)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Nil(t, rows[0]["сумма"], "общий query runner обязан сохранить SQL NULL")

		const src = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Сумма, Проект, Проект.Ссылка КАК ПроектСсылка ИЗ Документ.Работы";
			Стр = Запрос.Выполнить()[0];
			Возврат ТипЗнч(Стр.Сумма) + "|" + Строка(Стр.Сумма = 0)
				+ "|" + ТипЗнч(Стр.Проект) + "|" + Строка(Стр.Проект = "")
				+ "|" + ТипЗнч(Стр.ПроектСсылка) + "|" + Строка(ПустаяСсылка(Стр.ПроектСсылка));
		КонецПроцедуры`

		assert.Equal(t, "Число|true|Строка|true|СправочникСсылка.Проекты|true",
			runOnDBEntities(t, db, entities, src))
	})
}

func TestQueryTypedEmpty_СистемныеПоляРегистраДоступныВДSL(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		period := time.Date(2026, 9, 8, 12, 34, 56, 0, time.UTC)
		reg := &metadata.Register{
			Name:      "ОстаткиТипизация",
			Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}
		registers := []*metadata.Register{reg}
		repository := &typedQueryRegisterRegistry{registers: registers}

		require.NoError(t, db.MigrateRegisters(ctx, registers))
		require.NoError(t, db.WriteMovements(ctx, reg.Name, "Пост", uuid.New(),
			[]map[string]any{{"ВидДвижения": "Приход", "Количество": float64(1)}}, reg, &period))

		const queryText = `ВЫБРАТЬ Период, ВидДвижения ИЗ РегистрНакопления.ОстаткиТипизация`
		periodResult := evalQuery(t, `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "`+queryText+`";
			Возврат Запрос.Выполнить()[0].Период;
		КонецПроцедуры`, db, repository)
		actualPeriod, ok := periodResult.(time.Time)
		require.True(t, ok, "Период должен быть time.Time, получено %T", periodResult)
		assert.True(t, period.Equal(actualPeriod), "Период: %v", actualPeriod)

		movementResult := evalQuery(t, `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "`+queryText+`";
			Возврат Запрос.Выполнить()[0].ВидДвижения;
		КонецПроцедуры`, db, repository)
		assert.Equal(t, "Приход", movementResult)
	})
}
