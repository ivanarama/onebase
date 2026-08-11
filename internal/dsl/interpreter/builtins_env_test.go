package interpreter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Проверяем тем же путём, что и пользователь: исходник модуля → лексер →
// парсер → интерпретатор.
func evalEnv(t *testing.T, src string) any {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	interp := interpreter.New()
	obj := runtime.NewObject("Test", metadata.KindDocument)
	var result any
	require.NoError(t, interp.RunWithResult(prog.Procedures[0], obj, &result))
	return result
}

func TestDSL_ВременныйФайл_ИмяУникальноИЛежитВКаталоге(t *testing.T) {
	src := `Процедура Тест()
		Первый = ПолучитьИмяВременногоФайла("xml");
		Второй = ПолучитьИмяВременногоФайла("xml");
		Возврат Первый + "|" + Второй;
	КонецПроцедуры`
	out, _ := evalEnv(t, src).(string)
	parts := strings.Split(out, "|")
	require.Len(t, parts, 2)
	assert.NotEqual(t, parts[0], parts[1], "имена временных файлов должны различаться")
	for _, p := range parts {
		assert.Equal(t, ".xml", filepath.Ext(p), "расширение не применилось: %s", p)
		assert.Equal(t, os.TempDir(), filepath.Dir(p), "файл не во временном каталоге: %s", p)
	}
	// Сам файл не создаётся — как в 1С: возвращается только имя.
	assert.NoFileExists(t, parts[0])
}

// Расширение принимается и с точкой, и без: в переносимом коде встречаются оба.
func TestDSL_ВременныйФайл_РасширениеСТочкойИБез(t *testing.T) {
	src := `Процедура Тест()
		Возврат ПолучитьИмяВременногоФайла(".log");
	КонецПроцедуры`
	out, _ := evalEnv(t, src).(string)
	assert.Equal(t, ".log", filepath.Ext(out))
	assert.NotContains(t, filepath.Base(out), "..")
}

func TestDSL_КаталогВременныхФайловИРазделитель(t *testing.T) {
	src := `Процедура Тест()
		Возврат КаталогВременныхФайлов() + "|" + ПолучитьРазделительПути();
	КонецПроцедуры`
	out, _ := evalEnv(t, src).(string)
	parts := strings.SplitN(out, "|", 2)
	require.Len(t, parts, 2)
	assert.Equal(t, os.TempDir(), parts[0])
	assert.Equal(t, string(filepath.Separator), parts[1])
}

// Поведение сверено прогоном на платформе 1С 8.3 (fastex на живой базе), а не
// по документации. Оттуда же взяты ожидаемые строки.
func TestDSL_КодированиеСовпадаетС1С(t *testing.T) {
	src := `Процедура Тест()
		Возврат КодироватьСтроку("а б&в");
	КонецПроцедуры`
	// 1С: КодироватьСтроку("а б&в", КодировкаURL) → %D0%B0%20%D0%B1%26%D0%B2
	assert.Equal(t, "%D0%B0%20%D0%B1%26%D0%B2", evalEnv(t, src))
}

// Пробел кодируется как %20, а не «+»: url.QueryEscape здесь не годится.
func TestDSL_ПробелКодируетсяКак20(t *testing.T) {
	src := `Процедура Тест()
		Возврат КодироватьСтроку("a b");
	КонецПроцедуры`
	assert.Equal(t, "a%20b", evalEnv(t, src))
}

// Набор незакодированных символов в обоих режимах — как у платформы.
func TestDSL_НаборНезакодированныхСимволов(t *testing.T) {
	src := `Процедура Тест()
		Возврат КодироватьСтроку("-_.~!*()'+/:@?#[]$,;= ");
	КонецПроцедуры`
	assert.Equal(t, "-_.~%21%2A%28%29%27%2B%2F%3A%40%3F%23%5B%5D%24%2C%3B%3D%20", evalEnv(t, src))

	src2 := `Процедура Тест()
		Возврат КодироватьСтроку("-_.~!*()'+/:@?#[]$,;= ", "URLВКодировкеURL");
	КонецПроцедуры`
	assert.Equal(t, "-_.~!*()'+/:@?#[]$,;=%20", evalEnv(t, src2))
}

// «+» при раскодировании остаётся плюсом: проверено на 1С —
// РаскодироватьСтроку("a+b") возвращает «a+b», а не «a b».
func TestDSL_ПлюсНеСтановитсяПробелом(t *testing.T) {
	src := `Процедура Тест()
		Возврат РаскодироватьСтроку("a+b");
	КонецПроцедуры`
	assert.Equal(t, "a+b", evalEnv(t, src))
}

func TestDSL_РаскодированиеКириллицы(t *testing.T) {
	src := `Процедура Тест()
		Возврат РаскодироватьСтроку("%D0%9C");
	КонецПроцедуры`
	assert.Equal(t, "М", evalEnv(t, src))
}

