package interpreter_test

// ЗаписатьJSON над ссылкой ОТ МЕНЕДЖЕРА отдаёт идентификатор, а не структуру
// (#1353).
//
// Ветка `case *Ref` в valueToJSONSeen появилась вместе с обёрткой ссылочной
// колонки запроса (#1150) и действует на ЛЮБУЮ ссылку. Ссылка от менеджера —
// тот же тип, и до правки она проваливалась в default и маршалилась структурой
// `{"UUID":…,"Name":"Гвозди",…}`. Это ровно тот случай, ради которого правку и
// делали, но закреплён тестом был только путь из запроса: интеграция, читавшая
// `Name` из такого JSON, сломалась бы молча — и обратная регрессия, сужение
// ветки до колонок запроса, тоже не была бы поймана.
//
// Матричный тест по природе значения: идентификатор хранится на PostgreSQL
// колонкой UUID, на SQLite — TEXT, и до DSL доезжает разными путями.
//
// Менеджер инжектится тем же конструктором, что и продовый путь
// (`internal/ui/handlers_dsl.go` → `interpreter.NewCatalogsRoot`): иначе тест
// снова закрепил бы не тот путь — повод тот же, что у #611.

import (
	"context"
	"strings"
	"testing"

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

// runOnDBWithCatalogs — как runOnDBEntities, но в окружении есть ещё и
// `Справочники`: без него ссылку от менеджера взять неоткуда.
func runOnDBWithCatalogs(t *testing.T, db *storage.DB, ents []*metadata.Entity, src string) any {
	t.Helper()
	p := parser.New(lexer.New(src, "test.os"))
	prog, err := p.ParseProgram()
	require.NoError(t, err, "parse")
	require.NotEmpty(t, prog.Procedures)

	reg := &entityReg{entities: ents}
	factory := interpreter.NewQueryFactory(context.Background(), db, reg)
	catalogs := interpreter.NewCatalogsRoot(interpreter.NewStaticCtx(context.Background()), db, reg)
	extra := map[string]any{
		"__factory_Запрос": factory,
		"__factory_Query":  factory,
		"Справочники":      catalogs,
		"Catalogs":         catalogs,
	}
	var result any
	require.NoError(t, interpreter.New().RunWithResult(
		prog.Procedures[0], runtime.NewObject("Test", metadata.KindDocument), &result, extra))
	return result
}

func TestJSONManagerRef_ОтдаётИдентификаторАНеСтруктуру(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ents := refQueryEntities()
		_, nomID := seedRefQueryData(t, db, ents)

		const src = `Процедура Тест()
			Ссылка = Справочники.Номенклатура.НайтиПоНаименованию("Гвозди");
			Возврат ЗаписатьJSON(Ссылка);
		КонецПроцедуры`

		got := runOnDBWithCatalogs(t, db, ents, src)
		assert.Equal(t, "\""+nomID.String()+"\"", got)
		// Отдельная проверка именно той формы, которая ломала интеграции:
		// сообщение «нет Name» точнее, чем расхождение двух длинных строк.
		if s, ok := got.(string); ok && strings.Contains(s, "\"Name\"") {
			t.Errorf("ссылка от менеджера снова маршалится структурой: %s", s)
		}
	})
}

// Обратная граница: ссылка от менеджера и колонка результата запроса дают
// ОДИН и тот же JSON. Сужение ветки `case *Ref` до колонок запроса разведёт их
// и сломает этот тест — ради этого он и написан.
func TestJSONManagerRef_СовпадаетСКолонкойЗапроса(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ents := refQueryEntities()
		seedRefQueryData(t, db, ents)

		const src = `Процедура Тест()
			ОтМенеджера = Справочники.Номенклатура.НайтиПоНаименованию("Гвозди");
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Номенклатура.Ссылка КАК НомСсылка ИЗ Документ.РасходТовара";
			ИзЗапроса = Запрос.Выполнить()[0].НомСсылка;
			Возврат ЗаписатьJSON(ОтМенеджера) + "|" + ЗаписатьJSON(ИзЗапроса);
		КонецПроцедуры`

		got, ok := runOnDBWithCatalogs(t, db, ents, src).(string)
		require.True(t, ok, "результат обязан быть строкой")
		parts := strings.SplitN(got, "|", 2)
		require.Len(t, parts, 2)
		assert.Equal(t, parts[1], parts[0],
			"JSON ссылки от менеджера разошёлся с JSON колонки запроса")
	})
}

// НайтиПоРеквизиту получает ссылку от менеджера через CatalogProxy.CallMethod
// и findByField. Проверяем, что ЗаписатьJSON сериализует найденную ссылку
// в идентификатор.
func TestJSONManagerRef_НайтиПоРеквизитуТожеИдентификатор(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ents := refQueryEntities()
		_, nomID := seedRefQueryData(t, db, ents)

		const src = `Процедура Тест()
			Ссылка = Справочники.Номенклатура.НайтиПоРеквизиту("Наименование", "Гвозди");
			Возврат ЗаписатьJSON(Ссылка);
		КонецПроцедуры`

		assert.Equal(t, "\""+nomID.String()+"\"", runOnDBWithCatalogs(t, db, ents, src))
	})
}
