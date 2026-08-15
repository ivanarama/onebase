package interpreter_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

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

func TestBreakpointCondition_RejectsCallbacksNestedInHostComposites(t *testing.T) {
	t.Run("struct Stringer", func(t *testing.T) {
		var touched atomic.Bool
		hook := runWithCondition(t, `Str(Value) <> ""`, map[string]any{
			"Value": effectCompositeValue{Value: &effectStringValue{touched: &touched}},
		})
		if touched.Load() {
			t.Fatal("nested String callback executed")
		}
		if hook.err == nil || hook.stopped {
			t.Fatalf("composite host value was not rejected: stopped=%v err=%v", hook.stopped, hook.err)
		}
	})

	t.Run("typed slice Marshaler", func(t *testing.T) {
		var touched atomic.Bool
		hook := runWithCondition(t, `WriteJSON(Value) <> ""`, map[string]any{
			"Value": []*effectJSONValue{{touched: &touched}},
		})
		if touched.Load() {
			t.Fatal("nested MarshalJSON callback executed")
		}
		if hook.err == nil || hook.stopped {
			t.Fatalf("typed host slice was not rejected: stopped=%v err=%v", hook.stopped, hook.err)
		}
	})
}

func TestBreakpointCondition_StripsCapabilitiesFromTrustedDataWrappers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value func(*atomic.Bool) any
	}{
		{
			name: "Ref manager",
			value: func(touched *atomic.Bool) any {
				return &interpreter.Ref{UUID: "id", Name: "name", Type: "T", Manager: &effectRefManager{touched: touched}}
			},
		},
		{
			name: "DSLError cause",
			value: func(touched *atomic.Bool) any {
				return &interpreter.DSLError{File: "x.os", Line: 1, Msg: "safe", Err: &effectJSONError{touched: touched}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var touched atomic.Bool
			hook := runWithCondition(t, `StrLen(WriteJSON(Value)) > 2`, map[string]any{"Value": tc.value(&touched)})
			if touched.Load() {
				t.Fatal("nested capability callback executed")
			}
			if hook.err != nil || !hook.stopped {
				t.Fatalf("safe data wrapper was not serializable: stopped=%v err=%v", hook.stopped, hook.err)
			}
		})
	}
}

func TestBreakpointCondition_WriteJSONPreservesStructAndSliceViews(t *testing.T) {
	t.Run("Struct", func(t *testing.T) {
		value := interpreter.NewStructFromMap(map[string]any{"Field": "ok"})
		hook := runWithCondition(t, `StrLen(WriteJSON(Value)) > 2`, map[string]any{"Value": value})
		if hook.err != nil || !hook.stopped {
			t.Fatalf("Struct degraded during read-only JSON conversion: stopped=%v err=%v", hook.stopped, hook.err)
		}
	})

	t.Run("overlapping slices", func(t *testing.T) {
		backing := []any{"a", "b"}
		value := interpreter.NewArray([]any{backing[:1], backing[:2]})
		hook := runWithCondition(t, `StrFind(WriteJSON(Value), "b") > 0`, map[string]any{"Value": value})
		if hook.err != nil || !hook.stopped {
			t.Fatalf("overlapping slice views shared the wrong snapshot: stopped=%v err=%v", hook.stopped, hook.err)
		}
	})

	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "nil native slice", value: []any(nil)},
		{name: "nil native map", value: map[string]any(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := runWithCondition(t, `WriteJSON(Value) = "null"`, map[string]any{"Value": tc.value})
			if hook.err != nil || !hook.stopped {
				t.Fatalf("typed nil changed during read-only JSON conversion: stopped=%v err=%v", hook.stopped, hook.err)
			}
		})
	}
}

func TestBreakpointCondition_WriteJSONRejectsNativeCollectionCycles(t *testing.T) {
	arrayItems := []any{nil}
	array := interpreter.NewArray(arrayItems)
	arrayItems[0] = array

	mapValue := interpreter.NewStringMap(nil)
	mapValue.CallMethod("insert", []any{"self", mapValue})

	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "Array", value: array},
		{name: "Map", value: mapValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := runWithCondition(t, `WriteJSON(Value) <> ""`, map[string]any{"Value": tc.value})
			if hook.err == nil || hook.stopped {
				t.Fatalf("cyclic JSON value was not rejected: stopped=%v err=%v", hook.stopped, hook.err)
			}
		})
	}
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

type effectRowsIterator struct{ touched *atomic.Bool }

func (it *effectRowsIterator) IterateRows() []map[string]any {
	it.touched.Store(true)
	return []map[string]any{{"Значение": float64(1)}}
}

type effectStringObject struct{ touched *atomic.Bool }

func (o *effectStringObject) Get(string) any               { return nil }
func (o *effectStringObject) Set(string, any)              { o.touched.Store(true) }
func (o *effectStringObject) CallMethod(string, []any) any { o.touched.Store(true); return nil }
func (o *effectStringObject) String() string               { o.touched.Store(true); return "внешний" }

