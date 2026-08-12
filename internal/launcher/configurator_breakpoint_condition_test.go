package launcher

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func configuratorCSS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("static/configurator.css")
	if err != nil {
		t.Fatalf("read static/configurator.css: %v", err)
	}
	return string(b)
}

// Условные точки останова (план 99). Половина фичи живёт в конфигураторе:
// сервер вычисляет условие, только если оно до него доехало. Тест держит эту
// проводку — payload с condition, ввод условия и отдельный вид у условной точки.
func TestConfiguratorJS_BreakpointConditionWired(t *testing.T) {
	js := configuratorJS(t)
	for _, want := range []string{
		"function dbgEditBPCondition", // ввод условия
		"function dbgBPCondition",     // условие в локальном состоянии
		"e.event.altKey",              // Alt+клик по колонке редактора
		"condition: condition",        // условие уезжает на сервер
		"dbg-bp-cond",                 // поле условия в панели точек
		"cond_error",                  // ошибка вычисления видна человеку
		"dbg-bp-glyph-cond",           // условная точка отличается на глаз
		"function dbgCaptureLocalBP",  // снимок до оптимистичного изменения
		"function dbgRestoreLocalBP",  // rollback к подтверждённому состоянию
		"var _dbgBPSync",              // очередь и confirmed state на file:line
		"function dbgBPSyncState",
		"state.tail.then",   // изменения одной точки сериализованы
		"action = 'remove'", // toggle отправляется как явное состояние
		"dbgRestoreLocalBP(file, line, state.confirmed)",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("в configurator.js нет %q — условие точки останова не доедет до сервера", want)
		}
	}
}

// Поле условия должно быть в панели точек останова, иначе задать его можно
// только через Alt+клик, а из панели — никак.
func TestConfiguratorShell_HasBreakpointConditionInput(t *testing.T) {
	data := &configuratorData{
		Base: &Base{ID: "b", Name: "Т", ConfigSource: "file"}, Lang: "ru", Tab: "tree",
	}
	var buf bytes.Buffer
	if err := cfgTmpl.ExecuteTemplate(&buf, "cfg-foot", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	for _, want := range []string{`id="dbg-bp-cond"`, "dbgManualBP()"} {
		if !strings.Contains(html, want) {
			t.Fatalf("в панели отладки нет %q", want)
		}
	}
}

// Стиль условной точки обязан отличаться от обычной: одинаковый кружок в
// колонке означал бы «точка стоит, а прогон её проходит» без объяснений.
func TestConfiguratorCSS_ConditionalBreakpointGlyph(t *testing.T) {
	css := configuratorCSS(t)
	for _, want := range []string{".dbg-bp-glyph-cond", ".bp-cond-err", ".bp-cond-edit"} {
		if !strings.Contains(css, want) {
			t.Fatalf("в configurator.css нет %q", want)
		}
	}
}
