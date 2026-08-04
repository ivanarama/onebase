package interpreter

import (
	"fmt"
	"testing"
)

// Число(Истина) = 1, Число(Ложь) = 0. Раньше булево уходило в разбор строки
// «true»/«false», не парсилось и давало 0 в обоих случаях — типовая проверка
// флага «Если Число(Флаг) <> 1» не срабатывала никогда. Значение bool-поля
// приходит в DSL то булевым (форма, восстановление из БД), то числом 0/1
// (ПолучитьОбъект на SQLite), поэтому Число() обязано выравнивать оба случая.
func TestBuiltinToNumber_Bool(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{true, "1"},
		{false, "0"},
		{"1", "1"},
		{"0", "0"},
		{float64(1), "1"},
		{"не число", "0"},
	} {
		got, err := builtinToNumber([]any{tc.in}, "", 0)
		if err != nil {
			t.Fatalf("Число(%v): %v", tc.in, err)
		}
		if s := fmt.Sprintf("%v", got); s != tc.want {
			t.Errorf("Число(%v) = %s, ожидалось %s", tc.in, s, tc.want)
		}
	}
}