type effectStringValue struct{ touched *atomic.Bool }

func (v *effectStringValue) String() string { v.touched.Store(true); return "внешний" }

type effectJSONValue struct{ touched *atomic.Bool }

func (v *effectJSONValue) MarshalJSON() ([]byte, error) {
	v.touched.Store(true)
	return []byte(`"внешний"`), nil
}

type effectCompositeValue struct{ Value *effectStringValue }

type effectJSONError struct{ touched *atomic.Bool }

func (e *effectJSONError) Error() string { return "external error" }
func (e *effectJSONError) MarshalJSON() ([]byte, error) {
	e.touched.Store(true)
	return []byte(`{}`), nil
}

type effectRefManager struct{ touched *atomic.Bool }

func (m *effectRefManager) DeleteRef(string) error         { return nil }
func (m *effectRefManager) LoadObject(string) (any, error) { return nil, nil }
func (m *effectRefManager) MarshalJSON() ([]byte, error) {
	m.touched.Store(true)
	return []byte(`{}`), nil
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

func TestУсловиеТочкиОстанова_ЧистаяФункцияФормыРаботает(t *testing.T) {
	const sourceFile = "forms/order/object.form.os"
	main := parseProcFile(t, sourceFile, `Процедура Работа()
  Значение = 1;
КонецПроцедуры`)
	helper := parseProcFile(t, sourceFile, `Функция Удвоить(Значение)
  Возврат Значение * 2;
КонецФункции`)
	var leaked atomic.Bool
	hook := &condHook{expr: "Удвоить(4) = 8 И Проверить(__form_procs__)"}
	interp := interpreter.New()
	interp.DebugSource = func() interpreter.DebugHook { return hook }
	if err := interp.Run(main, nil, map[string]any{
		"__form_procs__": map[string]*ast.ProcedureDecl{"удвоить": helper},
		"Проверить": interpreter.ReadOnlyBuiltinFunc(func(args []any, _ string, _ int) (any, error) {
			if len(args) > 0 {
				_, raw := args[0].(map[string]*ast.ProcedureDecl)
				leaked.Store(raw)
			}
			return true, nil
		}),
	}); err != nil {
		t.Fatalf("прогон формы: %v", err)
	}
	if hook.err != nil || !hook.stopped {
		t.Fatalf("чистая функция той же формы не разрешилась: stopped=%v err=%v", hook.stopped, hook.err)
	}
	if leaked.Load() {
		t.Fatal("внутренняя карта процедур формы стала доступна DSL")
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

func TestУсловиеТочкиОстанова_ReadOnlyФункцияНеВозвращаетWritableCapability(t *testing.T) {
	var touched atomic.Bool
	writer := &effectObject{touched: &touched}
	vars := map[string]any{
		"ПолучитьЗапись": interpreter.ReadOnlyBuiltinFunc(func([]any, string, int) (any, error) {
			return writer, nil
		}),
		"Источник": interpreter.NewStructFromMap(map[string]any{"Наименование": "после"}),
	}
	assertConditionRejectsEffect(t, interpreter.New(),
		"ЗаполнитьЗначенияСвойств(ПолучитьЗапись(), Источник) = Неопределено",
		vars, &touched, "Наименование")
}

func TestУсловиеТочкиОстанова_НеВызываетВнешнийIterator(t *testing.T) {
	var touched atomic.Bool
	helper := parseProcFile(t, "iterator.os", `Функция ЕстьСтроки(Источник)
  Для Каждого Строка Из Источник Цикл
    Возврат Истина;
  КонецЦикла;
  Возврат Ложь;
КонецФункции`)
	interp := interpreter.New()
	interp.LookupProc = func(name string) *ast.ProcedureDecl {
		if strings.EqualFold(name, "ЕстьСтроки") {
			return helper
		}
		return nil
	}
	assertConditionRejectsEffect(t, interp, "ЕстьСтроки(Источник)",
		map[string]any{"Источник": &effectRowsIterator{touched: &touched}},
		&touched, "итерац")
}

func TestУсловиеТочкиОстанова_НеВызываетВнешнийStringer(t *testing.T) {
	for _, expr := range []string{
		`Строка(Объект) = "внешний"`,
		`Объект = "внешний"`,
		`Объект < "внешний"`,
		`"префикс" + Объект = "префиксвнешний"`,
		`Формат(Объект) = "внешний"`,
	} {
		t.Run(expr, func(t *testing.T) {
			var touched atomic.Bool
			assertConditionRejectsEffect(t, interpreter.New(), expr,
				map[string]any{"Объект": &effectStringObject{touched: &touched}},
				&touched, "строк")
		})
	}
}

func TestУсловиеТочкиОстанова_НеВызываетCallbackЗначения(t *testing.T) {
	for _, tc := range []struct {
		name  string
		expr  string
		value func(*atomic.Bool) any
		want  string
	}{
		{
			name: "Stringer в Строка",
			expr: `Строка(Значение) = "внешний"`,
			value: func(touched *atomic.Bool) any {
				return &effectStringValue{touched: touched}
			},
		},
		{
			name: "Stringer в сравнении",
			expr: `Значение = "внешний"`,
			value: func(touched *atomic.Bool) any {
				return &effectStringValue{touched: touched}
			},
		},
		{
			name: "Stringer в сравнении порядка",
			expr: `Значение < "внешний"`,
			value: func(touched *atomic.Bool) any {
				return &effectStringValue{touched: touched}
			},
		},
		{
			name: "Stringer в конкатенации",
			expr: `"префикс" + Значение = "префиксвнешний"`,
			value: func(touched *atomic.Bool) any {
				return &effectStringValue{touched: touched}
			},
		},
		{
			name: "Stringer в Формат",
			expr: `Формат(Значение) = "внешний"`,
			value: func(touched *atomic.Bool) any {
				return &effectStringValue{touched: touched}
			},
		},
		{
			name: "Stringer в строковой функции",
			expr: `СтрДлина(Значение) = 7`,
			value: func(touched *atomic.Bool) any {
				return &effectStringValue{touched: touched}
			},
		},
		{
			name: "Stringer в СтрШаблон",
			expr: `СтрШаблон("%1", Значение) = "внешний"`,
			value: func(touched *atomic.Bool) any {
				return &effectStringValue{touched: touched}
			},
		},
		{
			name: "Stringer в ПрочитатьJSON",
			expr: `ПрочитатьJSON(Значение) = Неопределено`,
			value: func(touched *atomic.Bool) any {
				return &effectStringValue{touched: touched}
			},
			want: "JSON",
		},
		{
			name: "JSON marshaler",
			expr: `ЗаписатьJSON(Значение) = "внешний"`,
			value: func(touched *atomic.Bool) any {
				return &effectJSONValue{touched: touched}
			},
		},
		{
			name: "вложенный Stringer",
			expr: `СтрСоединить(Значение, ",") = "внешний"`,
			value: func(touched *atomic.Bool) any {
				return interpreter.NewArray([]any{&effectStringValue{touched: touched}})
			},
		},
		{
			name: "вложенный JSON marshaler",
			expr: `ЗаписатьJSON(Значение) = "внешний"`,
			value: func(touched *atomic.Bool) any {
				return interpreter.NewArray([]any{&effectJSONValue{touched: touched}})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var touched atomic.Bool
			want := tc.want
			if want == "" {
				want = "внешн"
			}
			assertConditionRejectsEffect(t, interpreter.New(), tc.expr,
				map[string]any{"Значение": tc.value(&touched)},
				&touched, want)
		})
	}
}

func TestУсловиеТочкиОстанова_ОграничиваетОбходВложенныхЗначений(t *testing.T) {
	large := interpreter.NewArray(make([]any, 5000))
	deep := any("лист")
	for range 70 {
		deep = interpreter.NewArray([]any{deep})
	}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "размер", value: large},
		{name: "глубина", value: deep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := runWithCondition(t, `Значение <> Неопределено`, map[string]any{"Значение": tc.value})
			if hook.err == nil {
				t.Fatal("значение сверх лимита read-only traversal принято")
			}
			if !strings.Contains(strings.ToLower(hook.err.Error()), "предел") {
				t.Fatalf("ошибка не объясняет предел обхода: %v", hook.err)
			}
		})
	}
}

