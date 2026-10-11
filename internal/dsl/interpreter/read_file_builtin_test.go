package interpreter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// ПрочитатьФайл возвращает содержимое целиком и определяет кодировку так же,
// как ЧтениеТекста.Открыть: UTF-8, иначе Windows-1251 (#1441).
func TestReadFileBuiltin_DecodesUTF8AndWindows1251(t *testing.T) {
	SetFileSandbox("")
	dir := t.TempDir()

	const text = "первая строка\nвторая строка\n"

	utf8Path := filepath.Join(dir, "utf8.txt")
	if err := os.WriteFile(utf8Path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cp1251, err := charmap.Windows1251.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	cpPath := filepath.Join(dir, "cp1251.txt")
	if err := os.WriteFile(cpPath, cp1251, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, path string }{
		{"UTF-8", utf8Path},
		{"Windows-1251", cpPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readFileFn([]any{tc.path}, "", 0)
			if err != nil {
				t.Fatalf("ПрочитатьФайл: %v", err)
			}
			if got != text {
				t.Errorf("содержимое = %q, ожидалось %q", got, text)
			}
		})
	}
}

// Отсутствующий файл и пустой путь — человеческая ошибка, ловимая Попыткой,
// а не тихая пустая строка: молчаливый успех на неверном пути хуже отказа.
func TestReadFileBuiltin_ErrorsAreUserErrors(t *testing.T) {
	SetFileSandbox("")
	for _, tc := range []struct{ name, path, want string }{
		{"нет файла", filepath.Join(t.TempDir(), "нет.txt"), "ПрочитатьФайл"},
		{"пустой путь", "", "не указан путь"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := callBuiltinExpectPanic(t, BuiltinFunc(readFileFn), []any{tc.path})
			if !strings.Contains(msg, tc.want) {
				t.Errorf("сообщение %q не содержит %q", msg, tc.want)
			}
		})
	}
}

// Новый builtin обязан жить под тем же FileGuard, что и остальные файловые
// функции: иначе песочница, запретившая файлы, его не остановит.
func TestReadFileBuiltin_CoveredByFileGuard(t *testing.T) {
	deny := FileGuard(func() error { return errors.New("файлы запрещены") })
	functions := NewFileFunctions(deny)
	for _, name := range []string{"прочитатьфайл", "readfile"} {
		fn, ok := functions[name].(BuiltinFunc)
		if !ok {
			t.Fatalf("%s не зарегистрирован под guard'ом", name)
		}
		msg := callBuiltinExpectPanic(t, fn, []any{"любой.txt"})
		if !strings.Contains(msg, "файлы запрещены") {
			t.Errorf("%s: ожидалось сообщение guard'а, получено %q", name, msg)
		}
	}
}
