package launcher

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

var (
	applicableEventRuleRE  = regexp.MustCompile(`(?s)((?:\s*case\s+'[^']+'\s*:)+)\s*return\s*\[([^]]*)\]\s*;`)
	applicableEventCaseRE  = regexp.MustCompile(`case\s+'([^']+)'\s*:`)
	applicableEventValueRE = regexp.MustCompile(`'([^']+)'`)
)

// Панель редактора — подсказка, а серверный allow-list — контракт. Панель
// вправе показывать не все разрешённые события, но не должна предлагать пару,
// которую сервер затем отклонит в браузерной точке входа (#1162).
func TestРедакторФорм_ПредложенныеСобытияРазрешеныСервером(t *testing.T) {
	script := formsEditorScript(t)
	start := strings.Index(script, "function applicableEvents(kind)")
	if start < 0 {
		t.Fatal("в отрисованном редакторе нет applicableEvents")
	}
	end := strings.Index(script[start:], "function formEvents()")
	if end < 0 {
		t.Fatal("не найдена граница applicableEvents в отрисованном редакторе")
	}
	fn := script[start : start+end]

	rules := applicableEventRuleRE.FindAllStringSubmatch(fn, -1)
	allCases := applicableEventCaseRE.FindAllStringSubmatch(fn, -1)
	parsedCases := 0
	for _, rule := range rules {
		kinds := applicableEventCaseRE.FindAllStringSubmatch(rule[1], -1)
		events := applicableEventValueRE.FindAllStringSubmatch(rule[2], -1)
		parsedCases += len(kinds)
		if len(events) == 0 {
			t.Fatalf("правило панели без событий: %q", rule[0])
		}
		for _, kindMatch := range kinds {
			kind := metadata.FormElementType(kindMatch[1])
			if !metadata.IsKnownFormElementType(kind) {
				t.Errorf("панель предлагает события неизвестному виду элемента %q", kind)
				continue
			}
			for _, eventMatch := range events {
				event := metadata.FormEventType(eventMatch[1])
				if !metadata.BrowserFormEventAllowed(kind, event) {
					t.Errorf("панель предлагает %q для %q, но сервер эту пару отклоняет", event, kind)
				}
			}
		}
	}
	if len(rules) == 0 || parsedCases != len(allCases) {
		t.Fatalf("разобрано %d из %d веток applicableEvents; обнови тест-парсер вместе с шаблоном", parsedCases, len(allCases))
	}
}
