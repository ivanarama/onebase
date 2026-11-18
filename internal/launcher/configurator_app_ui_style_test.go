package launcher

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
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

// TestConfiguratorUIStyle_SelectedAfterReload — выбранный вид доезжает до
// страницы после перезагрузки. Проверка идёт путём пользователя: открыть
// «⚙ Конфигурация», выбрать пункт в списке, сохранить, открыть снова.
//
// Значение в app.yaml сохранялось и раньше, но помечался ли выбранный пункт
// <option selected> — не проверял никто: форма после сохранения показывала
// первый пункт («Обычный»), и пользователь видел настройку, которая сбрасывается
// при каждом обновлении страницы. Причина — html/template считает строку в
// позиции имени атрибута небезопасной и подставляет «ZgotmplZ» вместо
// « selected», а тесты искали значение прямо в тексте страницы и этого не
// видели.
func TestConfiguratorUIStyle_SelectedAfterReload(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	writeCfgFile(t, cfgDir, "config", "app.yaml", "name: Торговля\n")

	router := chi.NewRouter()
	router.Get("/bases/{id}/configurator", h.configuratorPage)
	router.Post("/bases/{id}/configurator/app", h.configuratorSaveApp)

	page := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bases/test/configurator", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("открытие конфигуратора: %d %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	// Исходно выбран обычный вид — это значение по умолчанию.
	if got := browserSubmit(t, page(), "/configurator/app").Get("app_ui_style"); got != project.UIStyleClassic {
		t.Fatalf("до сохранения выбран %q, ожидали %q", got, project.UIStyleClassic)
	}

	// Отправляем ровно то, что прислал бы браузер, поменяв только вид формы:
	// иначе тест проверял бы обработчик на данных, которых интерфейс не шлёт.
	form := browserSubmit(t, page(), "/configurator/app")
	form.Set("app_ui_style", project.UIStyleNew)
	rec := postCfgMultipart(t, "test", "/bases/test/configurator/app", form, h.configuratorSaveApp)
	if ok, errText := cfgResponse(t, rec); !ok {
		t.Fatalf("сохранение не удалось: %s", errText)
	}

	if got := browserSubmit(t, page(), "/configurator/app").Get("app_ui_style"); got != project.UIStyleNew {
		t.Fatalf("после перезагрузки выбран %q, ожидали %q", got, project.UIStyleNew)
	}

	// Метка выбранного пункта должна быть атрибутом, а не заглушкой
	// html/template: при строковом значении в позиции имени атрибута сервер
	// подставляет «ZgotmplZ», и браузер считает список невыбранным.
	pageHTML := page()
	start := strings.Index(pageHTML, `name="app_ui_style"`)
	end := strings.Index(pageHTML[start:], "</select>")
	if start < 0 || end < 0 {
		t.Fatal("в разметке нет списка app_ui_style")
	}
	if strings.Contains(pageHTML[start:start+end], "ZgotmplZ") {
		t.Errorf("выбранный пункт не помечен: %s", pageHTML[start:start+end])
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
