package interpreter_test

import (
	"testing"
)

// Арифметическая совместимость Неопределено с числом сохраняется (#1274).
// Эти тесты проверяют отсутствие значения; типизированные поля проходят
// обычную числовую ветку и проверяются матрицей публичных UI/DSL-путей.

func TestArith_NilPlusNumber_ReturnsNumber(t *testing.T) {
	src := `Функция Тест()
  X = Неопределено;
  Возврат X + 100;
КонецФункции`
	got := evalFunc(t, src)
	if !numEq(got, 100) {
		t.Errorf("nil + 100 = %v (%T), ожидалось 100", got, got)
	}
}

func TestArith_NumberPlusNil_ReturnsNumber(t *testing.T) {
	src := `Функция Тест()
  X = Неопределено;
  Возврат 50 + X;
КонецФункции`
	got := evalFunc(t, src)
	if !numEq(got, 50) {
		t.Errorf("50 + nil = %v, ожидалось 50", got)
	}
}

func TestArith_NilTimesNumber_ReturnsZero(t *testing.T) {
	src := `Функция Тест()
  X = Неопределено;
  Возврат X * 100;
КонецФункции`
	got := evalFunc(t, src)
	if !numEq(got, 0) {
		t.Errorf("nil * 100 = %v, ожидалось 0", got)
	}
}

func TestArith_NumberMinusNil_ReturnsNumber(t *testing.T) {
	src := `Функция Тест()
  X = Неопределено;
  Возврат 200 - X;
КонецФункции`
	got := evalFunc(t, src)
	if !numEq(got, 200) {
		t.Errorf("200 - nil = %v, ожидалось 200", got)
	}
}

// Защита от регрессии: «обычная» арифметика двух чисел должна по-прежнему
// возвращать число, а не строку (на случай если nil-toleration сломает
// перехват числовых операндов).
func TestArith_TwoNumbers_StillNumeric(t *testing.T) {
	src := `Функция Тест()
  Возврат 7 + 3;
КонецФункции`
	got := evalFunc(t, src)
	if !numEq(got, 10) {
		t.Errorf("7 + 3 = %v, ожидалось 10", got)
	}
}

// Защита от регрессии: «строка + число» в DSL — это конкатенация (так
// работает Сообщить("Итого: " + Сумма)). Не должно сломаться.
func TestArith_StringPlusNumber_Concats(t *testing.T) {
	src := `Функция Тест()
  Возврат "Сумма: " + 42;
КонецФункции`
	got := evalFunc(t, src)
	if got != "Сумма: 42" {
		t.Errorf("\"Сумма: \" + 42 = %v, ожидалось \"Сумма: 42\"", got)
	}
}
