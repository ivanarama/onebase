package expreval

import (
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// #788: формула компоновки, вызывающая зацикленную функцию конфигурации, обязана
// прерываться песочницей по лимиту итераций, а не считаться вечно. Раньше
// UI-путь исполнял формулы без песочницы вовсе — этот сценарий вешал хендлер.
func TestEvaluator_SandboxBoundsRunawayExpression(t *testing.T) {
	src := "Функция Вечность()\n\tПока Истина Цикл\n\tКонецЦикла;\n\tВозврат Истина;\nКонецФункции\n"
	prog, err := parser.New(lexer.New(src, "t.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	procs := map[string]*ast.ProcedureDecl{}
	for _, d := range prog.Procedures {
		procs[d.Name.Literal] = d
		procs[strings.ToLower(d.Name.Literal)] = d
	}
	interp := interpreter.New()
	interp.LookupProc = func(name string) *ast.ProcedureDecl {
		if p, ok := procs[name]; ok {
			return p
		}
		return procs[strings.ToLower(name)]
	}

	ev := New(interp, interpreter.SandboxProfile{MaxLoopIters: 10_000})

	done := make(chan struct{})
	var evalErr error
	go func() {
		_, evalErr = ev.EvalBool("Вечность()", compose.Row{})
		close(done)
	}()
	select {
	case <-done:
		if evalErr == nil {
			t.Fatal("зацикленная формула вернулась без ошибки — песочница не ограничила цикл")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("формула не прервана — песочница не применилась (вычисление зависло)")
	}
}

// Контроль: нормальная формула считается штатно — та же реализация, что и до
// объединения путей.
func TestEvaluator_NormalExpression(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	got, err := ev.EvalBool("1 = 1", compose.Row{})
	if err != nil {
		t.Fatalf("EvalBool: %v", err)
	}
	if !got {
		t.Fatal("ожидалось true для «1 = 1»")
	}
}

// Профиль формул закрывает возможности, а не только время (#884).
//
// Лимиты времени и итераций (#788) закрыли DoS, но файлы, сеть и запуск
// процессов формуле оставались доступны. Формула отчёта не должна уметь того,
// что умеет полноценный модуль: внешние отчёты загружаются через админку и
// живут в БД, то есть формула может приехать файлом со стороны — как внешняя
// обработка.
func TestEvaluator_ПрофильЗапрещаетВозможности(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())

	cases := []struct {
		name, expr, want string
	}{
		{"временный файл", `ПолучитьИмяВременногоФайла() <> ""`, "файл"},
		{"каталог временных файлов", `КаталогВременныхФайлов() <> ""`, "файл"},
		{"поиск файлов", `НайтиФайлы("/", "*") <> Неопределено`, "файл"},
		{"HTTP-запрос", `HTTPПолучить("http://example.com") <> Неопределено`, "сет"},
		{"команда ОС", `ВыполнитьКоманду("id") <> Неопределено`, "команд"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ev.EvalBool(c.expr, compose.Row{})
			if err == nil {
				t.Fatalf("формула «%s» выполнена без ошибки — возможность не запрещена", c.expr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), c.want) {
				t.Errorf("ошибка не объясняет запрет (%q): %v", c.want, err)
			}
		})
	}
}

// Обратная сторона: то, ради чего формулы существуют, работать обязано.
// Запрещаются возможности, а не составляется белый список функций — иначе
// каждый новый builtin ломал бы формулы.
func TestEvaluator_АрифметикаСтрокиДатыРаботают(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	row := compose.Row{"Сумма": float64(150), "Клиент": "ООО Ромашка"}

	for _, c := range []struct {
		name, expr string
	}{
		{"арифметика", "Сумма * 2 > 200"},
		{"строки", `СтрДлина(Клиент) > 3`},
		{"верхний регистр", `ВРег(Клиент) <> ""`},
		{"даты", `Год(ТекущаяДата()) > 2000`},
		{"формат", `Формат(Сумма, "ЧЦ=10") <> ""`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ev.EvalBool(c.expr, row)
			if err != nil {
				t.Fatalf("формула «%s» отвергнута: %v", c.expr, err)
			}
			if !got {
				t.Errorf("формула «%s» дала Ложь", c.expr)
			}
		})
	}
}
