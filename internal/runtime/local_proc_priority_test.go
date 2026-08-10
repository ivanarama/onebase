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
