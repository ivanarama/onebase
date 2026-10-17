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

// Дата-измерение регистра сведений сравнивается с параметром-моментом так же,
// как в PostgreSQL. На SQLite регистр хранит дату в формате привязки
// time.Time, а параметр уходил RFC3339 (правило #462 для реквизитов объектов):
// московская полночь — тот же UTC-день 21:00, и строковое сравнение «пробел
// против T» давало «Дата = &Д» без строк и терял границу у «>=»/МЕЖДУ.
func TestInfoRegisterDateDimensionComparedToTimeParamMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{Name: "ТабельПробы", Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Номер", Type: metadata.FieldTypeString},
				{Name: "Дата", Type: metadata.FieldTypeDate},
			}}
		entities := []*metadata.Entity{doc}
		require.NoError(t, db.Migrate(ctx, entities))
		ir := &metadata.InfoRegister{Name: "СменыПробы",
			Dimensions: []metadata.Field{
				{Name: "Мастер", Type: metadata.FieldTypeString},
				{Name: "Дата", Type: metadata.FieldTypeDate},
			},
			Resources: []metadata.Field{{Name: "Часов", Type: metadata.FieldTypeNumber}},
		}
		irs := []*metadata.InfoRegister{ir}
		require.NoError(t, db.MigrateInfoRegisters(ctx, irs))

		moscow := time.FixedZone("Europe/Moscow", 3*60*60)
		day := func(d int) time.Time { return time.Date(2027, 3, d, 0, 0, 0, 0, moscow) }
		// Ресурс «Часов» = номер дня от 1 марта: 0 — это 28.02, 32 — 01.04.
		for _, d := range []int{0, 1, 2, 31, 32} {
			require.NoError(t, db.InfoRegSet(ctx, ir,
				map[string]any{"Мастер": "001", "Дата": day(d)}, map[string]any{"Часов": d}, nil))
		}
		require.NoError(t, db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{"Номер": "001", "Дата": day(1)}, doc))
		// Регистр накопления пишет даты тем же путём, что и регистр сведений.
		reg := &metadata.Register{Name: "ПриходПробы",
			Dimensions: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
			Resources:  []metadata.Field{{Name: "Часов", Type: metadata.FieldTypeNumber}},
		}
		regs := []*metadata.Register{reg}
		require.NoError(t, db.MigrateRegisters(ctx, regs))
		period := day(1)
		for _, d := range []int{0, 1, 2, 31, 32} {
			require.NoError(t, db.WriteMovements(ctx, reg.Name, doc.Name, uuid.New(), []map[string]any{{
				"ВидДвижения": "Приход", "Дата": day(d), "Часов": d,
			}}, reg, &period))
		}

		// Возвращает найденные дни, а не их число: без исправления МЕЖДУ за
		// март отдавал те же три строки, но 02.03, 31.03 и 01.04.
		days := func(t *testing.T, text string, params map[string]any) string {
			t.Helper()
			compiled, err := query.Compile(text, query.CompileOpts{Entities: entities, InfoRegs: irs, Registers: regs,
				Dialect: db.Dialect(), Params: params})
			require.NoError(t, err)
			rows, _, err := query.Run(ctx, db, &compiled)
			require.NoError(t, err, compiled.SQL)
			var got []int
			for _, row := range rows {
				var n int
				_, err := fmt.Sscan(fmt.Sprint(row["часов"]), &n)
				require.NoError(t, err, "%v", row)
				got = append(got, n)
			}
			sort.Ints(got)
			return strings.Trim(fmt.Sprint(got), "[]")
		}
		march := map[string]any{"Начало": day(1), "Конец": time.Date(2027, 3, 31, 23, 59, 59, 0, moscow)}
		for _, tc := range []struct {
			name, text string
			params     map[string]any
			want       string
		}{
			{"равно", "ВЫБРАТЬ Часов ИЗ РегистрСведений.СменыПробы ГДЕ Дата = &Д", map[string]any{"Д": day(1)}, "1"},
			{"равно справа", "ВЫБРАТЬ Часов ИЗ РегистрСведений.СменыПробы ГДЕ &Д = Дата", map[string]any{"Д": day(1)}, "1"},
			{"не меньше", "ВЫБРАТЬ Часов ИЗ РегистрСведений.СменыПробы ГДЕ Дата >= &Д", map[string]any{"Д": day(1)}, "1 2 31 32"},
			{"меньше", "ВЫБРАТЬ Часов ИЗ РегистрСведений.СменыПробы ГДЕ Дата < &Д", map[string]any{"Д": day(1)}, "0"},
			{"между", "ВЫБРАТЬ Часов ИЗ РегистрСведений.СменыПробы ГДЕ Дата МЕЖДУ &Начало И &Конец", march, "1 2 31"},
			{"алиас", "ВЫБРАТЬ С.Часов ИЗ РегистрСведений.СменыПробы КАК С ГДЕ С.Дата МЕЖДУ &Начало И &Конец", march, "1 2 31"},
			// Первый источник — документ со своим полем «Дата»: квалификатор
			// регистра решает, чей это формат.
			{"соединение с документом", `ВЫБРАТЬ Р.Часов ИЗ Документ.ТабельПробы КАК Т
				ЛЕВОЕ СОЕДИНЕНИЕ РегистрСведений.СменыПробы КАК Р ПО Р.Мастер = Т.Номер
				ГДЕ Р.Дата = &Д`, map[string]any{"Д": day(1)}, "1"},
			{"регистр накопления", "ВЫБРАТЬ Часов ИЗ РегистрНакопления.ПриходПробы ГДЕ Дата = &Д", map[string]any{"Д": day(1)}, "1"},
			{"регистр накопления, между", "ВЫБРАТЬ Р.Часов ИЗ РегистрНакопления.ПриходПробы КАК Р ГДЕ Р.Дата МЕЖДУ &Начало И &Конец", march, "1 2 31"},
			// Реквизит объекта по-прежнему сравнивается в своём формате (#462).
			{"реквизит документа", "ВЫБРАТЬ 1 КАК Часов ИЗ Документ.ТабельПробы ГДЕ Дата = &Д", map[string]any{"Д": day(1)}, "1"},
			{"реквизит документа в соединении", `ВЫБРАТЬ Р.Часов ИЗ РегистрСведений.СменыПробы КАК Р
				ЛЕВОЕ СОЕДИНЕНИЕ Документ.ТабельПробы КАК Т ПО Т.Номер = Р.Мастер
				ГДЕ Р.Дата = &Д И Т.Дата = &Д`, map[string]any{"Д": day(1)}, "1"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.Equal(t, tc.want, days(t, tc.text, tc.params))
			})
		}
	})
}