// Кодирование и раскодирование обязаны быть взаимно обратны — иначе параметр
// молча искажается по дороге.
func TestDSL_КодированиеОбратимо(t *testing.T) {
	src := `Процедура Тест()
		Исходная = "значение с пробелом, & и кириллицей";
		Возврат РаскодироватьСтроку(КодироватьСтроку(Исходная)) = Исходная;
	КонецПроцедуры`
	assert.Equal(t, true, evalEnv(t, src))
}

func TestDSL_РаскодированиеБитойСтрокиНеПадает(t *testing.T) {
	src := `Процедура Тест()
		Возврат РаскодироватьСтроку("%zz неполный процент %");
	КонецПроцедуры`
	out, _ := evalEnv(t, src).(string)
	assert.Contains(t, out, "%zz")
}

func TestDSL_РаскодированиеНеВозвращаетНевалидныйUTF8(t *testing.T) {
	src := `Процедура Тест()
		Возврат РаскодироватьСтроку("%FF");
	КонецПроцедуры`
	assert.Equal(t, "%FF", evalEnv(t, src))
}

func TestDSL_ОстатокОтДеления(t *testing.T) {
	src := `Процедура Тест()
		Возврат Строка(7 % 2) + "|" + Строка(10 % 5) + "|" + Строка(-7 % 2);
	КонецПроцедуры`
	out, _ := evalEnv(t, src).(string)
	assert.Equal(t, "1|0|-1", out)
}

func TestDSL_ОстатокПриоритетКакУУмножения(t *testing.T) {
	src := `Процедура Тест()
		Возврат 1 + 7 % 3;
	КонецПроцедуры`
	// 7 % 3 = 1, затем + 1 → 2. Если бы приоритет был ниже сложения, вышло бы 8 % 3 = 2 —
	// поэтому проверяем ещё и вариант, где результаты расходятся.
	assert.EqualValues(t, 2, toInt(t, evalEnv(t, src)))

	src2 := `Процедура Тест()
		Возврат 10 + 9 % 4;
	КонецПроцедуры`
	// 9 % 4 = 1 → 11; при неверном приоритете (19 % 4) вышло бы 3.
	assert.EqualValues(t, 11, toInt(t, evalEnv(t, src2)))
}

func TestDSL_ОстатокДелениеНаНоль(t *testing.T) {
	src := `Процедура Тест()
		Попытка
			Возврат 5 % 0;
		Исключение
			Возврат "поймано";
		КонецПопытки;
	КонецПроцедуры`
	assert.Equal(t, "поймано", evalEnv(t, src))
}

func toInt(t *testing.T, v any) int64 {
	t.Helper()
	type intLike interface{ IntPart() int64 }
	switch x := v.(type) {
	case intLike:
		return x.IntPart()
	case int64:
		return x
	case float64:
		return int64(x)
	}
	t.Fatalf("неожиданный тип результата: %T", v)
	return 0
}

// Деление на ноль в остатке должно вести себя как в обычном делении: ошибка
// возникает, только когда операция применима. Иначе «строка % 0» падала бы
// делением на ноль там, где «строка / 0» молча даёт Неопределено.
func TestDSL_ОстатокНечисловойОперандНеПадаетДелениемНаНоль(t *testing.T) {
	src := `Процедура Тест()
		Попытка
			Возврат "abc" % 0;
		Исключение
			Возврат "исключение";
		КонецПопытки;
	КонецПроцедуры`
	assert.NotEqual(t, "исключение", evalEnv(t, src),
		"нечисловой операнд не должен приводить к ошибке деления на ноль")
}

// Остаток десятичный, без усечения операндов.
func TestDSL_ОстатокДробный(t *testing.T) {
	src := `Процедура Тест()
		Возврат Строка(7.5 % 2);
	КонецПроцедуры`
	assert.Equal(t, "1.5", evalEnv(t, src))
}

func TestDSL_ОстатокИДелениеОтклоняютОпасныеDecimal(t *testing.T) {
	for _, op := range []string{"%", "/"} {
		src := `Процедура Тест()
			Большое = Число("1e2147483647");
			Малое = Число("1e-2147483648");
			Попытка
				Результат = Большое ` + op + ` Малое;
				Возврат "операция прошла";
			Исключение
				Возврат ОписаниеОшибки();
			КонецПопытки;
		КонецПроцедуры`
		out, _ := evalEnv(t, src).(string)
		assert.Contains(t, out, "безопасного диапазона", "оператор %s", op)
	}
}

func TestDSL_СоставноеДелениеОтклоняетОпасныеDecimal(t *testing.T) {
	src := `Процедура Тест()
		Большое = Число("1e2147483647");
		Малое = Число("1e-2147483648");
		Попытка
			Большое /= Малое;
			Возврат "операция прошла";
		Исключение
			Возврат ОписаниеОшибки();
		КонецПопытки;
	КонецПроцедуры`
	out, _ := evalEnv(t, src).(string)
	assert.Contains(t, out, "безопасного диапазона")
}
