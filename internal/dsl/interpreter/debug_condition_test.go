package interpreter_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Условие точки останова вычисляется в живом контексте и на КАЖДОМ проходе
// строки, без участия человека. Значит, оно не должно уметь менять данные:
// `Записать(…)` в условии молча мутировал бы базу при каждой проверке, а
// вызов функции с побочными эффектами делал бы отладчик частью поведения
// программы — убрал точку, программа работает иначе (#883).
//
// Табло и консоль остаются полноценными намеренно: их человек набирает сам,
// по одному разу, глядя на результат.

// condHook — DebugHook, который вычисляет заданное условие на первой же строке
// и запоминает результат.
type condHook struct {
	expr    string
	asked   atomic.Bool
	stopped bool
	err     error
}

func (h *condHook) HookCheckBreakpoint(_ string, _ int, cond func(string) (bool, error)) bool {
	if h.asked.Swap(true) {
		return false
	}
	h.stopped, h.err = cond(h.expr)
	return false
}
func (h *condHook) HookShouldStep(string, int) bool { return false }
func (h *condHook) HookOnPause(string, int, map[string]any, func(string) (any, error), string) {
}
func (h *condHook) HookPushFrame(string, int) {}
func (h *condHook) HookPopFrame()             {}

// runWithCondition исполняет процедуру с отладчиком, который проверит condExpr.
func runWithCondition(t *testing.T, condExpr string, vars map[string]any) *condHook {
	t.Helper()
	interp := interpreter.New()
	hook := &condHook{expr: condExpr}
	interp.DebugSource = func() interpreter.DebugHook { return hook }
	proc := parseProcFile(t, "cond.os", `Процедура Работа()
  Х = 1;
  Y = Х + 1;
КонецПроцедуры`)
	if err := interp.Run(proc, runtime.NewObject("T", metadata.KindCatalog), vars); err != nil {
		t.Fatalf("прогон отлаживаемой процедуры: %v", err)
	}
	if !hook.asked.Load() {
		t.Fatal("условие не проверялось — тест не про то, что задуман")
	}
	return hook
}

// catalogsStub — заглушка на месте глобала «Справочники»: если условие до неё
// доберётся, тест это увидит.
type catalogsStub struct{ touched *atomic.Bool }

func (c catalogsStub) Get(string) any { c.touched.Store(true); return nil }

func TestУсловиеТочкиОстанова_ЗапрещаетИзменяющиеГлобалы(t *testing.T) {
	for _, name := range []string{"Справочники", "Документы", "РегистрыСведений", "Движения"} {
		t.Run(name, func(t *testing.T) {
			var touched atomic.Bool
			hook := runWithCondition(t, name+".Контрагенты", map[string]any{
				name: catalogsStub{touched: &touched},
			})
			if touched.Load() {
				t.Errorf("условие добралось до «%s» — данные можно изменить из условия", name)
			}
			if hook.err == nil {
				t.Fatalf("обращение к «%s» из условия прошло без ошибки", name)
			}
			if !strings.Contains(hook.err.Error(), name) {
				t.Errorf("ошибка не называет запрещённое имя: %v", hook.err)
			}
			// Сломанное условие ОСТАНАВЛИВАЕТ точку с диагностикой — молчаливый
			// пропуск был бы худшим вариантом (точка стоит, отладчик её
			// игнорирует, человек ищет ошибку в своём коде).
			if hook.stopped {
				t.Errorf("при отказе условие вернуло Истина: %v", hook.err)
			}
		})
	}
}

// Обычное условие продолжает работать: запрет не должен ломать то, ради чего
// условные точки заводились.
func TestУсловиеТочкиОстанова_ОбычноеВыражениеРаботает(t *testing.T) {
	hook := runWithCondition(t, "1 = 1", nil)
	if hook.err != nil {
		t.Fatalf("простое условие не вычислилось: %v", hook.err)
	}
	if !hook.stopped {
		t.Error("условие «1 = 1» дало Ложь")
	}

	hookFalse := runWithCondition(t, "1 = 2", nil)
	if hookFalse.err != nil {
		t.Fatalf("простое условие не вычислилось: %v", hookFalse.err)
	}
	if hookFalse.stopped {
		t.Error("условие «1 = 2» дало Истина")
	}
}

// Бесконечное условие отрубается дедлайном, а не вешает отлаживаемый прогон.
func TestУсловиеТочкиОстанова_ОтрубаетсяПоДедлайну(t *testing.T) {
	start := time.Now()
	hook := runWithCondition(t, `Приостановить(60) = 0`, nil)
	elapsed := time.Since(start)

	if elapsed > 30*time.Second {
		t.Fatalf("условие выполнялось %v — дедлайна нет", elapsed)
	}
	if hook.err == nil {
		t.Fatalf("Приостановить(60) в условии прошло за %v без ошибки", elapsed)
	}
}
