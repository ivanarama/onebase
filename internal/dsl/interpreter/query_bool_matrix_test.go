package interpreter_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// entityReg — QueryRegistry с настоящими сущностями (stubReg отдаёт nil и не
// годится для компиляции запроса к справочнику).
type entityReg struct{ entities []*metadata.Entity }

func (r *entityReg) Registers() []*metadata.Register               { return nil }
func (r *entityReg) InfoRegisters() []*metadata.InfoRegister       { return nil }
func (r *entityReg) AccountRegisters() []*metadata.AccountRegister { return nil }
func (r *entityReg) Entities() []*metadata.Entity                  { return r.entities }

func boolFlagCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "ПрофилиИзвлечения",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
			{ID: "f_flag", Name: "Активен", Type: metadata.FieldTypeBool},
		},
	}
}

// runOnDB прогоняет модуль поверх живой базы (запрос компилируется и исполняется
// по-настоящему, как в procrun), возвращая значение Возврат.
func runOnDB(t *testing.T, db *storage.DB, ent *metadata.Entity, src string) any {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err, "parse")
	require.NotEmpty(t, prog.Procedures)

	factory := interpreter.NewQueryFactory(context.Background(), db, &entityReg{entities: []*metadata.Entity{ent}})
	extra := map[string]any{
		"__factory_Запрос": factory,
		"__factory_Query":  factory,
	}
	var result any
	require.NoError(t, interpreter.New().RunWithResult(prog.Procedures[0], runtime.NewObject("Test", metadata.KindDocument), &result, extra))
	return result
}

// Регрессия к issue #704: булево, прочитанное из результата запроса, обязано
// быть ложным и в `Если`, и в `ЗначениеЗаполнено` — на обоих диалектах.
//
// Расхождение диалектов здесь и есть корень дефекта: PostgreSQL отдаёт булеву
// колонку как bool, SQLite хранит её в INTEGER и отдаёт int64. В DSL int64 не
// попадал ни в одну ветку truthy/isBlankVal и проваливался в «всё остальное —
// истина», поэтому `Ложь` из запроса выбирала ветку «истина» молча, без ошибки.
func TestQueryBoolFalseIsFalsy(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := boolFlagCatalog()
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))

		for _, r := range []struct {
			name string
			flag bool
		}{{"Активный", true}, {"Выключенный", false}} {
			require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(),
				map[string]any{"Наименование": r.name, "Активен": r.flag}, ent))
		}

		const src = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Наименование, Активен КАК А ИЗ Справочник.ПрофилиИзвлечения УПОРЯДОЧИТЬ ПО Наименование";
			Отчёт = "";
			Для Каждого Стр Из Запрос.Выполнить() Цикл
				Ветка = "нет";
				Если Стр.А Тогда
					Ветка = "да";
				КонецЕсли;
				Отчёт = Отчёт + Стр.Наименование + ":" + Ветка
					+ "/заполнено=" + Строка(ЗначениеЗаполнено(Стр.А))
					+ "/=Истина=" + Строка(Стр.А = Истина) + ";";
			КонецЦикла;
			Возврат Отчёт;
		КонецПроцедуры`

		assert.Equal(t,
			"Активный:да/заполнено=true/=Истина=true;Выключенный:нет/заполнено=false/=Истина=false;",
			runOnDB(t, db, ent, src))
	})
}

// Регрессия к issue #704: булев литерал в тексте запроса — это литерал, а не имя
// колонки. До фикса `Истина`/`Ложь` доезжали до SQL как идентификаторы, и самый
// естественный отбор падал с «no such column: истина» на обоих диалектах.
func TestQueryBoolLiteralInSQL(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := boolFlagCatalog()
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))

		for _, r := range []struct {
			name string
			flag bool
		}{{"Активный", true}, {"Выключенный", false}} {
			require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(),
				map[string]any{"Наименование": r.name, "Активен": r.flag}, ent))
		}

		cases := []struct {
			name  string
			query string
			want  string
		}{
			{"отбор по истине", "ГДЕ Активен = Истина", "Активный;"},
			{"отбор по лжи", "ГДЕ Активен = Ложь", "Выключенный;"},
			{"английский литерал", "ГДЕ Активен = TRUE", "Активный;"},
			{"поле как условие", "ГДЕ Активен", "Активный;"},
			{"отрицание поля", "ГДЕ НЕ Активен", "Выключенный;"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				src := `Процедура Тест()
					Запрос = Новый Запрос;
					Запрос.Текст = "ВЫБРАТЬ Наименование ИЗ Справочник.ПрофилиИзвлечения ` + c.query + ` УПОРЯДОЧИТЬ ПО Наименование";
					Отчёт = "";
					Для Каждого Стр Из Запрос.Выполнить() Цикл
						Отчёт = Отчёт + Стр.Наименование + ";";
					КонецЦикла;
					Возврат Отчёт;
				КонецПроцедуры`
				assert.Equal(t, c.want, runOnDB(t, db, ent, src))
			})
		}

		// Литерал в списке выборки: на SQLite это 0, на PostgreSQL — false;
		// ложным в DSL значение обязано быть одинаково.
		const selectSrc = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Ложь КАК Флаг ИЗ Справочник.ПрофилиИзвлечения ГДЕ Наименование = ""Активный""";
			Результат = Запрос.Выполнить();
			Если Результат[0].Флаг Тогда
				Возврат "истина";
			КонецЕсли;
			Возврат "ложь";
		КонецПроцедуры`
		assert.Equal(t, "ложь", runOnDB(t, db, ent, selectSrc))
	})
}
