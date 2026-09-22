package interpreter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Регрессия #1560: в условии точки останова ЭтотОбъект["Реквизит"] обязан
// читать так же, как ЭтотОбъект.Реквизит. Оболочка readOnlyThis не
// проксировала DynamicFieldAccessor, и условие падало с «не поддерживает
// индексное чтение», хотя dotted-форма работала.

func conditionWriter(t *testing.T) *interpreter.CatalogRecordWriter {
	t.Helper()
	ent := &metadata.Entity{
		Name: "Т", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	proxy := interpreter.NewCatalogProxy(ent, nil, nil)
	w, ok := proxy.CallMethod("создать", nil).(*interpreter.CatalogRecordWriter)
	require.True(t, ok, "создать вернул не CatalogRecordWriter")
	w.Set("Код", "А-1")
	return w
}

func runConditionOnThis(t *testing.T, this *interpreter.CatalogRecordWriter, condExpr string) *condHook {
	t.Helper()
	hook := &condHook{expr: condExpr}
	interp := interpreter.New()
	interp.DebugSource = func() interpreter.DebugHook { return hook }
	proc := parseProcFile(t, "cond.os", `Процедура Работа()
  Х = 1;
КонецПроцедуры`)
	if err := interp.Run(proc, this, nil); err != nil {
		t.Fatalf("прогон отлаживаемой процедуры: %v", err)
	}
	if !hook.asked.Load() {
		t.Fatal("условие не проверялось — тест не про то, что задуман")
	}
	return hook
}

func TestBreakpointCondition_IndexReadWorksLikeDotted(t *testing.T) {
	w := conditionWriter(t)

	hook := runConditionOnThis(t, w, `ЭтотОбъект["Код"] = "А-1"`)
	if hook.err != nil {
		t.Fatalf("индексное чтение в условии упало: %v", hook.err)
	}
	if !hook.stopped {
		t.Fatal("истинное индексное условие не остановило исполнение")
	}

	dotted := runConditionOnThis(t, w, `ЭтотОбъект.Код = "А-1"`)
	if dotted.err != nil || !dotted.stopped {
		t.Fatalf("dotted-форма сломана: stopped=%v err=%v", dotted.stopped, dotted.err)
	}
}

func TestBreakpointCondition_IndexReadKnownEmptyField(t *testing.T) {
	// Наименование объявлено, но не заполнено: known-empty, не unknown.
	w := conditionWriter(t)
	hook := runConditionOnThis(t, w, `ЭтотОбъект["Наименование"] = ""`)
	if hook.err != nil {
		t.Fatalf("пустой известный реквизит должен читаться: %v", hook.err)
	}
	if !hook.stopped {
		t.Fatal("условие на пустом известном реквизите не сработало")
	}
}

func TestBreakpointCondition_IndexReadUnknownFieldStaysError(t *testing.T) {
	w := conditionWriter(t)
	hook := runConditionOnThis(t, w, `ЭтотОбъект["НетТакогоРеквизита"] = "х"`)
	if hook.err == nil {
		t.Fatal("неизвестный реквизит должен давать ошибку, а не Неопределено")
	}
	if hook.stopped {
		t.Fatal("условие на неизвестном реквизите не должно останавливать")
	}
}

func TestBreakpointCondition_IndexReadDoesNotMutate(t *testing.T) {
	w := conditionWriter(t)
	runConditionOnThis(t, w, `ЭтотОбъект["Код"] = "А-1"`)
	if got := w.Get("Код"); got != "А-1" {
		t.Fatalf("значение реквизита изменилось: %v", got)
	}
}