func TestУсловиеТочкиОстанова_ОбходитЦиклическуюКоллекциюОдинРаз(t *testing.T) {
	items := []any{nil}
	value := interpreter.NewArray(items)
	items[0] = value
	hook := runWithCondition(t, `Строка(Значение) = "Массив[1]"`, map[string]any{"Значение": value})
	if hook.err != nil || !hook.stopped {
		t.Fatalf("циклическая штатная коллекция: stopped=%v err=%v", hook.stopped, hook.err)
	}
}

func TestУсловиеТочкиОстанова_ШтатныеСтроковыеЗначенияОстаютсяДоступны(t *testing.T) {
	for _, tc := range []struct {
		expr  string
		value any
	}{
		{expr: `Строка(Значение) = "12.5"`, value: decimal.RequireFromString("12.5")},
		{expr: `Строка(Значение) = "Ссылка"`, value: &interpreter.Ref{Name: "Ссылка"}},
		{expr: `Строка(Значение) = "15.08.2026 12:30:00"`, value: time.Date(2026, 8, 15, 12, 30, 0, 0, time.Local)},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			hook := runWithCondition(t, tc.expr, map[string]any{"Значение": tc.value})
			if hook.err != nil || !hook.stopped {
				t.Fatalf("штатное значение отвергнуто: stopped=%v err=%v", hook.stopped, hook.err)
			}
		})
	}
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
