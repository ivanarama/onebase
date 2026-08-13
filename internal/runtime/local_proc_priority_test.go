package runtime_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Своя функция модуля важнее чужого экспорта (issue #717).
//
// Порядок разрешения имён был обратным: экспортная функция ЛЮБОГО модуля
// полностью затеняла одноимённую локальную. Собственная функция становилась
// недостижимой, неквалифицированный вызов молча уходил в чужой экспорт, а при
// несовпадении числа параметров недостающие вставали как nil.
//
// Стоило это сломанного постороннего кода: добавление нового модуля со
// вспомогательным именем уводило вызов из давно зелёного файла в чужую
// функцию, единственный аргумент вставал на место первого параметра, второй
// становился nil — и имя статуса подставлялось как имя таблицы. Ошибка при
// этом указывала на строку в НОВОМ модуле.

const shadowExporter = `
Функция Кто(Первый, Второй) Экспорт
	Возврат "МОДУЛЬНАЯ(" + Строка(Первый) + "," + Строка(Второй) + ")";
КонецФункции
`

const shadowCaller = `
Функция Кто()
	Возврат "ЛОКАЛЬНАЯ";
КонецФункции

Функция Выполнить()
	Возврат Кто();
КонецФункции
`

// Регистрация ровно как в загрузчике проекта: общий модуль идёт в LoadModules
// (его экспорт виден всем), файл обработки — в Programs сущности, откуда его
// берёт GetSiblingProc по имени файла.
func loadShadowRegistry(t *testing.T, callerFile, callerSrc string) *runtime.Registry {
	t.Helper()
	reg := runtime.NewRegistry()

	modProg, err := parser.New(lexer.New(shadowExporter, "ЗатенениеБ.module.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse модуля: %v", err)
	}
	reg.LoadModules(map[string]*ast.Program{"ЗатенениеБ": modProg})

	callerProg, err := parser.New(lexer.New(callerSrc, callerFile)).ParseProgram()
	if err != nil {
		t.Fatalf("parse вызывающего: %v", err)
	}
	ent := &metadata.Entity{Name: "Тест", Kind: metadata.KindCatalog}
	reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{ent},
		Programs: map[string]*ast.Program{"Тест": callerProg},
	})
	return reg
}

func TestРазрешениеИмён_СвояФункцияВажнееЧужогоЭкспорта(t *testing.T) {
	reg := loadShadowRegistry(t, "ЗатенениеА.proc.os", shadowCaller)

	in := interpreter.New()
	in.LookupProc = reg.GetModuleProc
	in.LookupSiblingProc = reg.GetSiblingProc

	proc := reg.GetSiblingProc("ЗатенениеА.proc.os", "Выполнить")
	if proc == nil {
		t.Fatal("процедура Выполнить не найдена — проба некорректна")
	}
	var res any
	if err := in.RunWithResult(proc, runtime.NewObject("T", metadata.KindCatalog), &res); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := res.(string)
	if got != "ЛОКАЛЬНАЯ" {
		t.Fatalf("неквалифицированный вызов ушёл в чужой экспорт: %q", got)
	}
}

// То же правило для ОБЩИХ МОДУЛЕЙ. Процедуры модулей лежат отдельно от r.procs,
// поэтому GetSiblingProc до них не доходил и правило #717 на них не
// распространялось: неквалифицированный вызов внутри модуля разрешался только
// плоской картой и мог уйти в одноимённую процедуру чужого модуля.
//
// Поймано на переносе конфигурации: в одном модуле лежала недоконвертированная
// копия вспомогательных функций другого, и вызовы уходили в неё, падая на
// первой же строке — с указанием на файл, который вызывающий код не упоминает.
func TestРазрешениеИмён_СвояФункцияМодуляВажнееЧужогоМодуля(t *testing.T) {
	reg := runtime.NewRegistry()

	чужой, err := parser.New(lexer.New(`
Функция Кто() Экспорт
	Возврат "ЧУЖОЙ";
КонецФункции
`, "МодульБ.module.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse чужого модуля: %v", err)
	}
	свой, err := parser.New(lexer.New(`
Функция Кто()
	Возврат "СВОЙ";
КонецФункции

Функция Проверка() Экспорт
	Возврат Кто();
КонецФункции
`, "МодульА.module.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse своего модуля: %v", err)
	}
	reg.LoadModules(map[string]*ast.Program{"МодульА": свой, "МодульБ": чужой})

	in := interpreter.New()
	in.LookupProc = reg.GetModuleProc
	in.LookupSiblingProc = reg.GetSiblingProc
	in.LookupModuleProc = reg.GetModuleNamespacedProc

	proc := reg.GetModuleNamespacedProc("МодульА", "Проверка")
	if proc == nil {
		t.Fatal("процедура Проверка не найдена — проба некорректна")
	}
	var res any
	if err := in.RunWithResult(proc, runtime.NewObject("T", metadata.KindCatalog), &res); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, _ := res.(string); got != "СВОЙ" {
		t.Fatalf("вызов внутри модуля ушёл в чужой модуль: %q", got)
	}
}

