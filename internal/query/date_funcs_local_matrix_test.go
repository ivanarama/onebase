package query_test

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// Календарные функции запроса обязаны видеть тот же местный день, что DSL и UI.
// Два пояса ловят оба направления перехода через UTC-границу, а январь и июль
// закрепляют зимнее/летнее смещение America/New_York. Тест идёт через публичные
// Compile и Run и одним телом проверяет SQLite и PostgreSQL.
func TestDateFunctionsUseApplicationLocalTime(t *testing.T) {
	tests := []struct {
		name      string
		zoneName  string
		localName string
	}{
		{name: "Asia/Kolkata", zoneName: "Asia/Kolkata"},
		{name: "America/New_York", zoneName: "America/New_York"},
		{name: "system Local without TZ", zoneName: "America/New_York", localName: "Local"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.zoneName)
			require.NoError(t, err, "загрузка часового пояса")
			if tt.localName != "" {
				loc = loadLocationNamed(t, tt.localName, tt.zoneName)
			}

			savedLocal := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = savedLocal })
			if tt.localName != "" {
				savedTZ, hadTZ := os.LookupEnv("TZ")
				require.NoError(t, os.Unsetenv("TZ"))
				t.Cleanup(func() {
					if hadTZ {
						require.NoError(t, os.Setenv("TZ", savedTZ))
					} else {
						require.NoError(t, os.Unsetenv("TZ"))
					}
				})
			}

			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				ent := &metadata.Entity{
					Name: "КалендарныеФункции",
					Kind: metadata.KindCatalog,
					Fields: []metadata.Field{
						{Name: "Наименование", Type: metadata.FieldTypeString},
						{Name: "Момент", Type: metadata.FieldTypeDate},
					},
				}
				require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}), "миграция")

				moments := map[string]time.Time{
					"Зима": localBoundaryMoment(loc, tt.zoneName, 2026, time.January),
					"Лето": localBoundaryMoment(loc, tt.zoneName, 2026, time.July),
				}
				for name, moment := range moments {
					require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
						"Наименование": name,
						"Момент":       moment,
					}, ent), "запись %s", name)
				}

				compiled, err := query.Compile(`
					ВЫБРАТЬ
						Наименование,
						Момент,
						Год(Момент) КАК ГодМомента,
						Месяц(Момент) КАК МесяцМомента,
						День(Момент) КАК ДеньМомента,
						НачалоДня(Момент) КАК НачалоДняМомента,
						КонецДня(Момент) КАК КонецДняМомента,
						НачалоМесяца(Момент) КАК НачалоМесяцаМомента,
						НачалоГода(Момент) КАК НачалоГодаМомента
					ИЗ Справочник.КалендарныеФункции`, query.CompileOpts{
					Entities: []*metadata.Entity{ent},
					Dialect:  db.Dialect(),
				})
				require.NoError(t, err, "компиляция")

				rows, _, err := query.Run(ctx, db, &compiled)
				require.NoError(t, err, "выполнение")
				require.Len(t, rows, len(moments), "строк результата")

				for _, row := range rows {
					name, ok := row["наименование"].(string)
					require.True(t, ok, "Наименование: %#v", row["наименование"])
					want, ok := moments[name]
					require.True(t, ok, "неожиданная строка %q", name)

					raw, ok := row["момент"].(time.Time)
					require.True(t, ok, "сырой Момент должен быть датой, получено %T", row["момент"])
					require.True(t, raw.Equal(want), "сырой Момент: %s != %s", raw, want)
					require.Equal(t, want.Year(), intValue(t, row["годмомента"]))
					require.Equal(t, int(want.Month()), intValue(t, row["месяцмомента"]))
					require.Equal(t, want.Day(), intValue(t, row["деньмомента"]))

					assertLocalClock(t, row["началоднямомента"], want.Year(), want.Month(), want.Day(), 0, 0, 0)
					assertLocalClock(t, row["конецднямомента"], want.Year(), want.Month(), want.Day(), 23, 59, 59)
					assertLocalClock(t, row["началомесяцамомента"], want.Year(), want.Month(), 1, 0, 0, 0)
					assertLocalClock(t, row["началогодамомента"], want.Year(), time.January, 1, 0, 0, 0)
				}
			})
		})
	}
}

