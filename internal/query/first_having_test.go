package query

import (
	"strings"
	"testing"
)

// «ВЫБРАТЬ ПЕРВЫЕ N» — часть языка запросов 1С; переносимые оттуда модули пишут
// его постоянно. Без разбора конструкция доезжала до СУБД как есть и падала
// с «syntax error near "10"».
func TestCompile_ПервыеПревращаетсяВLimit(t *testing.T) {
	res, err := Compile(`ВЫБРАТЬ ПЕРВЫЕ 10 Наименование ИЗ Справочник.Товар`, CompileOpts{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.HasSuffix(res.SQL, " LIMIT 10") {
		t.Errorf("нет LIMIT в конце:\n%s", res.SQL)
	}
	if strings.Contains(strings.ToUpper(res.SQL), "ПЕРВЫЕ") {
		t.Errorf("слово ПЕРВЫЕ осталось в SQL:\n%s", res.SQL)
	}
}

func TestCompile_ПервыеВместеСРазличными(t *testing.T) {
	res, err := Compile(`ВЫБРАТЬ РАЗЛИЧНЫЕ ПЕРВЫЕ 5 Наименование ИЗ Справочник.Товар`, CompileOpts{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	up := strings.ToUpper(res.SQL)
	if !strings.Contains(up, "DISTINCT") {
		t.Errorf("РАЗЛИЧНЫЕ потерялось:\n%s", res.SQL)
	}
	if !strings.HasSuffix(res.SQL, " LIMIT 5") {
		t.Errorf("нет LIMIT:\n%s", res.SQL)
	}
}

func TestCompile_ПервыеПередРазличными(t *testing.T) {
	res, err := Compile(`ВЫБРАТЬ ПЕРВЫЕ 5 РАЗЛИЧНЫЕ Наименование ИЗ Справочник.Товар`, CompileOpts{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(strings.ToUpper(res.SQL), "SELECT DISTINCT") || !strings.HasSuffix(res.SQL, " LIMIT 5") {
		t.Errorf("модификаторы разобраны неверно:\n%s", res.SQL)
	}
}

func TestCompile_TopРаботаетКакПервые(t *testing.T) {
	res, err := Compile(`SELECT TOP 3 Наименование FROM Справочник.Товар`, CompileOpts{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.HasSuffix(res.SQL, " LIMIT 3") {
		t.Errorf("нет LIMIT:\n%s", res.SQL)
	}
}

// Поле, которое просто называется «Первые», ломать нельзя: вырезаем конструкцию
// только когда следом идёт число.
func TestCompile_ПервыеБезЧислаНеТрогаем(t *testing.T) {
	res, err := Compile(`ВЫБРАТЬ Первые ИЗ Справочник.Товар`, CompileOpts{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if strings.Contains(res.SQL, "LIMIT") {
		t.Errorf("LIMIT появился на пустом месте:\n%s", res.SQL)
	}
}

func TestCompile_ПервыеПроверяетКоличество(t *testing.T) {
	for _, src := range []string{
		`ВЫБРАТЬ ПЕРВЫЕ 0 Наименование ИЗ Справочник.Товар`,
		`ВЫБРАТЬ ПЕРВЫЕ 1.5 Наименование ИЗ Справочник.Товар`,
		`ВЫБРАТЬ ПЕРВЫЕ -1 Наименование ИЗ Справочник.Товар`,
		`SELECT TOP 999999999999999999999999 Name FROM Catalog.Товар`,
	} {
		t.Run(src, func(t *testing.T) {
			_, err := Compile(src, CompileOpts{})
			if err == nil || !strings.Contains(err.Error(), "положительное целое число") {
				t.Fatalf("ожидалась понятная ошибка количества, получено %v", err)
			}
		})
	}
}

func TestCompile_ПервыеВОбъединенииОтклоняетсяБезПодменыСемантики(t *testing.T) {
	queries := []string{
		`ВЫБРАТЬ ПЕРВЫЕ 1 Наименование ИЗ Справочник.Товар ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Наименование ИЗ Справочник.Товар`,
		`ВЫБРАТЬ Наименование ИЗ Справочник.Товар ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ ПЕРВЫЕ 1 Наименование ИЗ Справочник.Товар`,
		`ВЫБРАТЬ Наименование ИЗ (ВЫБРАТЬ ПЕРВЫЕ 1 Наименование ИЗ Справочник.Товар) КАК Т`,
	}
	for _, src := range queries {
		t.Run(src, func(t *testing.T) {
			_, err := Compile(src, CompileOpts{})
			if err == nil || (!strings.Contains(err.Error(), "ОБЪЕДИНИТЬ") && !strings.Contains(err.Error(), "вложенных")) {
				t.Fatalf("ожидался явный отказ для неподдержанной области ПЕРВЫЕ, получено %v", err)
			}
		})
	}
}

// ИМЕЮЩИЕ — то же, что ИМЕЯ: в 1С пишут именно так.
func TestCompile_ИмеющиеЭтоHaving(t *testing.T) {
	res, err := Compile(
		`ВЫБРАТЬ Товар, СУММА(Количество) КАК Кол ИЗ РегистрНакопления.Остатки СГРУППИРОВАТЬ ПО Товар ИМЕЮЩИЕ СУММА(Количество) > 0`,
		CompileOpts{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(strings.ToUpper(res.SQL), "HAVING") {
		t.Errorf("ИМЕЮЩИЕ не превратилось в HAVING:\n%s", res.SQL)
	}
	if strings.Contains(strings.ToUpper(res.SQL), "ИМЕЮЩИЕ") {
		t.Errorf("слово ИМЕЮЩИЕ осталось в SQL:\n%s", res.SQL)
	}
}

// ЕстьNULL — подстановка значения вместо NULL. В переносимых модулях
// встречается в каждом втором запросе с левым соединением.
func TestCompile_ЕстьNullЭтоCoalesce(t *testing.T) {
	res, err := Compile(`ВЫБРАТЬ ЕстьNULL(Количество, 0) КАК Кол ИЗ РегистрНакопления.Остатки`, CompileOpts{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.Contains(strings.ToUpper(res.SQL), "COALESCE(") {
		t.Errorf("ЕстьNULL не превратилось в COALESCE:\n%s", res.SQL)
	}
	if strings.Contains(strings.ToUpper(res.SQL), "ЕСТЬNULL") {
		t.Errorf("имя функции осталось в SQL:\n%s", res.SQL)
	}
}
