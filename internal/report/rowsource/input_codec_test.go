package rowsource

import (
	"github.com/shopspring/decimal"
	"strings"
	"testing"
)

func TestRowSourceAdditionalQueryInputCodec(t *testing.T) {
	t.Run("join_number_matches_initial_number", func(t *testing.T) {
		ctx, db, reg, _, src, _ := fixture(t)
		src.Code = `Функция Build() Экспорт
   Для Каждого Р Из Результат Цикл
    З=Новый Запрос;
    З.Текст="ВЫБРАТЬ Р.Amount КАК A ИЗ Документ.RowRequest КАК Р ВНУТРЕННЕЕ СОЕДИНЕНИЕ Документ.RowRequest.Works КАК Т ПО Т.Ссылка=Р.Ссылка";
    Для Каждого Д Из З.Выполнить() Цикл
     Если ТипЗнч(Д.A) = ТипЗнч(Р.Amount) Тогда Р.Вставить("Works","same"); Иначе Р.Вставить("Works","wrong_type"); КонецЕсли;
    КонецЦикла;
   КонецЦикла;
   Возврат Результат;
  КонецФункции`
		result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rows) != 1 || result.Rows[0]["Works"] != "same" {
			t.Fatalf("additional JOIN Query lost proven number type: %v", result.Rows)
		}
	})
	t.Run("additional_date_null_stays_null", func(t *testing.T) {
		ctx, db, reg, ent, src, id := fixture(t)
		if err := db.Upsert(ctx, ent.Name, id, map[string]any{"Label": "safe", "Note": "", "Amount": decimal.NewFromInt(1), "Count": decimal.NewFromInt(1), "Moment": nil, "Owner": "reader"}, ent); err != nil {
			t.Fatal(err)
		}
		src.Code = `Функция Build() Экспорт
   Для Каждого Р Из Результат Цикл
    З=Новый Запрос;
    З.Текст="ВЫБРАТЬ Р.Moment КАК M ИЗ Документ.RowRequest КАК Р";
    Для Каждого Д Из З.Выполнить() Цикл
     Если Д.M = Неопределено Тогда Р.Вставить("Works","null"); Иначе Р.Вставить("Works","not_null"); КонецЕсли;
    КонецЦикла;
   КонецЦикла;
   Возврат Результат;
  КонецФункции`
		result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rows) != 1 || result.Rows[0]["Works"] != "null" {
			t.Fatalf("additional Query changed SQL NULL before DSL: %v", result.Rows)
		}
	})
}

func TestRowSourceQueryReferenceAndDateCodec(t *testing.T) {
	ctx, db, reg, _, src, _ := fixture(t)
	src.Code = `Функция Build() Экспорт
  Для Каждого Р Из Результат Цикл
   З=Новый Запрос;З.Текст="ВЫБРАТЬ Р.Ссылка, Р.Moment КАК M ИЗ Документ.RowRequest КАК Р ГДЕ Р.Ссылка=&ID";
   З.УстановитьПараметр("ID",Р.Request);
   Для Каждого Д Из З.Выполнить() Цикл
    Если ТипЗнч(Д.Ссылка)=ТипЗнч(Р.Request) И Д.Ссылка=Р.Request И Д.M=Р.Moment Тогда Р.Вставить("Works","same");Иначе Р.Вставить("Works","changed");КонецЕсли;
   КонецЦикла;
  КонецЦикла;Возврат Результат;
 КонецФункции`
	result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows[0]["Works"] != "same" {
		t.Fatalf("reference root/identity/date changed: %v", result.Rows)
	}
	// Initial and additional query expose the same proven implicit reference aliases.
	query := strings.Replace(initialQuery, "Р.Ссылка КАК Request", "Р.Ссылка", 1)
	src.Code = `Функция Build() Экспорт Для Каждого Р Из Результат Цикл Р.Вставить("Works",Строка(Р.Ссылка));КонецЦикла;Возврат Результат;КонецФункции`
	result, err = Run(ctx, db, reg, Query{Text: query}, src)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows[0]["Works"] != result.Rows[0]["id"] {
		t.Fatal("initial implicit reference alias lost")
	}
	src.Extras[0].Name = "Ссылка"
	if result, err = Run(ctx, db, reg, Query{Text: query}, src); err == nil || result != nil {
		t.Fatal("extra overwrote compiler-proven DSL alias")
	}
}

func TestRowSourceQueryArrayParameters(t *testing.T) {
	ctx, db, reg, _, src, _ := fixture(t)
	src.Code = `Функция Build() Экспорт
  Для Каждого Р Из Результат Цикл
   З=Новый Запрос;З.Текст="ВЫБРАТЬ Р.Amount КАК A ИЗ Документ.RowRequest КАК Р ГДЕ Р.Ссылка В (&IDs)";
   М=Новый Массив;М.Добавить(Р.Request);З.УстановитьПараметр("IDs",М);
   Для Каждого Д Из З.Выполнить() Цикл
    Если Д.A=Р.Amount Тогда Р.Вставить("Works","same");КонецЕсли;
   КонецЦикла;
  КонецЦикла;Возврат Результат;
 КонецФункции`
	result, err := Run(ctx, db, reg, Query{Text: initialQuery}, src)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows[0]["Works"] != "same" {
		t.Fatal("typed ref array parameter lost")
	}
	src.Code = strings.Replace(src.Code, "М.Добавить(Р.Request)", "М.Добавить(М)", 1)
	if result, err = Run(ctx, db, reg, Query{Text: initialQuery}, src); err == nil || result != nil {
		t.Fatal("cyclic parameter accepted")
	}
}
