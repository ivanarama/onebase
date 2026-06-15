package launcher

import (
	"strings"
	"testing"
)

func TestConfigurator_GeneratePanelWired(t *testing.T) {
	html := renderCfgFoot(t)
	for _, sub := range []string{
		"cfggen-panel", "cfggen-prompt", "cfggen-send", "cfggen-apply", "cfggen-reject",
		"configurator/ai-generate", "configurator/ai-apply",
	} {
		if !strings.Contains(html, sub) {
			t.Errorf("в cfg-foot нет %q — панель генерации не подключена", sub)
		}
	}
}
