package parser_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

// Пропущенный фактический аргумент — «Метод(А,,Б)», «Метод(А, , Б)»,
// «Метод(А,)» — законная запись 1С, и в выгрузках БСП она обычна. Парсер
// требовал выражение после каждой запятой, поэтому загрузка конфигурации
// падала с `unexpected "," in expression` на первом же таком модуле, а
// пользователь не мог довести конвертацию до запуска (issue #1160).

// argsOfFirstCall достаёт аргументы первого вызова в теле процедуры.
func argsOfFirstCall(t *testing.T, src string) []ast.Expr {
	t.Helper()
	prog := parse(t, "Процедура Тест()\n  "+src+";\nКонецПроцедуры")
	if len(prog.Procedures) != 1 || len(prog.Procedures[0].Body) != 1 {
		t.Fatalf("%q: ждали одну процедуру с одним оператором", src)
	}
	stmt, ok := prog.Procedures[0].Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("%q: ждали ExprStmt, получили %T", src, prog.Procedures[0].Body[0])
	}
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok {
		t.Fatalf("%q: ждали CallExpr, получили %T", src, stmt.X)
	}
	return call.Args
}

// shape описывает разобранный список аргументов: "." — обычное выражение,
// "_" — пропуск. Форма читается глазами и не зависит от вида выражения.
func shape(args []ast.Expr) string {
	var b strings.Builder
	for _, a := range args {
		if _, skipped := a.(*ast.MissingArg); skipped {
			b.WriteByte('_')
			continue
		}
		b.WriteByte('.')
	}
	return b.String()
}

func TestParser_ПропущенныйАргументРазбирается(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"Ф()", ""},
		{"Ф(1)", "."},
		{"Ф(1, 2)", ".."},
		{"Ф(1,,3)", "._."},
		{"Ф(1, , 3)", "._."},
		{"Ф(1,2,)", ".._"},
		{"Ф(,1)", "_."},
		{"Ф(1,,,4)", ".__."},
		{"Ф(,)", "__"},
		{"Ф(,,)", "___"},
	}
	for _, c := range cases {
		got := shape(argsOfFirstCall(t, c.src))
		if got != c.want {
			t.Errorf("%s: форма аргументов %q, ждали %q", c.src, got, c.want)
		}
	}
}

func TestParser_ПропускВМетодеИКонструктореНовый(t *testing.T) {
	// Тот же parseArgs обслуживает вызов метода и «Новый Тип(…)» — обе формы
	// приходят из выгрузок 1С наравне со свободной функцией.
	if got := shape(argsOfFirstCall(t, "Объект.Метод(1,,3)")); got != "._." {
		t.Errorf("вызов метода: форма %q, ждали \"._.\"", got)
	}

	prog := parse(t, "Процедура Тест()\n  С = Новый Структура(\"А,Б,В\", 1,,3);\nКонецПроцедуры")
	assign, ok := prog.Procedures[0].Body[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("ждали AssignStmt, получили %T", prog.Procedures[0].Body[0])
	}
	newExpr, ok := assign.Value.(*ast.NewExpr)
	if !ok {
		t.Fatalf("ждали NewExpr, получили %T", assign.Value)
	}
	if got := shape(newExpr.Args); got != ".._." {
		t.Errorf("Новый Структура: форма %q, ждали \".._.\" (имена полей, 1, пропуск, 3)", got)
	}
}

func TestParser_ПропускНеМаскируетОшибкуСинтаксиса(t *testing.T) {
	// Пропуском считается только запятая или закрывающая скобка на месте начала
	// аргумента. Мусор на этом месте обязан по-прежнему быть ошибкой, иначе
	// расширение грамматики проглотит опечатки вместе с 1С-формой.
	for _, src := range []string{"Ф(1, * 2)", "Ф(1, +)", "Ф(*)"} {
		p := parser.New(lexer.New("Процедура Тест()\n  "+src+";\nКонецПроцедуры", "test.os"))
		if _, err := p.ParseProgram(); err == nil {
			t.Errorf("%s: разобрано без ошибки, ждали ошибку синтаксиса", src)
		}
	}
}

func TestParser_ВызовыИзЗаявки1160(t *testing.T) {
	// Дословно оба примера из отчёта: модуль БСП администрирования кластера.
	src := `Процедура ПодключитьсяККластеру(РезультатЗапуска, Комментарий, Команда, ПараметрыАдминистрированияКластера, Фильтр, ТипыСвойствОписанияБазы)
	ЗаписьЖурналаРегистрации(НСтр("ru = 'Подключение к кластеру серверов'", ОбщегоНазначения.КодОсновногоЯзыка()),
		?(РезультатЗапуска.КодВозврата = 0, УровеньЖурналаРегистрации.Информация, УровеньЖурналаРегистрации.Предупреждение),,,
		Комментарий);

	Свойства = ЗапуститьКоманду(Команда, ПараметрыАдминистрированияКластера, , Фильтр, ТипыСвойствОписанияБазы);
КонецПроцедуры`

	prog := parse(t, src)
	body := prog.Procedures[0].Body
	if len(body) != 2 {
		t.Fatalf("ждали два оператора, получили %d", len(body))
	}

	journal := body[0].(*ast.ExprStmt).X.(*ast.CallExpr)
	if got := shape(journal.Args); got != "..__." {
		t.Errorf("ЗаписьЖурналаРегистрации: форма %q, ждали \"..__.\"", got)
	}
	command := body[1].(*ast.AssignStmt).Value.(*ast.CallExpr)
	if got := shape(command.Args); got != ".._.." {
		t.Errorf("ЗапуститьКоманду: форма %q, ждали \".._..\"", got)
	}
}
