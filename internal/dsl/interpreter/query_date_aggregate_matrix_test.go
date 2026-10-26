package interpreter_test

// Дата присоединённого источника и МИНИМУМ/МАКСИМУМ поля-даты приходили из
// Запрос.Выполнить() строкой («2026-10-18T14:30:13Z»): DateColumns знал только
// прямые поля первого источника. Разность таких «дат» молча давала
// Неопределено — так на конфигурации колл-центра «Задержка звонка» расчёта
// штрафа не находила ни одной заявки. Тест матричный: на PostgreSQL драйвер и
// так отдаёт time.Time.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func callEntities() []*metadata.Entity {
	order := &metadata.Entity{
		Name: "Заявки", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_num", Name: "Наименование", Type: metadata.FieldTypeString},
			{ID: "f_sent", Name: "Сдана", Type: metadata.FieldTypeDate},
		},
	}
	call := &metadata.Entity{
		Name: "Звонки", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_cnum", Name: "Наименование", Type: metadata.FieldTypeString},
			{ID: "f_when", Name: "Момент", Type: metadata.FieldTypeDate},
		},
	}
	return []*metadata.Entity{order, call}
}

func TestQueryJoinedAndAggregateDateColumnsAreDates(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := callEntities()
		require.NoError(t, db.Migrate(ctx, ents))
		sent := time.Date(2026, 10, 18, 9, 0, 0, 0, time.Local)
		require.NoError(t, db.Upsert(ctx, ents[0].Name, uuid.New(),
			map[string]any{"Наименование": "З-1", "Сдана": sent}, ents[0]))
		for _, h := range []int{2, 5} {
			require.NoError(t, db.Upsert(ctx, ents[1].Name, uuid.New(),
				map[string]any{"Наименование": "З-1", "Момент": sent.Add(time.Duration(h) * time.Hour)}, ents[1]))
		}

		const src = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ З.Сдана КАК Сдана, Зв.Момент КАК Звонок
				|ИЗ Справочник.Заявки КАК З
				|  ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.Звонки КАК Зв ПО Зв.Наименование = З.Наименование
				|УПОРЯДОЧИТЬ ПО Звонок";
			Стр = Запрос.Выполнить()[0];
			Итог = ТипЗнч(Стр.Звонок) + "/" + Строка((Стр.Звонок - Стр.Сдана) / 3600);
			Запрос.Текст = "ВЫБРАТЬ З.Наименование КАК Номер, МИНИМУМ(Зв.Момент) КАК Первый, МАКСИМУМ(З.Сдана) КАК Сдана
				|ИЗ Справочник.Заявки КАК З
				|  ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.Звонки КАК Зв ПО Зв.Наименование = З.Наименование
				|СГРУППИРОВАТЬ ПО З.Наименование";
			Стр = Запрос.Выполнить()[0];
			Возврат Итог + "/" + ТипЗнч(Стр.Первый) + "/" + ТипЗнч(Стр.Сдана) + "/" + Строка((Стр.Первый - Стр.Сдана) / 3600);
		КонецПроцедуры`

		assert.Equal(t, "Дата/2/Дата/Дата/2", runOnDBWithCatalogs(t, db, ents, src),
			"дата присоединённого источника и МИНИМУМ/МАКСИМУМ даты должны приходить датой")
	})
}
