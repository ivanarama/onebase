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
func evalEnv(t *testing.T, src string, extraVars ...map[string]any) any {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	interp := interpreter.New()
	obj := runtime.NewObject("Test", metadata.KindDocument)
	var result any
	require.NoError(t, interp.RunWithResult(prog.Procedures[0], obj, &result, extraVars...))
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

func TestDSL_ВременныйФайл_БезАргументаПолучаетTMP(t *testing.T) {
	for _, fn := range []string{"ПолучитьИмяВременногоФайла", "GetTempFileName"} {
		for _, tc := range []struct {
			name string
			args string
			ext  string
		}{
			{name: "omitted", ext: ".tmp"},
			{name: "undefined", args: "Неопределено", ext: ".tmp"},
			{name: "explicit empty", args: `""`, ext: ""},
		} {
			t.Run(fn+"/"+tc.name, func(t *testing.T) {
				src := `Процедура Тест()
					Возврат ` + fn + `(` + tc.args + `);
				КонецПроцедуры`
				out, _ := evalEnv(t, src).(string)
				assert.Equal(t, tc.ext, filepath.Ext(out))
			})
		}
	}
}

func TestDSL_КаталогВременныхФайловИРазделитель(t *testing.T) {
	src := `Процедура Тест()
		Возврат КаталогВременныхФайлов() + "|" + ПолучитьРазделительПути();
	КонецПроцедуры`
	out, _ := evalEnv(t, src).(string)
	parts := strings.SplitN(out, "|", 2)
	require.Len(t, parts, 2)
	wantDir := os.TempDir()
	if !strings.HasSuffix(wantDir, string(filepath.Separator)) {
		wantDir += string(filepath.Separator)
	}
	assert.Equal(t, wantDir, parts[0])
	assert.Equal(t, string(filepath.Separator), parts[1])
}

func TestDSL_КаталогВременныхФайловПоддерживаетПрямуюКонкатенацию(t *testing.T) {
	src := `Процедура Тест()
		Возврат КаталогВременныхФайлов() + "probe.tmp";
	КонецПроцедуры`
	assert.Equal(t, filepath.Join(os.TempDir(), "probe.tmp"), evalEnv(t, src))
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

func TestDSL_СпособКодированияСтрокиСовместимС1С(t *testing.T) {
	for _, mode := range []string{
		"СпособКодированияСтроки.URLВКодировкеURL",
		"URLВКодировкеURL",
		"StringEncodingMethod.URLInURLCoding",
		"URLInURLCoding",
		"StringEncodingMethod.URLInURLEncoding",
		"URLInURLEncoding",
	} {
		src := `Процедура Тест()
			Возврат КодироватьСтроку("https://example.test/a b?q=x y", ` + mode + `);
		КонецПроцедуры`
		assert.Equal(t, "https://example.test/a%20b?q=x%20y", evalEnv(t, src), mode)
	}
}

func TestDSL_РежимКомпонентаИзПеречисления(t *testing.T) {
	for _, mode := range []string{
		"СпособКодированияСтроки.КодировкаURL",
		"КодировкаURL",
		"StringEncodingMethod.URLEncoding",
		"URLEncoding",
	} {
		src := `Процедура Тест()
			Возврат КодироватьСтроку("a/b", ` + mode + `);
		КонецПроцедуры`
		assert.Equal(t, "a%2Fb", evalEnv(t, src), mode)
	}
}

func TestDSL_UTF8ТретийАргументПоддерживается(t *testing.T) {
	for _, encoding := range []string{
		"КодировкаТекста.UTF8",
		"TextEncoding.UTF8",
		`"UTF-8"`,
		`"utf_8"`,
	} {
		src := `Процедура Тест()
			Возврат РаскодироватьСтроку(КодироватьСтроку("а/б", КодировкаURL, ` + encoding + `), КодировкаURL, ` + encoding + `);
		КонецПроцедуры`
		assert.Equal(t, "а/б", evalEnv(t, src), encoding)
	}
}

func TestDSL_ОшибкиФункцийОкруженияЛовятсяСПозицией(t *testing.T) {
	tests := []struct {
		name string
		call string
		want string
	}{
		{name: "invalid extension", call: `ПолучитьИмяВременногоФайла("../txt")`, want: "расширени"},
		{name: "missing encode argument", call: `КодироватьСтроку()`, want: "первый аргумент"},
		{name: "missing decode argument", call: `DecodeString()`, want: "первый аргумент"},
		{name: "undefined encode argument", call: `EncodeString(Неопределено)`, want: "первый аргумент"},
		{name: "unknown mode", call: `EncodeString("a/b", "URLВКодировкеUR1")`, want: "неизвестный способ"},
		{name: "unknown enum member", call: `КодироватьСтроку("a/b", СпособКодированияСтроки.URLВКодировкеUR1)`, want: "способ URL-кодирования"},
		{name: "unsupported encoding", call: `КодироватьСтроку("текст", КодировкаURL, "Windows-1251")`, want: "неподдерживаемая кодировка"},
		{name: "temp dir extra argument", call: `TempFilesDir(1)`, want: "0 аргументов"},
		{name: "temp name extra argument", call: `GetTempFileName("tmp", "лишний")`, want: "не более 1 аргумента"},
		{name: "separator extra argument", call: `GetPathSeparator(1)`, want: "0 аргументов"},
		{name: "encode extra argument", call: `EncodeString("a", КодировкаURL, UTF8, "лишний")`, want: "не более 3 аргументов"},
		{name: "decode extra argument", call: `DecodeString("a", КодировкаURL, UTF8, "лишний")`, want: "не более 3 аргументов"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := `Процедура Тест()
				Попытка
					` + tc.call + `;
					Возврат "не поймано";
				Исключение
					Возврат ИнформацияОбОшибке();
				КонецПопытки;
			КонецПроцедуры`
			result := evalEnv(t, src)
			info, ok := result.(*interpreter.Struct)
			require.True(t, ok, "ошибка не перехвачена: %#v", result)
			assert.Equal(t, "test.os", info.Get("Источник"))
			assert.Greater(t, info.Get("НомерСтроки").(float64), float64(0))
			assert.Contains(t, info.Get("Описание"), tc.want)
		})
	}
}

func TestDSL_ПустойСпособКодированияНеСтановитсяРежимомПоУмолчанию(t *testing.T) {
	for _, fn := range []string{"КодироватьСтроку", "EncodeString", "РаскодироватьСтроку", "DecodeString"} {
		for _, tc := range []struct {
			name  string
			mode  string
			extra map[string]any
		}{
			{name: "empty", mode: `""`},
			{name: "whitespace", mode: `"   "`},
			{name: "empty MapThis", mode: "Режим", extra: map[string]any{
				"Режим": &interpreter.MapThis{M: map[string]any{}},
			}},
		} {
			t.Run(fn+"/"+tc.name, func(t *testing.T) {
				src := `Процедура Тест()
					Попытка
						` + fn + `("a%2Fb", ` + tc.mode + `);
						Возврат "не поймано";
					Исключение
						Возврат ОписаниеОшибки();
					КонецПопытки;
				КонецПроцедуры`
				result := evalEnv(t, src, tc.extra)
				assert.Contains(t, result, "неизвестный способ URL-кодирования")
			})
		}
	}
}

func TestDSL_ОшибкаСозданияВременногоКаталогаЛовится(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(root, []byte("x"), 0o600))
	interpreter.SetFileSandbox(root)
	defer interpreter.SetFileSandbox("")

	src := `Процедура Тест()
		Попытка
			КаталогВременныхФайлов();
			Возврат "не поймано";
		Исключение
			Возврат ИнформацияОбОшибке();
		КонецПопытки;
	КонецПроцедуры`
	info, ok := evalEnv(t, src).(*interpreter.Struct)
	require.True(t, ok, "ошибка ОС не была перехвачена")
	assert.Equal(t, "test.os", info.Get("Источник"))
	assert.Greater(t, info.Get("НомерСтроки").(float64), float64(0))
	assert.Contains(t, info.Get("Описание"), "создать каталог временных файлов")
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
