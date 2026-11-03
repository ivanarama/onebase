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

// BETWEEN должен нормализовать сразу три операнда, даже если только одна
// граница — time.Time, а другая — строка RFC3339. Проверяем идентичности,
// включительность обеих границ и соседние микросекунды через публичный API.
func TestRegisterDateMixedBetweenBoundariesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "КонтекстДиапазона", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Момент", Type: metadata.FieldTypeString}}}
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{entity}))
		require.NoError(t, db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Момент": "строка"}, entity))
		ir := &metadata.InfoRegister{Name: "ДиапазоныДат",
			Dimensions: []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString}, {Name: "Момент", Type: metadata.FieldTypeDate}},
			Resources:  []metadata.Field{{Name: "Число", Type: metadata.FieldTypeNumber}}}
		reg := &metadata.Register{Name: "ДвиженияДиапазона", Dimensions: ir.Dimensions, Resources: ir.Resources}
		require.NoError(t, db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}))
		require.NoError(t, db.MigrateRegisters(ctx, []*metadata.Register{reg}))
		lower := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
		// Точное воспроизведение блокера ревью: только 11, 12, 13 часов,
		// нижняя граница time.Time, верхняя — строка UTC, результат только 12.
		original := &metadata.InfoRegister{Name: "ПроверкаДат", Dimensions: ir.Dimensions, Resources: ir.Resources}
		require.NoError(t, db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{original}))
		for _, hour := range []int{11, 12, 13} {
			require.NoError(t, db.InfoRegSet(ctx, original,
				map[string]any{"Метка": fmt.Sprint(hour), "Момент": fmt.Sprintf("2027-03-01T%02d:00:00Z", hour)},
				map[string]any{"Число": 1}, nil))
		}
		compiled, err := query.Compile("ВЫБРАТЬ Метка ИЗ РегистрСведений.ПроверкаДат КАК Р ГДЕ Р.Момент МЕЖДУ &Начало И &Конец",
			query.CompileOpts{InfoRegs: []*metadata.InfoRegister{original}, Dialect: db.Dialect(),
				Params: map[string]any{"Начало": lower, "Конец": "2027-03-01T12:30:00Z"}})
		require.NoError(t, err)
		rows, _, err := query.Run(ctx, db, &compiled)
		if db.Dialect().Name() == "postgres" {
			require.ErrorContains(t, err, "SQLSTATE 42883")
		} else {
			require.NoError(t, err, compiled.SQL)
			require.Len(t, rows, 1, compiled.SQL)
			require.Equal(t, "12", rows[0]["метка"])
		}
		upper := lower.Add(30*time.Minute + 123456*time.Microsecond)
		type record struct {
			label string
			date  time.Time
		}
		var records []record
		for _, format := range []string{"time", "utc", "offset"} {
			for _, date := range []time.Time{lower.Add(-time.Hour), lower.Add(-time.Second), lower, upper, upper.Add(time.Microsecond), lower.Add(time.Hour)} {
				// Штатная SQLite-запись time.Time хранит целые секунды;
				// дробные старые значения проверяем в строковых форматах.
				if format == "time" && date.Nanosecond() != 0 {
					continue
				}
				var value any = date
				switch format {
				case "utc":
					value = date.Format(time.RFC3339Nano)
				case "offset":
					value = date.In(time.FixedZone("MSK", 3*60*60)).Format(time.RFC3339Nano)
				}
				label := format + "/" + date.Format("15:04:05.999999")
				records = append(records, record{label, date})
				require.NoError(t, db.InfoRegSet(ctx, ir, map[string]any{"Метка": label, "Момент": value}, map[string]any{"Число": 1}, nil))
				require.NoError(t, db.WriteMovements(ctx, reg.Name, "Проверка", uuid.New(), []map[string]any{{
					"ВидДвижения": "Приход", "Метка": label, "Момент": value, "Число": 1,
				}}, reg, &lower))
			}
		}
		for _, source := range []string{"РегистрСведений.ДиапазоныДат", "РегистрНакопления.ДвиженияДиапазона"} {
			for _, mode := range []string{"bare", "alias", "join"} {
				from, field, label := source, "Момент", "Метка"
				if mode != "bare" {
					from += " КАК Р"
					field, label = "Р.Момент", "Р.Метка"
				}
				if mode == "join" {
					from = "Справочник.КонтекстДиапазона КАК Т ЛЕВОЕ СОЕДИНЕНИЕ " + from + " ПО 1 = 1"
				}
				for _, end := range []time.Time{lower.Add(30 * time.Minute), upper} {
					loString := lower.In(time.FixedZone("west", -5*60*60)).Format(time.RFC3339Nano)
					hiString := end.In(time.FixedZone("east", 3*60*60)).Format(time.RFC3339Nano)
					for _, tc := range []struct {
						name   string
						lo, hi any
					}{
						{"time-string-utc", lower, end.Format(time.RFC3339Nano)},
						{"time-string-offset", lower, hiString},
						{"string-time", loString, end},
						{"pointer-string", &lower, hiString},
						{"string-pointer", loString, &end},
						{"time-time", lower, end},
						{"pointer-pointer", &lower, &end},
					} {
						t.Run(fmt.Sprintf("%s/%s/%s/%s", source, mode, end.Format(time.RFC3339Nano), tc.name), func(t *testing.T) {
							predicate := field + " МЕЖДУ &Начало И &Конец"
							switch tc.name {
							case "time-string-offset":
								predicate = field + " BETWEEN &Начало AND &Конец ORDER BY " + label
							case "string-time":
								predicate = "(" + predicate + ") И 1 = 1"
							case "pointer-string":
								predicate += " И 1 = 1"
							case "string-pointer":
								predicate += " ИЛИ 1 = 0"
							}
							compiled, err := query.Compile("ВЫБРАТЬ "+label+" ИЗ "+from+" ГДЕ "+predicate,
								query.CompileOpts{Entities: []*metadata.Entity{entity}, InfoRegs: []*metadata.InfoRegister{ir}, Registers: []*metadata.Register{reg},
									Dialect: db.Dialect(), Params: map[string]any{"Начало": tc.lo, "Конец": tc.hi}})
							require.NoError(t, err)
							rows, _, err := query.Run(ctx, db, &compiled)
							if db.Dialect().Name() == "postgres" && strings.Contains(tc.name, "string") {
								// PostgreSQL явно типизирует строковый параметр как text.
								// Существующий отказ смешанного диапазона сохраняется;
								// эта доработка меняет только SQLite.
								require.ErrorContains(t, err, "SQLSTATE 42883", compiled.SQL)
								return
							}
							require.NoError(t, err, compiled.SQL)
							var want, got []string
							for _, r := range records {
								if !r.date.Before(lower) && !r.date.After(end) {
									want = append(want, r.label)
								}
							}
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

// Строковые сравнения и произвольные выражения границ не получают новую
// семантику момента: преобразование допустимо только для двух параметров.
func TestRegisterDateBetweenKeepsOtherExpressions(t *testing.T) {
	ir := &metadata.InfoRegister{Name: "Даты", Dimensions: []metadata.Field{{Name: "Момент", Type: metadata.FieldTypeDate}}}
	date := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, predicate string
		lo, hi          any
	}{
		{"strings", "Момент МЕЖДУ &Начало И &Конец", date.Format(time.RFC3339), date.Add(time.Hour).Format(time.RFC3339)},
		{"invalid string", "Момент МЕЖДУ &Начало И &Конец", date, "tomorrow"},
		{"upper expression", "Момент МЕЖДУ &Начало И &Конец + 1", date, date.Format(time.RFC3339)},
		{"lower expression", "Момент МЕЖДУ &Начало + 1 И &Конец", date.Format(time.RFC3339), date},
		{"literal", `Момент МЕЖДУ &Начало И "2027-03-01T13:00:00Z"`, date, date},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := query.Compile("ВЫБРАТЬ Момент ИЗ РегистрСведений.Даты ГДЕ "+tc.predicate,
				query.CompileOpts{Dialect: storage.SQLiteDialect{}, InfoRegs: []*metadata.InfoRegister{ir}, Params: map[string]any{"Начало": tc.lo, "Конец": tc.hi}})
			require.NoError(t, err)
			require.False(t, strings.Contains(compiled.SQL, "ob_utc_datetime"), compiled.SQL)
		})
	}
}
