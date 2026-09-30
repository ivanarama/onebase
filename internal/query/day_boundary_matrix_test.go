package query_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Граница дня в отборе обязана означать одно и то же на SQLite и PostgreSQL.
//
// SQLite хранит момент текстом в UTC — у справочника «2026-10-09T21:00:00Z», у
// регистров «2026-10-09 21:00:00+00:00» — и сравнивает лексикографически, а
// НачалоДня/КонецДня с #1243 отдают местные стенные часы без зоны. Отбор «за
// 10 октября» в Москве терял первые три часа дня и захватывал первые три часа
// 11-го; ИМЕЮЩИЕ МАКСИМУМ(Срок) < НачалоДня(&НаДату) считал просроченным долг
// со сроком «сегодня»; Обороты(НачалоДня(&Д), КонецДня(&Д)) брали чужие
// движения. Параметр-дата против реквизита регистра уходил в формате
// справочника и тоже промахивался. На PostgreSQL всё это было верно, поэтому
// один отчёт давал на двух СУБД разные цифры.
//
// Два пояса ловят оба направления: в Москве местная полночь — вечер
// предыдущего UTC-дня, в Нью-Йорке — утро того же. Путь публичный:
// Compile → Run, одно тело на обоих диалектах.
func TestDayBoundaryComparisonsMatrix(t *testing.T) {
	for _, zone := range []string{"Europe/Moscow", "America/New_York"} {
		zone := zone
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			require.NoError(t, err)
			saved := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = saved })
			dayBoundaryMatrix(t, loc)
		})
	}
}

