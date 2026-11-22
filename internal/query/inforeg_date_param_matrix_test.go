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

// Имя ссылочного измерения может совпадать с алиасом другого регистра.
// В Р.Мастер.Дата средний сегмент остаётся переходом к справочнику:
// дата объекта должна сравниваться в своём формате при любом JOIN-алиасе.
func TestInfoRegisterReferenceDateAliasCollisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		master := &metadata.Entity{Name: "МастераПробы", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}}}
		entities := []*metadata.Entity{master}
		require.NoError(t, db.Migrate(ctx, entities))
		ir := &metadata.InfoRegister{Name: "СменыПробы",
			Dimensions: []metadata.Field{
				{Name: "Мастер", Type: "reference:МастераПробы", RefEntity: master.Name},
				{Name: "Дата", Type: metadata.FieldTypeDate},
			},
			Resources: []metadata.Field{{Name: "Часов", Type: metadata.FieldTypeNumber}},
		}
		require.NoError(t, db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}))
		date := time.Date(2027, 3, 1, 0, 0, 0, 0, time.FixedZone("Europe/Moscow", 3*60*60))
		id := uuid.New()
		require.NoError(t, db.Upsert(ctx, master.Name, id, map[string]any{"Дата": date}, master))
		require.NoError(t, db.InfoRegSet(ctx, ir,
			map[string]any{"Мастер": id, "Дата": date}, map[string]any{"Часов": 8}, nil))

		for _, alias := range []string{"Мастер", "Другой"} {
			for _, predicate := range []string{
				"Р.Мастер.Дата = &Д",
				"Р.Мастер.Дата >= &Д",
				"Р.Мастер.Дата МЕЖДУ &Д И &Конец",
			} {
				t.Run(alias+"/"+predicate, func(t *testing.T) {
					text := fmt.Sprintf(`ВЫБРАТЬ Р.Часов ИЗ РегистрСведений.СменыПробы КАК Р
						ЛЕВОЕ СОЕДИНЕНИЕ РегистрСведений.СменыПробы КАК %s ПО %s.Мастер = Р.Мастер
						ГДЕ %s И Р.Дата = &Д`, alias, alias, predicate)
					compiled, err := query.Compile(text, query.CompileOpts{
						Entities: entities, InfoRegs: []*metadata.InfoRegister{ir}, Dialect: db.Dialect(),
						Params: map[string]any{"Д": date, "Конец": date.Add(24 * time.Hour)},
					})
					require.NoError(t, err)
					rows, _, err := query.Run(ctx, db, &compiled)
					require.NoError(t, err, compiled.SQL)
					require.Len(t, rows, 1, compiled.SQL)
					require.Equal(t, "8", fmt.Sprint(rows[0]["часов"]))
				})
			}
		}
	})
}

