package interpreter_test

// Ссылочная колонка результата запроса — ссылка, а не строка UUID (#1150).
//
// Матричный тест обязателен по природе значения: идентификатор ссылки хранится
// на PostgreSQL колонкой UUID, на SQLite — TEXT, и до DSL он доезжает разными
// путями. Раздельные тесты расхождения не показывают — каждый зелёный на своём
// движке, пока поведение молча разное (правило CLAUDE.md, повод — #607).
//
// Проверка идёт через публичную точку входа: модуль исполняется целиком поверх
// живой базы, ровно как в procrun. Дёргать оборачивание напрямую нельзя — ровно
// так в #611 зелёный тест покрывал код, который в продовом пути не вызывался.

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

func refQueryEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{
			Name: "Номенклатура",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{ID: "n_name", Name: "Наименование", Type: metadata.FieldTypeString},
			},
		},
		{
			Name: "РасходТовара",
			Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{ID: "d_num", Name: "Номер", Type: metadata.FieldTypeString},
				{ID: "d_nom", Name: "Номенклатура", Type: "reference:Номенклатура", RefEntity: "Номенклатура"},
			},
		},
	}
}

// runOnDBEntities — то же, что runOnDB, но с несколькими сущностями: ссылочная
// колонка без второй сущности не компилируется.
func runOnDBEntities(t *testing.T, db *storage.DB, ents []*metadata.Entity, src string) any {
	t.Helper()
	p := parser.New(lexer.New(src, "test.os"))
	prog, err := p.ParseProgram()
	require.NoError(t, err, "parse")
	require.NotEmpty(t, prog.Procedures)

	factory := interpreter.NewQueryFactory(context.Background(), db, &entityReg{entities: ents})
	extra := map[string]any{
		"__factory_Запрос": factory,
		"__factory_Query":  factory,
	}
	var result any
	require.NoError(t, interpreter.New().RunWithResult(
		prog.Procedures[0], runtime.NewObject("Test", metadata.KindDocument), &result, extra))
	return result
}

// seedRefQueryData заводит один документ с одной ссылкой на номенклатуру и
// возвращает идентификаторы обоих объектов.
func seedRefQueryData(t *testing.T, db *storage.DB, ents []*metadata.Entity) (docID, nomID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, db.Migrate(ctx, ents))
	nom, doc := ents[0], ents[1]
	nomID, docID = uuid.New(), uuid.New()
	require.NoError(t, db.Upsert(ctx, nom.Name, nomID,
		map[string]any{"наименование": "Гвозди"}, nom))
	require.NoError(t, db.Upsert(ctx, doc.Name, docID,
		map[string]any{"номер": "РТ-1", "номенклатура": nomID.String()}, doc))
	return docID, nomID
}

// ТипЗнч на колонке-ссылке обязан отдавать квалифицированное имя типа — то же,
// что обещает раздел руководства про Тип/ТипЗнч. До правки колонка приезжала
// строкой, и написанное по руководству сравнение молча не срабатывало.
func TestQueryRefColumn_ТипЗнчОтдаётСсылочныйТип(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ents := refQueryEntities()
		seedRefQueryData(t, db, ents)

		const src = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Ссылка, Номенклатура.Ссылка КАК НомСсылка ИЗ Документ.РасходТовара";
			Стр = Запрос.Выполнить()[0];
			Возврат ТипЗнч(Стр.Ссылка)
				+ "|" + Строка(ТипЗнч(Стр.Ссылка) = Тип("ДокументСсылка.РасходТовара"))
				+ "|" + ТипЗнч(Стр.НомСсылка)
				+ "|" + Строка(ТипЗнч(Стр.НомСсылка) = Тип("СправочникСсылка.Номенклатура"));
		КонецПроцедуры`

		assert.Equal(t,
			"ДокументСсылка.РасходТовара|true|СправочникСсылка.Номенклатура|true",
			runOnDBEntities(t, db, ents, src))
	})
}

// Совместимость — вторая половина правки, а не пожелание: сегодня конфигурации
// кладут колонку в параметр, сравнивают со строкой и печатают её. Сломайся
// что-то из этого — обёртка ломала бы рабочий код молча, ровно тем же способом,
// который она лечит.
func TestQueryRefColumn_СтароеПоведениеСохранено(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ents := refQueryEntities()
		docID, nomID := seedRefQueryData(t, db, ents)

		src := `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Ссылка, Номенклатура.Ссылка КАК НомСсылка ИЗ Документ.РасходТовара";
			Стр = Запрос.Выполнить()[0];

			// Печать и приведение к строке — прежний UUID.
			Отчёт = Строка(Стр.Ссылка) + "|" + (Стр.Ссылка + "");
			// Сравнение со строкой UUID.
			Отчёт = Отчёт + "|=" + Строка(Стр.Ссылка = "` + docID.String() + `");
			Отчёт = Отчёт + "|<>" + Строка(Стр.НомСсылка = "` + docID.String() + `");
			// Ссылка как значение параметра другого запроса.
			Втор = Новый Запрос;
			Втор.Текст = "ВЫБРАТЬ Наименование ИЗ Справочник.Номенклатура ГДЕ Ссылка = &Н";
			Втор.УстановитьПараметр("Н", Стр.НомСсылка);
			Отчёт = Отчёт + "|" + Втор.Выполнить()[0].Наименование;
			// JSON остаётся идентификатором, а не разложенной структурой.
			Отчёт = Отчёт + "|" + ЗаписатьJSON(Стр.НомСсылка);
			// Пустая ссылка распознаётся и в обёрнутом виде.
			Отчёт = Отчёт + "|" + Строка(ПустаяСсылка(Стр.Ссылка));
			Возврат Отчёт;
		КонецПроцедуры`

		assert.Equal(t,
			docID.String()+"|"+docID.String()+
				"|=true|<>false|Гвозди|\""+nomID.String()+"\"|false",
			runOnDBEntities(t, db, ents, src))
	})
}

// Представление ссылочного реквизита (голая `Номенклатура` в списке выборки)
// осталось строкой — и это не недоделка, а контракт компилятора: такая колонка
// разворачивается в наименование связанной сущности, ссылки в ней нет.
// Тест закрепляет границу, чтобы её не потеряли при следующей правке.
func TestQueryRefColumn_ПредставлениеОстаётсяСтрокой(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ents := refQueryEntities()
		seedRefQueryData(t, db, ents)

		const src = `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "ВЫБРАТЬ Номенклатура ИЗ Документ.РасходТовара";
			Стр = Запрос.Выполнить()[0];
			Возврат ТипЗнч(Стр.Номенклатура) + "|" + Стр.Номенклатура;
		КонецПроцедуры`

		assert.Equal(t, "Строка|Гвозди", runOnDBEntities(t, db, ents, src))
	})
}
