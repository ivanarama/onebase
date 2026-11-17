package launcher

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
)

// TestConfiguratorSaveApp_UIStyle — вид форм выбирается в панели «Конфигурация»
// и пишется вложенным ключом ui.style. Значение classic удаляет ключ, чтобы
// после возврата к виду по умолчанию в файле не оставалось «ui: {}» или
// висящего style.
func TestConfiguratorSaveApp_UIStyle(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	appPath := writeCfgFile(t, cfgDir, "config", "app.yaml", appYAMLWithForeignBlocks)

	save := func(style string) {
		t.Helper()
		rec := postCfgMultipart(t, "test", "/bases/test/configurator/app", url.Values{
			"app_name":     {"Торговля"},
			"app_ui_style": {style},
		}, h.configuratorSaveApp)
		if ok, errText := cfgResponse(t, rec); !ok {
			t.Fatalf("сохранение вида %q не удалось: %s", style, errText)
		}
	}

	save(project.UIStyleNew)
	cfg, err := project.LoadConfig(cfgDir)
	if err != nil {
		t.Fatalf("app.yaml после сохранения не разбирается: %v", err)
	}
	if style, err := cfg.NormalizedUIStyle(); err != nil || style != project.UIStyleNew {
		t.Fatalf("вид после сохранения = %q, %v; ожидали %q", style, err, project.UIStyleNew)
	}
	// Чужие блоки обязаны пережить правку вложенного ключа.
	assertFileContains(t, appPath, "smtp_host: smtp.example.org", "${env:S3_KEY}")

	save(project.UIStyleClassic)
	raw, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("чтение app.yaml: %v", err)
	}
	assertAppConfigLoads(t, raw)
	if strings.Contains(string(raw), "style:") {
		t.Errorf("выбор обычного вида не удалил ui.style:\n%s", raw)
	}

	save(project.UIStyleNew)
	cfg, err = project.LoadConfig(cfgDir)
	if err != nil {
		t.Fatalf("app.yaml после повторного включения не разбирается: %v", err)
	}
	if style, _ := cfg.NormalizedUIStyle(); style != project.UIStyleNew {
		t.Fatalf("вид после повторного включения = %q", style)
	}
}

// TestConfiguratorSaveApp_OldFormKeepsUIStyle — страница без поля вида (старая
// вкладка, отправка из другого места) не должна сбрасывать выставленный вид:
// иначе сохранение названия молча возвращало бы обычный вид.
func TestConfiguratorSaveApp_OldFormKeepsUIStyle(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	writeCfgFile(t, cfgDir, "config", "app.yaml",
		"name: Торговля\nui:\n  style: new-style\n")

	rec := postCfgMultipart(t, "test", "/bases/test/configurator/app", url.Values{
		"app_name": {"Торговля"},
	}, h.configuratorSaveApp)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	cfg, err := project.LoadConfig(cfgDir)
	if err != nil {
		t.Fatalf("app.yaml не разбирается: %v", err)
	}
	if style, _ := cfg.NormalizedUIStyle(); style != project.UIStyleNew {
		t.Fatalf("вид сброшен формой без поля: %q", style)
	}
}

// TestConfiguratorSaveApp_RejectsUnknownUIStyle — подставленное значение не
// должно попасть в app.yaml: иначе следующая же проверка конфигурации падала бы
// на файле, который записал сам конфигуратор.
func TestConfiguratorSaveApp_RejectsUnknownUIStyle(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	appPath := writeCfgFile(t, cfgDir, "config", "app.yaml", "name: Торговля\n")

	rec := postCfgMultipart(t, "test", "/bases/test/configurator/app", url.Values{
		"app_name":     {"Торговля"},
		"app_ui_style": {"new_style"},
	}, h.configuratorSaveApp)
	if ok, _ := cfgResponse(t, rec); ok {
		t.Fatal("неизвестный вид формы сохранён без ошибки")
	}

	raw, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("чтение app.yaml: %v", err)
	}
	if strings.Contains(string(raw), "new_style") {
		t.Errorf("неизвестный вид попал в app.yaml:\n%s", raw)
	}
}