// Плоская карта строилась обходом map, а он в Go рандомизирован: при совпадении
// имён в двух модулях победитель выбирался случайно и мог смениться при
// следующем запуске того же кода. Пятьдесят перезагрузок подряд обязаны дать
// один и тот же ответ.
func TestРазрешениеИмён_ПлоскаяКартаМодулейДетерминирована(t *testing.T) {
	парс := func(имяФайла, текст string) *ast.Program {
		t.Helper()
		prog, err := parser.New(lexer.New(текст, имяФайла)).ParseProgram()
		if err != nil {
			t.Fatalf("parse %s: %v", имяФайла, err)
		}
		return prog
	}
	модули := map[string]*ast.Program{
		"Альфа": парс("Альфа.module.os", "Функция Общая() Экспорт\n\tВозврат \"альфа\";\nКонецФункции\n"),
		"Бета":  парс("Бета.module.os", "Функция Общая() Экспорт\n\tВозврат \"бета\";\nКонецФункции\n"),
		"Гамма": парс("Гамма.module.os", "Функция Общая() Экспорт\n\tВозврат \"гамма\";\nКонецФункции\n"),
	}

	reg := runtime.NewRegistry()
	reg.LoadModules(модули)
	первый := reg.GetModuleProc("Общая")
	if первый == nil {
		t.Fatal("процедура Общая не найдена — проба некорректна")
	}
	for i := 0; i < 50; i++ {
		reg.LoadModules(модули)
		if got := reg.GetModuleProc("Общая"); got != первый {
			t.Fatalf("перезагрузка %d выбрала другой модуль: было %s, стало %s",
				i, первый.Name.File, got.Name.File)
		}
	}
	if первый.Name.File != "Альфа.module.os" {
		t.Errorf("ожидался первый по имени модуля, получен %s", первый.Name.File)
	}
}

// Обратная сторона: чужой экспорт обязан оставаться доступным — но только
// квалифицированно. Иначе «починка» отрезала бы модульные функции вовсе.
func TestРазрешениеИмён_ЧужойЭкспортДоступенКвалифицированно(t *testing.T) {
	reg := loadShadowRegistry(t, "ЗатенениеВ.proc.os", `
Функция Кто()
	Возврат "ЛОКАЛЬНАЯ";
КонецФункции

Функция Проверка()
	Возврат ЗатенениеБ.Кто(1, 2);
КонецФункции
`)

	in := interpreter.New()
	in.LookupProc = reg.GetModuleProc
	in.LookupSiblingProc = reg.GetSiblingProc
	in.LookupModuleProc = reg.GetModuleNamespacedProc

	proc := reg.GetSiblingProc("ЗатенениеВ.proc.os", "Проверка")
	if proc == nil {
		t.Fatal("процедура Проверка не найдена")
	}
	var res any
	if err := in.RunWithResult(proc, runtime.NewObject("T", metadata.KindCatalog), &res); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := res.(string)
	if !strings.HasPrefix(got, "МОДУЛЬНАЯ") {
		t.Fatalf("квалифицированный вызов не дошёл до экспорта: %q", got)
	}
	if !strings.Contains(got, "1") || !strings.Contains(got, "2") {
		t.Errorf("аргументы не доехали: %q", got)
	}
}
