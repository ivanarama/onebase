package interpreter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ПрочитатьФайл проверяется тем же путём, каким её вызовет пользователь:
// исходник модуля → парсер → интерпретатор. Повод для этого правила — #611:
// зелёный тест на функции, которую прод не вызывает, хуже отсутствующего.
func TestReadFile_PublicDSLPath(t *testing.T) {
	interpreter.SetFileSandbox("")
	t.Cleanup(func() { interpreter.SetFileSandbox("") })
	const text = "первая строка\nвторая строка\n"
	cp1251, err := charmap.Windows1251.NewEncoder().Bytes([]byte(text))
	require.NoError(t, err)
	for _, function := range []string{"ПрочитатьФайл", "ReadFile"} {
		for _, tc := range []struct {
			name string
			data []byte
			want string
		}{
			{"UTF-8", []byte(text), text},
			{"Windows-1251", cp1251, text},
			{"UTF-8 replacement rune", []byte("текст \uFFFD"), "текст \uFFFD"},
			{"empty", nil, ""},
		} {
			t.Run(function+"/"+tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "накладная.txt")
				require.NoError(t, os.WriteFile(path, tc.data, 0o600))
				src := `Процедура Тест()
 Возврат ` + function + `("` + strings.ReplaceAll(path, `\`, `\\`) + `");
КонецПроцедуры`
				var result any
				require.NoError(t, interpreter.New().RunSandboxed(parseProc(t, src), nil, interpreter.SandboxProfile{}, &result))
				assert.Equal(t, tc.want, result)
			})
		}
	}
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

// Ограничение пути проверяется до чтения через обе публичные функции.
func TestReadFile_PublicDSLPathRootBoundary(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	require.NoError(t, os.Mkdir(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside"), 0o600))
	outside := filepath.Join(parent, "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
	interpreter.SetFileSandbox(root)
	t.Cleanup(func() { interpreter.SetFileSandbox("") })
	for _, function := range []string{"ПрочитатьФайл", "ReadFile"} {
		for _, tc := range []struct{ name, path, want string }{
			{"inside relative", "inside.txt", "inside"},
			{"inside absolute", filepath.Join(root, "inside.txt"), "inside"},
			{"outside relative", "../outside.txt", "вне рабочего каталога"},
			{"outside absolute", outside, "вне рабочего каталога"},
			{"empty path", "", "не указан путь"},
			{"missing file", "missing.txt", "ПрочитатьФайл"},
		} {
			t.Run(function+"/"+tc.name, func(t *testing.T) {
				src := `Процедура Тест()
 Попытка
  Возврат ` + function + `("` + strings.ReplaceAll(tc.path, `\`, `\\`) + `");
 Исключение
  Возврат ОписаниеОшибки();
 КонецПопытки;
КонецПроцедуры`
				var result any
				require.NoError(t, interpreter.New().RunSandboxed(parseProc(t, src), nil, interpreter.SandboxProfile{}, &result))
				if strings.HasPrefix(tc.name, "inside") {
					assert.Equal(t, tc.want, result)
				} else {
					assert.Contains(t, result, tc.want)
				}
			})
		}
	}
}

func TestReadFile_PublicDSLPathDenyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exists.txt")
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0o600))
	for _, function := range []string{"ПрочитатьФайл", "ReadFile"} {
		t.Run(function, func(t *testing.T) {
			src := `Процедура Тест()
 Попытка
  Возврат ` + function + `("` + strings.ReplaceAll(path, `\`, `\\`) + `");
 Исключение
  Возврат ОписаниеОшибки();
 КонецПопытки;
КонецПроцедуры`
			var result any
			require.NoError(t, interpreter.New().RunSandboxed(parseProc(t, src), nil, interpreter.SandboxProfile{DenyFile: true}, &result))
			assert.Contains(t, result, "файловые операции запрещены")
		})
	}
}
