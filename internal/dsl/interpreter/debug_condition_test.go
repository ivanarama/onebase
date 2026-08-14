package interpreter_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
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
	return runWithConditionInterp(t, interpreter.New(), condExpr, vars)
}

func runWithConditionInterp(t *testing.T, interp *interpreter.Interpreter, condExpr string, vars map[string]any) *condHook {
	t.Helper()
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

// effectObject имитирует уже созданный writer/менеджер в локальной переменной.
// До центральной границы overlay глобальных имён его не видел вовсе.
type effectObject struct {
	fields  map[string]any
	touched *atomic.Bool
}

func (o *effectObject) Get(name string) any {
	for k, v := range o.fields {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}
func (o *effectObject) Set(string, any) { o.touched.Store(true) }
func (o *effectObject) CallMethod(string, []any) any {
	o.touched.Store(true)
	return nil
}

func assertConditionRejectsEffect(t *testing.T, interp *interpreter.Interpreter, expr string, vars map[string]any, touched *atomic.Bool, want string) {
	t.Helper()
	hook := runWithConditionInterp(t, interp, expr, vars)
	if touched.Load() {
		t.Fatalf("условие %q выполнило побочный эффект", expr)
	}
	if hook.err == nil {
		t.Fatalf("условие %q прошло без ошибки", expr)
	}
	if !strings.Contains(strings.ToLower(hook.err.Error()), strings.ToLower(want)) {
		t.Errorf("диагностика %q не называет запрещённую операцию %q: %v", expr, want, hook.err)
	}
	if hook.stopped {
		t.Errorf("при отказе условие вернуло Истина: %v", hook.err)
	}
}

func TestУсловиеТочкиОстанова_ЗапрещаетЛокальныйWriterИОбаАлиаса(t *testing.T) {
	for _, method := range []string{"Записать", "Write"} {
		t.Run(method, func(t *testing.T) {
			var touched atomic.Bool
			writer := &effectObject{touched: &touched}
			assertConditionRejectsEffect(t, interpreter.New(),
				"Запись."+method+"() = Неопределено", map[string]any{"Запись": writer}, &touched, method)
		})
	}
}

func TestУсловиеТочкиОстанова_ЗапрещаетНумераторИОбмен(t *testing.T) {
	t.Run("СледующийНомер", func(t *testing.T) {
		var touched atomic.Bool
		numerators := &effectObject{touched: &touched}
		assertConditionRejectsEffect(t, interpreter.New(),
			`Нумераторы.СледующийНомер("Заказ") = ""`, map[string]any{"Нумераторы": numerators}, &touched, "СледующийНомер")
	})

	t.Run("ЗагрузитьПакет", func(t *testing.T) {
		var touched atomic.Bool
		plan := &effectObject{touched: &touched}
		root := &effectObject{fields: map[string]any{"Основной": plan}, touched: &touched}
		assertConditionRejectsEffect(t, interpreter.New(),
			`ПланыОбмена.Основной.ЗагрузитьПакет("данные") = Неопределено`,
			map[string]any{"ПланыОбмена": root}, &touched, "ЗагрузитьПакет")
	})
}

type constantsDBProbe struct{ touched atomic.Bool }

func (p *constantsDBProbe) SetConstant(context.Context, string, any) error {
	p.touched.Store(true)
	return nil
}

func TestУсловиеТочкиОстанова_НеМеняетКонстантыЧерезЧистыйBuiltin(t *testing.T) {
	probe := &constantsDBProbe{}
	constants := interpreter.NewConstantsRoot(context.Background(), probe,
		[]string{"Режим"}, map[string]any{"Режим": "до"})
	source := interpreter.NewStructFromMap(map[string]any{"Режим": "после"})
	assertConditionRejectsEffect(t, interpreter.New(),
		"ЗаполнитьЗначенияСвойств(Константы, Источник) = Неопределено",
		map[string]any{"Константы": constants, "Источник": source}, &probe.touched, "Режим")
}

func TestУсловиеТочкиОстанова_ВложеннаяФункцияНеМожетВызватьВнешнийЭффект(t *testing.T) {
	var touched atomic.Bool
	danger := parseProcFile(t, "danger.os", `Функция ОпаснаяФункция()
  ПобочныйЭффект();
  Возврат Истина;
КонецФункции`)
	interp := interpreter.New()
	interp.LookupProc = func(name string) *ast.ProcedureDecl {
		if strings.EqualFold(name, "ОпаснаяФункция") {
			return danger
		}
		return nil
	}
	vars := map[string]any{
		"ПобочныйЭффект": interpreter.BuiltinFunc(func([]any, string, int) (any, error) {
			touched.Store(true)
			return nil, nil
		}),
	}
	assertConditionRejectsEffect(t, interp, "ОпаснаяФункция()", vars, &touched, "ПобочныйЭффект")
}

func TestУсловиеТочкиОстанова_ЧистаяВложеннаяФункцияРаботает(t *testing.T) {
	helper := parseProcFile(t, "pure-helper.os", `Функция Удвоить(Значение)
  Возврат Значение * 2;
КонецФункции`)
	interp := interpreter.New()
	interp.LookupProc = func(name string) *ast.ProcedureDecl {
		if strings.EqualFold(name, "Удвоить") {
			return helper
		}
		return nil
	}
	hook := runWithConditionInterp(t, interp, "Удвоить(4) = 8", nil)
	if hook.err != nil {
		t.Fatalf("чистая helper-функция отвергнута: %v", hook.err)
	}
	if !hook.stopped {
		t.Fatal("чистая helper-функция дала Ложь")
	}
}

func TestУсловиеТочкиОстанова_ВложеннаяФункцияНеМеняетМодульнуюПеременную(t *testing.T) {
	danger := parseProcFile(t, "module-state.os", `Перем Состояние;
Функция ИзменитьСостояние()
  Состояние = 1;
  Возврат Истина;
КонецФункции`)
	interp := interpreter.New()
	interp.LookupProc = func(name string) *ast.ProcedureDecl {
		if strings.EqualFold(name, "ИзменитьСостояние") {
			return danger
		}
		return nil
	}
	hook := runWithConditionInterp(t, interp, "ИзменитьСостояние()", nil)
	if hook.err == nil {
		t.Fatal("условие изменило модульную переменную без ошибки")
	}
	if !strings.Contains(strings.ToLower(hook.err.Error()), "модульной переменной") {
		t.Fatalf("ошибка не объясняет запрет модульного состояния: %v", hook.err)
	}
}

func TestУсловиеТочкиОстанова_ВложеннаяФункцияНеМеняетКоллекциюПоИндексу(t *testing.T) {
	helper := parseProcFile(t, "array-helper.os", `Функция ИзменитьМассив(Значения)
  Значения[0] = 99;
  Возврат Истина;
КонецФункции`)
	interp := interpreter.New()
	interp.LookupProc = func(name string) *ast.ProcedureDecl {
		if strings.EqualFold(name, "ИзменитьМассив") {
			return helper
		}
		return nil
	}
	values := interpreter.NewArray([]any{float64(1)})
	hook := runWithConditionInterp(t, interp, "ИзменитьМассив(Значения)", map[string]any{"Значения": values})
	if hook.err == nil {
		t.Fatal("условие изменило коллекцию без ошибки")
	}
	if got := values.Index(0); got != float64(1) {
		t.Fatalf("условие изменило исходную коллекцию: %v", got)
	}
}

func TestУсловиеТочкиОстанова_НеВызываетОбычнуюВнешнююФункцию(t *testing.T) {
	var touched atomic.Bool
	vars := map[string]any{
		"ПобочныйЭффект": interpreter.BuiltinFunc(func([]any, string, int) (any, error) {
			touched.Store(true)
			return true, nil
		}),
	}
	assertConditionRejectsEffect(t, interpreter.New(), "ПобочныйЭффект()", vars, &touched, "ПобочныйЭффект")
}

func TestУсловиеТочкиОстанова_ReadOnlyРежимВосстанавливаетсяДоОсновногоКода(t *testing.T) {
	var touched atomic.Bool
	writer := &effectObject{touched: &touched}
	hook := &condHook{expr: "Запись.Записать() = Неопределено"}
	interp := interpreter.New()
	interp.DebugSource = func() interpreter.DebugHook { return hook }
	proc := parseProcFile(t, "restore.os", `Процедура Работа()
  Запись.Записать();
КонецПроцедуры`)
	if err := interp.Run(proc, nil, map[string]any{"Запись": writer}); err != nil {
		t.Fatalf("основной код после отказа условия: %v", err)
	}
	if hook.err == nil {
		t.Fatal("опасное условие не было отвергнуто")
	}
	if !touched.Load() {
		t.Fatal("read-only режим утёк из условия и заблокировал основной код")
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

func TestУсловиеТочкиОстанова_ПоляОбъектаОстаютсяДоступныДляЧтения(t *testing.T) {
	var touched atomic.Bool
	obj := &effectObject{fields: map[string]any{"Наименование": "Безопасно"}, touched: &touched}
	hook := runWithCondition(t, `Объект.Наименование = "Безопасно"`, map[string]any{"Объект": obj})
	if hook.err != nil {
		t.Fatalf("чтение поля объекта отвергнуто: %v", hook.err)
	}
	if !hook.stopped {
		t.Fatal("чтение поля объекта вернуло Ложь")
	}
	if touched.Load() {
		t.Fatal("чтение поля объекта вызвало изменение")
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
