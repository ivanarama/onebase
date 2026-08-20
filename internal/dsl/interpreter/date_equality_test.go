package interpreter_test

// Равенство дат (#1034).
//
// Дата из запроса приходит в UTC — на SQLite она хранится строкой RFC3339 и
// разбирается с зоной Z, — а литерал Дата(...) строится в локальной зоне. Один
// и тот же момент, разные значения: сравнение «=» шло через строковый ключ и
// давало Ложь, тогда как «<=» и «>=» (они хронологические) давали Истину.
//
// «Меньше либо равно истинно, больше либо равно истинно, а равно ложно» — не то
// поведение, которое конфигуратор ожидает и тем более проверяет: отбор
// «Если Стр.Дата = ДатаДокумента» молча не находил совпадений.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

// evalWithVars исполняет выражение DSL с инжектированными переменными — так же,
// как значения приходят в модуль из результата запроса.
func evalWithVars(t *testing.T, src string, vars map[string]any) any {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	var result any
	require.NoError(t, interpreter.New().RunWithResult(prog.Procedures[0], nil, &result, vars))
	return result
}

// sameMomentDifferentZones возвращает один момент в двух заведомо разных
// зонах. FixedZone делает регрессию детерминированной и на CI с локальной UTC:
// прежний вариант через time.Local там мог случайно сравнивать две UTC-даты.
func sameMomentDifferentZones() (time.Time, time.Time) {
	utc := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	moscow := utc.In(time.FixedZone("UTC+3", 3*60*60))
	return utc, moscow
}

func TestDateEquality_SameMomentDifferentZones(t *testing.T) {
	fromQuery, localValue := sameMomentDifferentZones()
	vars := map[string]any{"ИзЗапроса": fromQuery, "Локальная": localValue}

	t.Run("равно", func(t *testing.T) {
		src := `Функция Т()
  Возврат ИзЗапроса = Локальная;
КонецФункции`
		assert.Equal(t, true, evalWithVars(t, src, vars),
			"один и тот же момент в разных зонах должен быть равен")
	})

	t.Run("не равно даёт Ложь", func(t *testing.T) {
		src := `Функция Т()
  Возврат ИзЗапроса <> Локальная;
КонецФункции`
		assert.Equal(t, false, evalWithVars(t, src, vars))
	})

	// Согласованность с «<=» и «>=»: раньше они говорили «равны», а «=» — нет.
	t.Run("согласовано со сравнением", func(t *testing.T) {
		src := `Функция Т()
  Эталон = Локальная;
  Возврат Строка(ИзЗапроса <= Эталон) + "/" + Строка(ИзЗапроса >= Эталон) + "/" + Строка(ИзЗапроса = Эталон);
КонецФункции`
		assert.Equal(t, "true/true/true", evalWithVars(t, src, vars))
	})
}

// Разные моменты по-прежнему не равны: починка равенства не должна превратиться
// в «любые две даты равны».
func TestDateEquality_DifferentMomentsStillDiffer(t *testing.T) {
	fromQuery, _ := sameMomentDifferentZones()
	vars := map[string]any{"ИзЗапроса": fromQuery}
	src := `Функция Т()
  Возврат ИзЗапроса = Дата(2026, 3, 15, 9, 0, 1);
КонецФункции`
	assert.Equal(t, false, evalWithVars(t, src, vars))
}

// Дата и строка сравниваются как раньше: правило добавлено только для пары
// «дата и дата», чтобы не превратить сравнение с текстом в неожиданную истину.
func TestDateEquality_DateVersusStringUnchanged(t *testing.T) {
	fromQuery, _ := sameMomentDifferentZones()
	vars := map[string]any{"ИзЗапроса": fromQuery}
	src := `Функция Т()
  Возврат ИзЗапроса = "2026-03-15";
КонецФункции`
	assert.Equal(t, false, evalWithVars(t, src, vars))
}

// Ключи Соответствия используют ту же семантику равенства дат, что и «=»:
// вставка того же момента в другой зоне обновляет запись, а не создаёт дубль.
func TestDateEquality_MapKeySameMomentDifferentZones(t *testing.T) {
	fromQuery, localValue := sameMomentDifferentZones()
	vars := map[string]any{"ИзЗапроса": fromQuery, "Локальная": localValue}
	src := `Функция Т()
  Данные = Новый Соответствие;
  Данные.Вставить(ИзЗапроса, "первое");
  ДоЗамены = Данные.Получить(Локальная);
  Данные.Вставить(Локальная, "второе");
  Возврат ДоЗамены + "/" + Данные.Получить(ИзЗапроса) + "/" + Строка(Данные.Количество());
КонецФункции`
	assert.Equal(t, "первое/второе/1", evalWithVars(t, src, vars))
}
