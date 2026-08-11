package interpreter_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

// parseProcFile разбирает исходник DSL с заданным именем файла и возвращает
// первую процедуру. Имя файла важно: гонка curFile/curLine проявляется, когда
// конкурентные запуски исполняют код из разных файлов.
func parseProcFile(t *testing.T, file, src string) *ast.ProcedureDecl {
	t.Helper()
	p := parser.New(lexer.New(src, file))
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	if len(prog.Procedures) == 0 {
		t.Fatalf("no procedures in %s", file)
	}
	return prog.Procedures[0]
}

// TestInterpreter_ConcurrentRun: один Interpreter (как в проде: cli/run.go
// создаёт единственный interp на сервер) исполняет процедуры из разных файлов
// в N горутин. Запускать под `go test -race`: на гонке curFile/curLine
// race-детектор падает.
func TestInterpreter_ConcurrentRun(t *testing.T) {
	interp := interpreter.New()

	procs := []*ast.ProcedureDecl{
		parseProcFile(t, "alpha.os", `Procedure WorkA()
  X = 1;
  Y = X + 2;
  Z = Y * 3;
EndProcedure`),
		parseProcFile(t, "beta.os", `Procedure WorkB()
  A = 10;
  B = A - 4;
  C = B + A;
EndProcedure`),
		parseProcFile(t, "gamma.os", `Procedure WorkC()
  S = "";
  S = S + "x";
  S = S + "y";
EndProcedure`),
	}

	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			proc := procs[n%len(procs)]
			for j := 0; j < 100; j++ {
				if err := interp.Run(proc, nil); err != nil {
					t.Errorf("run %s: %v", proc.Name.Literal, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestInterpreter_ConcurrentDSLErrorLocation: пока одна горутина гоняет код из
// busy.os, другая поднимает Error() в boom.os. DSLError обязан указывать на
// boom.os:3 — место возбуждения, а не на файл, который случайно исполнялся
// параллельно. На общем curFile/curLine это не гарантировано (и под -race —
// гонка).
func TestInterpreter_ConcurrentDSLErrorLocation(t *testing.T) {
	interp := interpreter.New()

	boom := parseProcFile(t, "boom.os", `Procedure Boom()
  X = 1;
  Error("boom");
EndProcedure`)
	busy := parseProcFile(t, "busy.os", `Procedure Busy()
  A = 1;
  B = A + 1;
  C = B + 1;
EndProcedure`)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			if err := interp.Run(busy, nil); err != nil {
				t.Errorf("busy: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			err := interp.Run(boom, nil)
			dslErr, ok := err.(*interpreter.DSLError)
			if !ok {
				t.Errorf("want DSLError, got %T: %v", err, err)
				return
			}
			if dslErr.File != "boom.os" || dslErr.Line != 3 {
				t.Errorf("wrong location: %s:%d (want boom.os:3)", dslErr.File, dslErr.Line)
				return
			}
		}
	}()
	wg.Wait()
}

// countingHook — потокобезопасный DebugHook, считающий вызовы.
type countingHook struct{ calls atomic.Int64 }

func (h *countingHook) HookCheckBreakpoint(file string, line int, cond func(string) (bool, error)) bool {
	h.calls.Add(1)
	return false
}
func (h *countingHook) HookShouldStep(file string, depth int) bool {
	h.calls.Add(1)
	return false
}
func (h *countingHook) HookOnPause(file string, line int, vars map[string]any, evalFn func(string) (any, error), reason string) {
	h.calls.Add(1)
}
func (h *countingHook) HookPushFrame(procedure string, line int) { h.calls.Add(1) }
func (h *countingHook) HookPopFrame()                            { h.calls.Add(1) }

// TestInterpreter_ConcurrentDebugToggle: включение/выключение отладки во время
// конкурентных запусков (как debugGlobalEnable/Disable на живом сервере) не
// должно гонять и тем более ронять интерпретатор. DebugSource консультируется
// на старте каждого запуска; включён/выключен — состояние источника.
func TestInterpreter_ConcurrentDebugToggle(t *testing.T) {
	interp := interpreter.New()
	proc := parseProcFile(t, "toggled.os", `Procedure Work()
  X = 1;
  Y = X + 2;
  Z = Y * 3;
EndProcedure`)

	hook := &countingHook{}
	var enabled atomic.Bool
	interp.DebugSource = func() interpreter.DebugHook {
		if enabled.Load() {
			return hook
		}
		return nil
	}

	stop := make(chan struct{})
	var toggler sync.WaitGroup
	toggler.Add(1)
	go func() { // дёргает отладчик, как debugGlobalEnable/Disable
		defer toggler.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			enabled.Store(true)
			enabled.Store(false)
		}
	}()

	var workers sync.WaitGroup
	for g := 0; g < 4; g++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 200; j++ {
				if err := interp.Run(proc, nil); err != nil {
					t.Errorf("run: %v", err)
					return
				}
			}
		}()
	}

	workers.Wait()
	close(stop)
	toggler.Wait()
}

// TestInterpreter_DebugSourceCapturedPerRun: hook захватывается один раз на
// запуск — при включённом источнике запуск дёргает hook, при выключенном нет.
func TestInterpreter_DebugSourceCapturedPerRun(t *testing.T) {
	interp := interpreter.New()
	proc := parseProcFile(t, "cap.os", `Procedure Work()
  X = 1;
  Y = X + 2;
EndProcedure`)

	hook := &countingHook{}
	var enabled atomic.Bool
	interp.DebugSource = func() interpreter.DebugHook {
		if enabled.Load() {
			return hook
		}
		return nil
	}

	enabled.Store(true)
	if err := interp.Run(proc, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	n := hook.calls.Load()
	if n == 0 {
		t.Fatal("debug hook не вызывался при включённом DebugSource")
	}

	enabled.Store(false)
	if err := interp.Run(proc, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := hook.calls.Load(); got != n {
		t.Fatalf("debug hook вызывался при выключенном DebugSource: %d → %d", n, got)
	}
}
