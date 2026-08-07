package launcher

import (
	"strings"
	"testing"
)

// TestCodeTheme_TopbarToggle — светлая тема редактора кода: кнопка в топбаре,
// запоминание выбора и раннее восстановление класса ещё в <head>. Класс ставится
// до отрисовки намеренно: иначе блоки кода успевают мигнуть тёмным, прежде чем
// применится сохранённая светлая тема.
func TestCodeTheme_TopbarToggle(t *testing.T) {
	tree := renderCfgMain(t, richCfgData("tree"))
	if !strings.Contains(tree, `id="cfg-code-theme-toggle"`) {
		t.Error("на вкладке «Дерево» нет кнопки темы редактора кода")
	}
	if !strings.Contains(tree, "cfgCodeThemeToggle()") {
		t.Error("кнопка темы кода не вызывает cfgCodeThemeToggle()")
	}
	// Обе подписи отрисованы в разметке — перевод делает шаблон, JS их не пишет.
	for _, want := range []string{`class="cth-to-light"`, `class="cth-to-dark"`} {
		if !strings.Contains(tree, want) {
			t.Errorf("в кнопке темы кода нет подписи %s", want)
		}
	}
	if !strings.Contains(tree, "cfg-code-light") || !strings.Contains(tree, "cfgCodeTheme") {
		t.Error("в head нет раннего восстановления темы кода из localStorage")
	}

	// Вне «Дерева» редакторов модулей нет — нет и кнопки.
	if out := renderCfgMain(t, richCfgData("files")); strings.Contains(out, `id="cfg-code-theme-toggle"`) {
		t.Error("кнопка темы кода не должна показываться вне «Дерева»")
	}
}

// TestCodeTheme_MonacoAndFallbackFollowToggle — обе поверхности кода слушают один
// тумблер: и Monaco, и блоки pre.os-code с самописной подсветкой, которые видны
// до инициализации редактора. Пока фолбэк красили жёстко, переключение давало
// тёмный блок, светлеющий только по клику.
func TestCodeTheme_MonacoAndFallbackFollowToggle(t *testing.T) {
	js, err := staticFiles.ReadFile("static/configurator.js")
	if err != nil {
		t.Fatalf("static/configurator.js: %v", err)
	}
	src := string(js)
	if !strings.Contains(src, "cfgCodeThemeDefine(monaco)") {
		t.Error("конфигуратор не объявляет темы кода из общего code-theme.js")
	}
	if !strings.Contains(src, "theme: cfgCodeThemeName()") {
		t.Error("редактор конфигуратора создаётся мимо текущей темы")
	}
	if strings.Contains(src, "theme: 'onebase-dark'") {
		t.Error("редактор создаётся с жёстко зашитой тёмной темой — тумблер не подхватится")
	}

	// Сами темы и тумблер живут в общем файле: их использует и конструктор форм.
	shared, err := staticFiles.ReadFile("static/code-theme.js")
	if err != nil {
		t.Fatalf("static/code-theme.js: %v", err)
	}
	sharedSrc := string(shared)
	for _, want := range []string{
		"function cfgCodeThemeName()",                // текущая тема по классу на <html>
		"function cfgCodeThemeToggle()",              // переключение + localStorage
		"function cfgCodeThemeDefine(monaco)",        // объявление тем страницей
		"monaco.editor.defineTheme('onebase-dark'",   // тёмная тема Monaco
		"monaco.editor.defineTheme('onebase-light'",  // светлая тема Monaco
		"monaco.editor.setTheme(cfgCodeThemeName())", // применение к уже открытым редакторам
		"localStorage.setItem('cfgCodeTheme'",        // выбор переживает перезагрузку
	} {
		if !strings.Contains(sharedSrc, want) {
			t.Errorf("в code-theme.js нет %q", want)
		}
	}

	css, err := staticFiles.ReadFile("static/configurator.css")
	if err != nil {
		t.Fatalf("static/configurator.css: %v", err)
	}
	cssSrc := string(css)
	if !strings.Contains(cssSrc, "html.cfg-code-light{") {
		t.Error("в configurator.css нет светлого набора переменных html.cfg-code-light")
	}
	// Цвета подсветки и фон блока — только через переменные: иначе светлая тема
	// красит Monaco, а pre.os-code остаётся тёмным.
	for _, want := range []string{
		"background:var(--cfg-code-bg)",
		".hl-kw{color:var(--cfg-hl-kw)",
		".hl-cmt{color:var(--cfg-hl-cmt)",
	} {
		if !strings.Contains(cssSrc, want) {
			t.Errorf("в configurator.css нет %q", want)
		}
	}
	if strings.Contains(cssSrc, ".hl-str{color:#c3e88d}") {
		t.Error("в configurator.css вернулся жёстко зашитый цвет подсветки — тема на него не влияет")
	}
}

// TestCodeTheme_ConfigHistoryFollowsTheme — просмотр версии в истории
// конфигурации показывает исходник файла, а не вывод консоли, поэтому обязан
// идти за темой: жёсткий тёмный блок посреди светлого редактора выглядел
// чужеродно. Модалка рендерится на странице конфигуратора, где переменные
// объявлены; значения после запятой оставлены для рендера вне неё.
func TestCodeTheme_ConfigHistoryFollowsTheme(t *testing.T) {
	out := configHistoryPre("До", []byte("Процедура Тест()\nКонецПроцедуры\n"))
	for _, want := range []string{"var(--cfg-code-bg,#0f172a)", "var(--cfg-code-fg,#e2e8f0)"} {
		if !strings.Contains(out, want) {
			t.Errorf("просмотр версии не берёт цвет из темы: нет %q", want)
		}
	}
	if strings.Contains(out, "background:#0f172a") {
		t.Error("просмотр версии снова красится жёстким тёмным фоном")
	}
	// Экранирование содержимого не должно пострадать от правки стилей.
	if esc := configHistoryPre("До", []byte("<script>")); !strings.Contains(esc, "&lt;script&gt;") {
		t.Error("содержимое файла версии перестало экранироваться")
	}
}