// Публичная запись принимает и time.Time, и строку RFC3339. Старые строки
// обоих форматов могут одновременно находиться в одном регистре.
func TestRegisterDateMixedFormatsComparedToTimeParamMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		// Одноимённая строка главного источника не определяет тип даты JOIN.
		entity := &metadata.Entity{Name: "КонтекстДатПробы", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Момент", Type: metadata.FieldTypeString}}}
		entities := []*metadata.Entity{entity}
		require.NoError(t, db.Migrate(ctx, entities))
		require.NoError(t, db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Момент": "строка"}, entity))
		ir := &metadata.InfoRegister{Name: "МоментыПробы",
			Dimensions: []metadata.Field{
				{Name: "Метка", Type: metadata.FieldTypeString},
				{Name: "Момент", Type: metadata.FieldTypeDate},
			},
			Resources: []metadata.Field{{Name: "Число", Type: metadata.FieldTypeNumber}},
		}
		reg := &metadata.Register{Name: "ДатыДвиженийПробы",
			Dimensions: ir.Dimensions, Resources: ir.Resources}
		require.NoError(t, db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}))
		require.NoError(t, db.MigrateRegisters(ctx, []*metadata.Register{reg}))
		date := time.Date(2027, 3, 1, 0, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
		type record struct {
			label string
			date  time.Time
		}
		var records []record
		for _, format := range []string{"time", "utc", "offset"} {
			for _, seconds := range []int{-1, 0, 1} {
				moment := date.Add(time.Duration(seconds) * time.Second)
				var value any = moment
				switch format {
				case "utc":
					value = moment.UTC().Format(time.RFC3339)
				case "offset":
					value = moment.Format(time.RFC3339)
				}
				label := fmt.Sprintf("%s/%d", format, seconds)
				records = append(records, record{label, moment})
				require.NoError(t, db.InfoRegSet(ctx, ir,
					map[string]any{"Метка": label, "Момент": value}, map[string]any{"Число": 1}, nil))
				require.NoError(t, db.WriteMovements(ctx, reg.Name, "Тест", uuid.New(), []map[string]any{{
					"ВидДвижения": "Приход", "Метка": label, "Момент": value, "Число": 1,
				}}, reg, &date))
			}
		}
		// Не округлять старые строковые значения до секунд/миллисекунд:
		// PostgreSQL сохраняет микросекунды, а SQLite должен различать их тоже.
		fraction := date.Add(123456 * time.Microsecond)
		records = append(records, record{"fraction", fraction})
		require.NoError(t, db.InfoRegSet(ctx, ir,
			map[string]any{"Метка": "fraction", "Момент": fraction.Format(time.RFC3339Nano)},
			map[string]any{"Число": 1}, nil))
		require.NoError(t, db.WriteMovements(ctx, reg.Name, "Тест", uuid.New(), []map[string]any{{
			"ВидДвижения": "Приход", "Метка": "fraction", "Момент": fraction.Format(time.RFC3339Nano), "Число": 1,
		}}, reg, &date))

		for _, source := range []string{"РегистрСведений.МоментыПробы", "РегистрНакопления.ДатыДвиженийПробы"} {
			for _, mode := range []string{"bare", "alias", "join"} {
				field := "Момент"
				from := source
				if mode != "bare" {
					field = "Р.Момент"
					from += " КАК Р"
				}
				if mode == "join" {
					from = "Справочник.КонтекстДатПробы КАК Т ЛЕВОЕ СОЕДИНЕНИЕ " + from + " ПО 1 = 1"
				}
				for _, param := range []time.Time{date, fraction} {
					for _, op := range []string{"=", "<>", "!=", "<", "<=", ">", ">=", "МЕЖДУ", "reverse"} {
						t.Run(fmt.Sprintf("%s/%s/%s/%s", source, mode, param.Format(time.RFC3339Nano), op), func(t *testing.T) {
							predicate := field + " " + op + " &Д"
							if op == "reverse" {
								predicate = "&Д = " + field
							}
							if op == "МЕЖДУ" {
								predicate += " И &Конец"
							}
							var want []string
							for _, r := range records {
								cmp := r.date.Compare(param)
								match := (op == "=" || op == "reverse") && cmp == 0 ||
									(op == "<>" || op == "!=") && cmp != 0 ||
									op == "<" && cmp < 0 || op == "<=" && cmp <= 0 ||
									op == ">" && cmp > 0 || op == ">=" && cmp >= 0 ||
									op == "МЕЖДУ" && cmp >= 0 && !r.date.After(param.Add(time.Second))
								if match {
									want = append(want, r.label)
								}
							}
							// *time.Time проходит тот же публичный путь параметров.
							compiled, err := query.Compile("ВЫБРАТЬ Метка ИЗ "+from+" ГДЕ "+predicate,
								query.CompileOpts{Entities: entities, InfoRegs: []*metadata.InfoRegister{ir}, Registers: []*metadata.Register{reg},
									Dialect: db.Dialect(), Params: map[string]any{"Д": &param, "Конец": param.Add(time.Second)}})
							require.NoError(t, err)
							rows, _, err := query.Run(ctx, db, &compiled)
							require.NoError(t, err, compiled.SQL)
							var got []string
							for _, row := range rows {
								got = append(got, fmt.Sprint(row["метка"]))
							}
							sort.Strings(want)
							sort.Strings(got)
							require.Equal(t, want, got, compiled.SQL)
						})
					}
				}
			}
		}
	})
}
