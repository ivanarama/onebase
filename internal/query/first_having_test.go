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
