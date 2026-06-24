package launcher

import (
	"os"
	"strings"
	"testing"
)

// TestConfigurator_GeneratePanelWired проверяет, что панель генерации каркаса
// ИИ подключена в подвал конфигуратора: присутствуют ключевые элементы UI и
// вызовы эндпоинтов ai-generate/ai-apply.
//
// Тест подобран из ветки этапа 2b (план 57) и адаптирован под реализацию
// этапа 3: кнопка отклонения (cfggen-reject) была убрана при упрощении панели,
// закрытие выполняется через cfggen-close.
func TestConfigurator_GeneratePanelWired(t *testing.T) {
	// HTML-панель остаётся в шаблоне; вызовы эндпоинтов уехали в
	// /static/configurator.js (план 55 фаза 2b-2).
	html := renderCfgFoot(t)
	for _, sub := range []string{
		"cfggen-panel", "cfggen-prompt", "cfggen-send", "cfggen-apply",
	} {
		if !strings.Contains(html, sub) {
			t.Errorf("в cfg-foot нет %q — панель генерации не подключена", sub)
		}
	}
	js := configuratorJS(t)
	for _, sub := range []string{
		"configurator/ai-generate", "configurator/ai-apply", "cfggen-change-check",
		"cfggen-new-content", "selectedChanges", "oldContent", "toolTrace", "Tool trace:",
		"cfggen-check", "checkText", "repairRounds",
	} {
		if !strings.Contains(js, sub) {
			t.Errorf("в configurator.js нет %q — панель генерации не подключена", sub)
		}
	}
}

func TestAISettings_LimitFieldsWired(t *testing.T) {
	b, err := os.ReadFile("static/ai-settings.js")
	if err != nil {
		t.Fatalf("read static/ai-settings.js: %v", err)
	}
	js := string(b)
	for _, sub := range []string{"ai.max_tool_rounds", "max_tool_rounds"} {
		if !strings.Contains(js, sub) {
			t.Errorf("в ai-settings.js нет %q — настройка лимита tool-use не подключена", sub)
		}
	}
}
