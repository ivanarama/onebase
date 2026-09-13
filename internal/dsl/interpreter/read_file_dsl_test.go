package interpreter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ПрочитатьФайл проверяется тем же путём, каким её вызовет пользователь:
// исходник модуля → парсер → интерпретатор. Повод для этого правила — #611:
// зелёный тест на функции, которую прод не вызывает, хуже отсутствующего.
func TestReadFile_PublicDSLPath(t *testing.T) {
	interpreter.SetFileSandbox("")
	path := filepath.Join(t.TempDir(), "накладная.txt")
	const text = "первая строка\nвторая строка\n"
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))

	src := `Процедура Тест()
  Возврат ПрочитатьФайл("` + strings.ReplaceAll(path, `\`, `\\`) + `");
КонецПроцедуры`
	var result any
	require.NoError(t, interpreter.New().RunSandboxed(parseProc(t, src), nil, interpreter.SandboxProfile{}, &result))
	assert.Equal(t, text, result)
}

// Отсутствующий файл виден прикладному коду как обычное исключение: типовой
// обработчик обмена ловит его Попыткой и пишет причину, а не падает целиком.
func TestReadFile_MissingFileCatchable(t *testing.T) {
	interpreter.SetFileSandbox("")
	missing := filepath.Join(t.TempDir(), "нет.txt")
	src := `Процедура Тест()
  Попытка
    Возврат ПрочитатьФайл("` + strings.ReplaceAll(missing, `\`, `\\`) + `");
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
КонецПроцедуры`
	var result any
	require.NoError(t, interpreter.New().RunSandboxed(parseProc(t, src), nil, interpreter.SandboxProfile{}, &result))
	assert.Contains(t, result.(string), "ПрочитатьФайл")
}

// Строгий профиль песочницы запрещает и новую функцию — иначе она стала бы
// обходом файловой capability, закрытой для остальных файловых операций.
func TestReadFile_SandboxDeniedCatchable(t *testing.T) {
	src := `Процедура Тест()
  Попытка
    Возврат ПрочитатьФайл("любой.txt");
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
КонецПроцедуры`
	var result any
	require.NoError(t, interpreter.New().RunSandboxed(parseProc(t, src), nil, interpreter.RestrictedProfile(), &result))
	assert.Contains(t, result.(string), "файловые операции запрещены")
}
