package launcher

import (
	"strings"
	"testing"
)

func TestConfigurator_CheckRendersStructuredIssueFields(t *testing.T) {
	js := configuratorJS(t)
	for _, sub := range []string{"i.code", "i.suggestedFix"} {
		if !strings.Contains(js, sub) {
			t.Errorf("configurator.js не выводит structured поле %q", sub)
		}
	}
}
