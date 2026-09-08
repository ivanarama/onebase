package interpreter_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

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