func dayBoundaryMatrix(t *testing.T, loc *time.Location) {
	at := func(day, hour, minute, second int) time.Time {
		return time.Date(2026, 10, day, hour, minute, second, 0, loc)
	}
	// Отбираем 10 октября по местному времени.
	points := map[string]time.Time{
		"до":      at(9, 23, 30, 0),
		"полночь": at(10, 0, 0, 0),
		"начало":  at(10, 0, 30, 0),
		"конец":   at(10, 23, 30, 0),
		"после":   at(11, 1, 0, 0),
	}
	params := map[string]any{
		"Д":    at(10, 0, 0, 0),
		"След": at(11, 0, 0, 0),
		"Кон":  at(10, 23, 59, 59),
	}
	const (
		day    = "конец,начало,полночь"
		before = "до"
		upTo   = "до,конец,начало,полночь"
	)

	ent := &metadata.Entity{Name: "Событие", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Момент", Type: metadata.FieldTypeDate},
	}}
	reg := &metadata.Register{Name: "Долги",
		Dimensions: []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}},
		Attributes: []metadata.Field{{Name: "Срок", Type: metadata.FieldTypeDate}},
	}
	ireg := &metadata.InfoRegister{Name: "Сроки", Periodic: true,
		Dimensions: []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Срок", Type: metadata.FieldTypeDate}},
	}

	cases := []struct{ name, text, want string }{
		// Реквизит справочника (RFC3339 на SQLite).
		{"граница дня", "ВЫБРАТЬ Наименование ИЗ Справочник.Событие ГДЕ Момент >= НачалоДня(&Д) И Момент <= КонецДня(&Д)", day},
		{"полуинтервал параметров", "ВЫБРАТЬ Наименование ИЗ Справочник.Событие ГДЕ Момент >= &Д И Момент < &След", day},
		{"день момента", "ВЫБРАТЬ Наименование ИЗ Справочник.Событие ГДЕ НачалоДня(Момент) = НачалоДня(&Д)", day},
		{"МЕЖДУ границами", "ВЫБРАТЬ Наименование ИЗ Справочник.Событие ГДЕ Момент МЕЖДУ НачалоДня(&Д) И КонецДня(&Д)", day},
		{"раньше дня", "ВЫБРАТЬ Наименование ИЗ Справочник.Событие ГДЕ Момент < НачалоДня(&Д)", before},
		{"граница слева", "ВЫБРАТЬ Наименование ИЗ Справочник.Событие ГДЕ КонецДня(&Д) < Момент", "после"},
		{"день момента против параметра", "ВЫБРАТЬ Наименование ИЗ Справочник.Событие ГДЕ НачалоДня(Момент) >= &Д", day + ",после"},
		{"квалифицированное поле", "ВЫБРАТЬ С.Наименование ИЗ Справочник.Событие КАК С ГДЕ С.Момент >= НачалоДня(&Д) И С.Момент < НачалоДня(&След)", day},

		// Период и реквизит регистра накопления (sqliteTimeLayout на SQLite).
		{"период регистра", "ВЫБРАТЬ Метка ИЗ РегистрНакопления.Долги ГДЕ period >= НачалоДня(&Д) И period <= КонецДня(&Д)", day},
		{"реквизит регистра и параметры", "ВЫБРАТЬ Метка ИЗ РегистрНакопления.Долги ГДЕ Срок >= &Д И Срок < &След", day},
		{"реквизит регистра до конца дня", "ВЫБРАТЬ Метка ИЗ РегистрНакопления.Долги ГДЕ Срок <= &Кон", upTo},
		{"просрочка", "ВЫБРАТЬ Метка ИЗ РегистрНакопления.Долги ГДЕ Срок < НачалоДня(&Д)", before},
		{"просрочка по максимуму", "ВЫБРАТЬ Метка ИЗ РегистрНакопления.Долги СГРУППИРОВАТЬ ПО Метка ИМЕЮЩИЕ МАКСИМУМ(Срок) < НачалоДня(&Д)", before},
		{"просрочка в ВЫБОР", "ВЫБРАТЬ Метка ИЗ РегистрНакопления.Долги СГРУППИРОВАТЬ ПО Метка ИМЕЮЩИЕ ВЫБОР КОГДА МАКСИМУМ(Срок) < НачалоДня(&Д) ТОГДА 1 ИНАЧЕ 0 КОНЕЦ = 1", before},
		{"обороты за день", "ВЫБРАТЬ Метка ИЗ РегистрНакопления.Долги.Обороты(НачалоДня(&Д), КонецДня(&Д))", day},
		{"остатки на конец дня", "ВЫБРАТЬ Метка ИЗ РегистрНакопления.Долги.Остатки(КонецДня(&Д)) ГДЕ СуммаОстаток <> 0", upTo},

		// Регистр сведений (sqliteTimeLayout на SQLite).
		{"период регистра сведений", "ВЫБРАТЬ Метка ИЗ РегистрСведений.Сроки ГДЕ period >= НачалоДня(&Д) И period <= КонецДня(&Д)", day},
		{"ресурс регистра сведений и параметры", "ВЫБРАТЬ Метка ИЗ РегистрСведений.Сроки ГДЕ Срок >= &Д И Срок < &След", day},
		{"срез на конец дня", "ВЫБРАТЬ Метка ИЗ РегистрСведений.Сроки.СрезПоследних(КонецДня(&Д))", upTo},
	}

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))
		require.NoError(t, db.MigrateRegisters(ctx, []*metadata.Register{reg}))
		require.NoError(t, db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ireg}))
		for name, moment := range points {
			moment := moment
			require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": name, "Момент": moment}, ent))
			require.NoError(t, db.WriteMovements(ctx, reg.Name, "Документ", uuid.New(), []map[string]any{
				{"ВидДвижения": "Приход", "Метка": name, "Сумма": float64(1), "Срок": moment},
			}, reg, &moment))
			require.NoError(t, db.InfoRegSet(ctx, ireg, map[string]any{"Метка": name}, map[string]any{"Срок": moment}, &moment))
		}

		for _, tc := range cases {
			compiled, err := query.Compile(tc.text, query.CompileOpts{
				Entities:  []*metadata.Entity{ent},
				Registers: []*metadata.Register{reg},
				InfoRegs:  []*metadata.InfoRegister{ireg},
				Dialect:   db.Dialect(),
				Params:    params,
			})
			require.NoError(t, err, "%s: компиляция", tc.name)
			rows, _, err := query.Run(ctx, db, &compiled)
			require.NoError(t, err, "%s: выполнение\nSQL: %s", tc.name, compiled.SQL)
			var got []string
			for _, row := range rows {
				for _, v := range row {
					got = append(got, fmt.Sprint(v))
				}
			}
			sort.Strings(got)
			require.Equal(t, tc.want, strings.Join(got, ","), "%s\nSQL: %s", tc.name, compiled.SQL)
		}
	})
}

// Период виртуальной таблицы с периодичностью на SQLite — усечённая метка
// («2026-10-10»), а не момент: сравнение с границей дня с ней совпадало и
// обязано остаться прежним, без перевода границы в UTC.
func TestDayBoundaryLeavesVirtualTablePeriodLabel(t *testing.T) {
	reg := &metadata.Register{Name: "Долги",
		Dimensions: []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}},
	}
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.Local)
	for _, text := range []string{
		"ВЫБРАТЬ Период, Метка ИЗ РегистрНакопления.Долги.Обороты(&Нач, &Кон, День) ГДЕ Период = НачалоДня(&Д)",
		"ВЫБРАТЬ О.Период, О.Метка ИЗ РегистрНакопления.Долги.Обороты(&Нач, &Кон, День) КАК О ГДЕ О.Период = НачалоДня(&Д)",
	} {
		compiled, err := query.Compile(text, query.CompileOpts{
			Registers: []*metadata.Register{reg},
			Dialect:   storage.SQLiteDialect{},
			Params:    map[string]any{"Нач": day, "Кон": day.AddDate(0, 0, 1), "Д": day},
		})
		require.NoError(t, err, text)
		require.NotContains(t, compiled.SQL, "ob_utc_", compiled.SQL)
		require.Contains(t, compiled.SQL, "ob_local_datetime(", compiled.SQL)
	}
}
