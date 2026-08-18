package interpreter

import (
	"testing"
	"time"
)

// Конструктор Дата(Год, Месяц, День).
func TestDate_Constructor(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат Дата(2026, 5, 11);
КонецФункции`)
	d, ok := r.(time.Time)
	if !ok {
		t.Fatalf("Дата() вернула %T, ожидался time.Time", r)
	}
	if d.Year() != 2026 || d.Month() != 5 || d.Day() != 11 {
		t.Errorf("Дата(2026,5,11) = %v", d)
	}
}

// Конструктор с временем: Дата(Год,Месяц,День,Час,Минута,Секунда).
func TestDate_ConstructorWithTime(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат Дата(2026, 5, 11, 14, 30, 45);
КонецФункции`)
	d := r.(time.Time)
	if d.Hour() != 14 || d.Minute() != 30 || d.Second() != 45 {
		t.Errorf("время в Дата(): %v", d)
	}
}

// Конструктор из строки.
func TestDate_ConstructorString(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат Дата("2026-05-11");
КонецФункции`)
	d := r.(time.Time)
	if d.Year() != 2026 || d.Day() != 11 {
		t.Errorf("Дата(строка) = %v", d)
	}
}

// Дата + Число → сдвиг вперёд на N секунд.
func TestDate_AddSeconds(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат Дата(2026, 5, 11) + 86400;
КонецФункции`)
	d := r.(time.Time)
	if d.Day() != 12 {
		t.Errorf("Дата + 86400 = %v, ожидался день 12", d)
	}
}

// Дата - Число → сдвиг назад.
func TestDate_SubtractSeconds(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат Дата(2026, 5, 11) - 86400;
КонецФункции`)
	d := r.(time.Time)
	if d.Day() != 10 {
		t.Errorf("Дата - 86400 = %v, ожидался день 10", d)
	}
}

// Дата - Дата → разница в секундах.
func TestDate_Difference(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат Дата(2026, 5, 11) - Дата(2026, 5, 10);
КонецФункции`)
	if r != float64(86400) {
		t.Errorf("разность дат = %v, ожидалось 86400", r)
	}
}

// ДобавитьДень.
func TestDate_AddDay(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат ДобавитьДень(Дата(2026, 5, 11), 5);
КонецФункции`)
	d := r.(time.Time)
	if d.Day() != 16 {
		t.Errorf("ДобавитьДень(+5) = %v", d)
	}
}

// ДобавитьДень с отрицательным аргументом — переход через границу месяца.
func TestDate_AddDayNegative(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат ДобавитьДень(Дата(2026, 5, 2), -5);
КонецФункции`)
	d := r.(time.Time)
	if d.Month() != 4 || d.Day() != 27 {
		t.Errorf("ДобавитьДень(-5) = %v, ожидалось 27 апреля", d)
	}
}

// ДобавитьГод.
func TestDate_AddYear(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат ДобавитьГод(Дата(2026, 5, 11), -1);
КонецФункции`)
	d := r.(time.Time)
	if d.Year() != 2025 {
		t.Errorf("ДобавитьГод(-1) = %v", d)
	}
}

// Сравнение дат — хронологическое, а не строковое.
func TestDate_Compare(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Если Дата(2026, 5, 10) < Дата(2026, 5, 11) Тогда
    Возврат "до";
  Иначе
    Возврат "не до";
  КонецЕсли;
КонецФункции`)
	if r != "до" {
		t.Errorf("сравнение дат дало %v, ожидалось «до»", r)
	}
}

// Форматирование даты: ДФ=dd.MM.yyyy и ДФ=dd.mm.yyyy.
func TestDate_Format(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат Формат(Дата(2026, 8, 19), "ДФ=dd.MM.yyyy");
КонецФункции`)
	if r != "19.08.2026" {
		t.Errorf("Формат(ДФ=dd.MM.yyyy) = %v, ожидалось «19.08.2026»", r)
	}
}