func TestDateFunctionUsesQualifiedSourceFieldType(t *testing.T) {
	first := &metadata.Entity{
		Name: "Первый",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Значение", Type: metadata.FieldTypeDate},
		},
	}
	other := &metadata.Entity{
		Name: "Другой",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Значение", Type: metadata.FieldTypeString},
		},
	}

	compiled, err := query.Compile(`
		ВЫБРАТЬ
			День(Первый.Значение) КАК ДеньПервого,
			День(Другой.Значение) КАК ДеньДругого
		ИЗ Справочник.Первый КАК Первый
			ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Другой КАК Другой
			ПО Первый.Ссылка = Другой.Ссылка`, query.CompileOpts{
		Entities: []*metadata.Entity{first, other},
		Dialect:  storage.SQLiteDialect{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(compiled.SQL, "ob_local_datetime("), compiled.SQL)
	require.Contains(t, compiled.SQL, "ob_local_datetime(первый.значение)", compiled.SQL)
	require.NotContains(t, compiled.SQL, "ob_local_datetime(другой.значение)", compiled.SQL)
}

func TestDateFunctionScopesQualifiedSourceTypesInNestedSelect(t *testing.T) {
	dateEntity, stringEntity := dateFunctionScopeEntities()
	compiled, err := query.Compile(`
		ВЫБРАТЬ
			День(Т.Значение) КАК ДеньСтроки,
			(ВЫБРАТЬ День(Т.Значение) ИЗ Справочник.Даты КАК Т) КАК ДеньДаты
		ИЗ Справочник.Строки КАК Т`, query.CompileOpts{
		Entities: []*metadata.Entity{dateEntity, stringEntity},
		Dialect:  storage.SQLiteDialect{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(compiled.SQL, "ob_local_datetime("), compiled.SQL)
	require.Contains(t, compiled.SQL, "ob_local_datetime(т.значение)", compiled.SQL)
}

func TestDateFunctionScopesQualifiedSourceTypesAcrossUnion(t *testing.T) {
	dateEntity, stringEntity := dateFunctionScopeEntities()
	compiled, err := query.Compile(`
		ВЫБРАТЬ День(Т.Значение) ИЗ Справочник.Даты КАК Т
		ОБЪЕДИНИТЬ ВСЕ
		ВЫБРАТЬ День(Т.Значение) ИЗ Справочник.Строки КАК Т`, query.CompileOpts{
		Entities: []*metadata.Entity{dateEntity, stringEntity},
		Dialect:  storage.SQLiteDialect{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(compiled.SQL, "ob_local_datetime("), compiled.SQL)
	require.Contains(t, compiled.SQL, "ob_local_datetime(т.значение)", compiled.SQL)
}

// Неквалифицированное имя типизируется областью своего SELECT, а не первым
// источником всего потока. Здесь внешний Значение — строка, внутренний — дата,
// и первым в тексте встречается именно внутренний источник: до областной карты
// его тип решал и за внешнее поле, localizуя строку.
func TestDateFunctionScopesUnqualifiedFieldTypesInNestedSelect(t *testing.T) {
	dateEntity, stringEntity := dateFunctionScopeEntities()
	compiled, err := query.Compile(`
		ВЫБРАТЬ
			День(Значение) КАК ДеньСтроки,
			(ВЫБРАТЬ День(Значение) ИЗ Справочник.Даты) КАК ДеньДаты
		ИЗ Справочник.Строки`, query.CompileOpts{
		Entities: []*metadata.Entity{dateEntity, stringEntity},
		Dialect:  storage.SQLiteDialect{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(compiled.SQL, "ob_local_datetime("), compiled.SQL)
	require.Contains(t, compiled.SQL, "ob_local_datetime(значение)) AS integer) FROM даты", compiled.SQL)
	require.Contains(t, compiled.SQL, "strftime('%d', значение) AS integer) AS деньстроки", compiled.SQL)
}

// Ветки ОБЪЕДИНИТЬ — соседние области одного уровня. Дата локализуется в своей
// ветке, строка в соседней остаётся как записана.
func TestDateFunctionScopesUnqualifiedFieldTypesAcrossUnion(t *testing.T) {
	dateEntity, stringEntity := dateFunctionScopeEntities()
	compiled, err := query.Compile(`
		ВЫБРАТЬ День(Значение) ИЗ Справочник.Даты
		ОБЪЕДИНИТЬ ВСЕ
		ВЫБРАТЬ День(Значение) ИЗ Справочник.Строки`, query.CompileOpts{
		Entities: []*metadata.Entity{dateEntity, stringEntity},
		Dialect:  storage.SQLiteDialect{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(compiled.SQL, "ob_local_datetime("), compiled.SQL)
	require.Contains(t, compiled.SQL, "ob_local_datetime(значение)) AS integer) FROM даты", compiled.SQL)
}

// Одно имя с разными типами внутри одной области не угадывается: компиляция
// остаётся в прежней семантике, а не выбирает тип первого источника.
func TestDateFunctionLeavesAmbiguousUnqualifiedFieldUnlocalized(t *testing.T) {
	dateEntity, stringEntity := dateFunctionScopeEntities()
	compiled, err := query.Compile(`
		ВЫБРАТЬ День(Значение) КАК Д
		ИЗ Справочник.Даты
			ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Строки
			ПО Даты.Ссылка = Строки.Ссылка`, query.CompileOpts{
		Entities: []*metadata.Entity{dateEntity, stringEntity},
		Dialect:  storage.SQLiteDialect{},
	})
	require.NoError(t, err)
	require.NotContains(t, compiled.SQL, "ob_local_datetime(", compiled.SQL)
}

// Алиас виртуальной таблицы стоит после списка аргументов: …Остатки() КАК Ост.
// Доказанный date-тип измерения не должен теряться из-за такой формы алиаса —
// до исправления День(Ост.Момент) компилировался без ob_local_datetime.
func TestDateFunctionUsesVirtualTableAliasQualifiedSource(t *testing.T) {
	reg := &metadata.Register{
		Name: "События",
		Dimensions: []metadata.Field{
			{Name: "Момент", Type: metadata.FieldTypeDate},
		},
		Resources: []metadata.Field{
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		},
	}
	tests := []struct {
		name string
		src  string
	}{
		{name: "alias after empty args", src: `
			ВЫБРАТЬ День(Ост.Момент) КАК ДеньМомента
			ИЗ РегистрНакопления.События.Остатки() КАК Ост`},
		{name: "alias after arg list", src: `
			ВЫБРАТЬ День(Ост.Момент) КАК ДеньМомента
			ИЗ РегистрНакопления.События.Остатки(&Период) КАК Ост`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			compiled, err := query.Compile(tt.src, query.CompileOpts{
				Registers: []*metadata.Register{reg},
				Dialect:   storage.SQLiteDialect{},
			})
			require.NoError(t, err)
			require.Contains(t, compiled.SQL, "ob_local_datetime(ост.момент)", compiled.SQL)
		})
	}
}

func dateFunctionScopeEntities() (*metadata.Entity, *metadata.Entity) {
	dateEntity := &metadata.Entity{
		Name: "Даты",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Значение", Type: metadata.FieldTypeDate},
		},
	}
	stringEntity := &metadata.Entity{
		Name: "Строки",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Значение", Type: metadata.FieldTypeString},
		},
	}
	return dateEntity, stringEntity
}

func loadLocationNamed(t *testing.T, name, zoneName string) *time.Location {
	t.Helper()
	zones, err := zip.OpenReader(filepath.Join(zoneinfoRoot(t), "lib", "time", "zoneinfo.zip"))
	require.NoError(t, err, "открытие Go zoneinfo.zip")
	defer func() {
		require.NoError(t, zones.Close(), "закрытие Go zoneinfo.zip")
	}()
	for _, file := range zones.File {
		if file.Name != zoneName {
			continue
		}
		reader, err := file.Open()
		require.NoError(t, err, "открытие зоны %s", zoneName)
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		require.NoError(t, readErr, "чтение зоны %s", zoneName)
		require.NoError(t, closeErr, "закрытие зоны %s", zoneName)
		loc, err := time.LoadLocationFromTZData(name, data)
		require.NoError(t, err, "загрузка зоны %s с именем %s", zoneName, name)
		return loc
	}
	t.Fatalf("зона %s не найдена в Go zoneinfo.zip", zoneName)
	return nil
}

func zoneinfoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOROOT").Output()
	require.NoError(t, err, "определение GOROOT")
	root := strings.TrimSpace(string(out))
	require.NotEmpty(t, root, "go env GOROOT")
	return root
}

func localBoundaryMoment(loc *time.Location, zoneName string, year int, month time.Month) time.Time {
	if zoneName == "Asia/Kolkata" {
		// В положительной зоне местная полночь хранится предыдущим UTC-днём.
		return time.Date(year, month, 15, 0, 30, 0, 0, loc)
	}
	// В отрицательной зоне поздний вечер хранится следующим UTC-днём.
	return time.Date(year, month, 15, 23, 30, 0, 0, loc)
}

func intValue(t *testing.T, value any) int {
	t.Helper()
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		t.Fatalf("ожидалось число, получено %T (%#v)", value, value)
		return 0
	}
}

func assertLocalClock(t *testing.T, value any, year int, month time.Month, day, hour, minute, second int) {
	t.Helper()
	got, ok := query.ToDateValue(value)
	require.True(t, ok, "ожидалась дата, получено %T (%#v)", value, value)
	got = got.In(time.Local)
	require.Equal(t,
		fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", year, month, day, hour, minute, second),
		got.Format("2006-01-02 15:04:05"),
	)
}
