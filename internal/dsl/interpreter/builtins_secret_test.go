package interpreter_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/secrets"
)

// masterKey задаёт процессу мастер-ключ так же, как администратор сервера, —
// переменной окружения — и возвращает enc:-ссылку на значение.
func masterKey(t *testing.T, plain string) string {
	t.Helper()
	key, err := secrets.GenerateKey()
	require.NoError(t, err)
	t.Setenv(secrets.EnvMasterKeyFile, "")
	t.Setenv(secrets.EnvMasterKey, key.Hex())
	ref, err := key.Encrypt(plain)
	require.NoError(t, err)
	return ref
}

// Значение приходит в модуль так же, как из реквизита справочника, — строкой
// в переменной.
func TestDSL_РасшифроватьСекрет_EncИзДанных(t *testing.T) {
	ref := masterKey(t, "пароль-1С")
	src := `Процедура Тест()
		Возврат РасшифроватьСекрет(Пароль) + "|" + DecryptSecret("Basic ${" + Пароль + "}") + "|" + РасшифроватьСекрет("открытый");
	КонецПроцедуры`
	got := evalEnv(t, src, map[string]any{"Пароль": ref})
	assert.Equal(t, "пароль-1С|Basic пароль-1С|открытый", got)
}

// env:/file: из данных не разыменовываются, а ошибка ловится Попыткой и не
// содержит значения переменной.
func TestDSL_РасшифроватьСекрет_EnvИFileОтклоняются(t *testing.T) {
	masterKey(t, "x")
	t.Setenv("ONEBASE_TEST_SECRET_LEAK", "утечка")
	src := `Процедура Тест()
		Итог = "";
		Для Каждого Значение Из Значения Цикл
			Попытка
				Итог = Итог + РасшифроватьСекрет(Значение) + ";";
			Исключение
				Итог = Итог + ОписаниеОшибки() + ";";
			КонецПопытки;
		КонецЦикла;
		Возврат Итог;
	КонецПроцедуры`
	arr := interpreter.NewArray([]any{
		"env:ONEBASE_TEST_SECRET_LEAK",
		"${env:ONEBASE_TEST_SECRET_LEAK}",
		"file:/etc/passwd",
	})
	got, _ := evalEnv(t, src, map[string]any{"Значения": arr}).(string)
	assert.NotContains(t, got, "утечка")
	assert.Equal(t, 3, strings.Count(got, "только enc:-ссылки"), got)
}

// Без мастер-ключа enc: не расшифровывается — ловимая ошибка с подсказкой.
func TestDSL_РасшифроватьСекрет_БезМастерКлюча(t *testing.T) {
	ref := masterKey(t, "x")
	t.Setenv(secrets.EnvMasterKey, "")
	src := `Процедура Тест()
		Попытка
			Возврат РасшифроватьСекрет(Пароль);
		Исключение
			Возврат ОписаниеОшибки();
		КонецПопытки;
	КонецПроцедуры`
	got, _ := evalEnv(t, src, map[string]any{"Пароль": ref}).(string)
	assert.Contains(t, got, secrets.EnvMasterKey)
}

// Недоверенный код (ИИ, внешние отчёты) открытых секретов не получает: имя
// перекрыто песочницей и переопределить его через переменные нельзя.
func TestDSL_РасшифроватьСекрет_ЗакрытВПесочнице(t *testing.T) {
	ref := masterKey(t, "пароль-1С")
	src := `Процедура Тест()
		Попытка
			Возврат РасшифроватьСекрет(Пароль);
		Исключение
			Возврат ОписаниеОшибки();
		КонецПопытки;
	КонецПроцедуры`
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	require.NoError(t, err)
	interp := interpreter.New()
	var result any
	require.NoError(t, interp.RunSandboxed(prog.Procedures[0], runtime.NewObject("Test", metadata.KindDocument),
		interpreter.RestrictedProfile(), &result, map[string]any{"Пароль": ref}))
	got, _ := result.(string)
	assert.NotContains(t, got, "пароль-1С")
	assert.Contains(t, got, "песочница")
}
