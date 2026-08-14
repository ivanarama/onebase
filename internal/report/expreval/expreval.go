// Package expreval вычисляет DSL-выражения компоновки отчётов (условия `when`
// и вычисляемые показатели) построчно, в песочнице.
//
// Единственный вход — New(interp, profile): выражение НЕЛЬЗЯ исполнить без
// явного SandboxProfile. Раньше существовали две почти одинаковые копии
// evaluator'а — в internal/api (через CallSandboxed с лимитами) и в internal/ui
// (через обычный RunWithResult БЕЗ песочницы). UI-путь исполнял формулы без
// лимитов времени/итераций, поэтому «Пока Истина Цикл» в формуле вешал хендлер
// навсегда (ctx-таймаут интерпретатор не прерывает) и навечно занимал слот
// concurrency-лимита отчётов (issue #788). Общий пакет убирает вторую копию и
// делает песочницу обязательной конструктивно.
package expreval

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// Evaluator вычисляет выражения компоновки. Каждое выражение компилируется в
// процедуру один раз (кэш) и исполняется на строку с полями строки как
// переменными. Не-bool результат условия трактуется как false.
type Evaluator struct {
	interp  *interpreter.Interpreter
	profile interpreter.SandboxProfile
	mu      sync.Mutex
	cache   map[string]*ast.ProcedureDecl
}

// Контракт: Evaluator реализует compose.Evaluator.
var _ compose.Evaluator = (*Evaluator)(nil)

// DefaultProfile — профиль формул компоновки: формула считается на каждую
// строку отчёта, обязана быть практически мгновенной и не должна уметь того,
// что умеет полноценный модуль.
//
// Лимиты времени и итераций закрывали реальный DoS (#788): «Пока Истина Цикл» в
// формуле вешал хендлер навсегда. Но профиль ограничивал ТОЛЬКО их — файлы,
// сеть и запуск процессов формуле оставались доступны (#884). Прежнее
// обоснование «формулы — доверенный код конфигурации» перестало быть полным:
// внешние отчёты загружаются через админку и живут в БД, то есть формула может
// приехать файлом со стороны, как внешняя обработка.
//
// Запрещаются возможности, а не составляется белый список функций: арифметика,
// строки, даты и формат — это и есть то, ради чего формулы существуют, и
// перечислять их поимённо значило бы ломать формулы при каждом новом builtin'е.
// Отказ ловится Попыткой и виден в предупреждениях компоновки.
func DefaultProfile() interpreter.SandboxProfile {
	return interpreter.SandboxProfile{
		DenyNet:      true,
		DenyFile:     true,
		DenyExec:     true,
		MaxWallClock: 10 * time.Second,
		MaxLoopIters: 1_000_000,
	}
}

// New строит evaluator. profile обязателен и передаётся явно: мимо песочницы
// выражение выполнить нельзя.
func New(interp *interpreter.Interpreter, profile interpreter.SandboxProfile) *Evaluator {
	return &Evaluator{interp: interp, profile: profile, cache: map[string]*ast.ProcedureDecl{}}
}

func (e *Evaluator) compile(expr string) (*ast.ProcedureDecl, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.cache[expr]; ok {
		return p, nil
	}
	src := "Функция __cond()\nВозврат (" + expr + ");\nКонецФункции\n"
	prog, err := parser.New(lexer.New(src, "cond.os")).ParseProgram()
	if err != nil {
		return nil, err
	}
	var proc *ast.ProcedureDecl
	for _, d := range prog.Procedures {
		proc = d
		break
	}
	if proc == nil {
		return nil, fmt.Errorf("пустое выражение условия")
	}
	e.cache[expr] = proc
	return proc, nil
}

func (e *Evaluator) EvalBool(expr string, row compose.Row) (bool, error) {
	proc, err := e.compile(expr)
	if err != nil {
		return false, err
	}
	result, err := e.interp.CallSandboxed(proc, &interpreter.MapThis{M: row}, nil, e.profile, map[string]any(row))
	if err != nil {
		// Деление на ноль — неопределённое значение: условие просто не срабатывает
		// (без ошибки), как пустая ячейка в 1С. Прочие ошибки пробрасываем.
		if errors.Is(err, interpreter.ErrDivisionByZero) {
			return false, nil
		}
		return false, err
	}
	b, _ := result.(bool)
	return b, nil
}

func (e *Evaluator) EvalNum(expr string, row compose.Row) (decimal.Decimal, bool, error) {
	proc, err := e.compile(expr)
	if err != nil {
		return decimal.Zero, false, err
	}
	result, err := e.interp.CallSandboxed(proc, &interpreter.MapThis{M: row}, nil, e.profile, map[string]any(row))
	if err != nil {
		// Деление на ноль — неопределённое значение (пустая ячейка, как в 1С), а не
		// runtime-ошибка: ok=false без ошибки, чтобы компоновка не поднимала
		// предупреждение. Прочие ошибки пробрасываем для показа.
		if errors.Is(err, interpreter.ErrDivisionByZero) {
			return decimal.Zero, false, nil
		}
		return decimal.Zero, false, err
	}
	d, ok := compose.ExportToDecimal(result)
	return d, ok, nil
}
