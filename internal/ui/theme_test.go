package ui

import (
	"bytes"
	"fmt"
	"html/template"
	"math"
	"regexp"
	"strings"
	"testing"
)

// Тёмная тема enterprise-UI (план 218).
//
// Проверяется каркас: токены палитры в базовом <style>, режим темы через
// data-theme на <html>, переключатель в системном меню, head-скрипт, который
// ставит тему до первой отрисовки, и контраст ключевых текстовых пар —
// числами, а не на глаз.

// tokenBlock возвращает тело блока токенов (:root или html[data-theme="..."]).
func tokenBlock(t *testing.T, css, prefix string) string {
	t.Helper()
	i := strings.Index(css, prefix)
	if i < 0 {
		t.Fatalf("в CSS нет блока %q", prefix)
	}
	rest := css[i+len(prefix):]
	depth := 1
	for j, r := range rest {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[:j]
			}
		}
	}
	t.Fatalf("незакрытый блок %q", prefix)
	return ""
}

var tokenRe = regexp.MustCompile(`(--ob-[a-z0-9-]+):(#[0-9a-fA-F]{3,6})`)

func tokensIn(t *testing.T, body string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, mm := range tokenRe.FindAllStringSubmatch(body, -1) {
		token := mm[1]
		m[token] = strings.ToLower(mm[2][1:])
	}
	if len(m) < 30 {
		t.Fatalf("разобрано только %d токенов темы", len(m))
	}
	return m
}

// Контраст по WCAG 2.1: relative luminance + ratio.
func hexLum(in string) float64 {
	hex := in
	if len(hex) == 3 {
		hex = strings.Repeat(hex[0:1], 2) + strings.Repeat(hex[1:2], 2) + strings.Repeat(hex[2:3], 2)
	}
	var c [3]float64
	for i := 0; i < 3; i++ {
		var n int
		fmt.Sscanf(hex[i*2:i*2+2], "%02x", &n)
		c[i] = float64(n) / 255
		if c[i] <= 0.04045 {
			c[i] = c[i] / 12.92
		} else {
			c[i] = math.Pow((c[i]+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

func contrast(hex1, hex2 string) float64 {
	l1, l2 := hexLum(hex1), hexLum(hex2)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// Ключевые текстовые пары, которым норматив требует >= 4.5:1 (обычный текст):
// основной текст на поверхностях, навигация и шапка, поля, вторичный текст.
var contrastPairs = []struct{ fg, bg string }{
	{"--ob-text", "--ob-bg"},
	{"--ob-text", "--ob-surface"},
	{"--ob-text", "--ob-surface-2"},
	{"--ob-text", "--ob-surface-3"},
	{"--ob-text", "--ob-zebra"},
	{"--ob-text-2", "--ob-surface"},
	{"--ob-text-2", "--ob-surface-3"},
	{"--ob-text-3", "--ob-surface"},
	{"--ob-text-3", "--ob-surface-2"},
	{"--ob-text-3", "--ob-surface-3"},
	{"--ob-text-4", "--ob-surface-3"},
	{"--ob-nav-text", "--ob-nav-bg"},
	{"--ob-topbar-text", "--ob-topbar-bg"},
	{"--ob-field-text", "--ob-field-bg"},
	{"--ob-btn-2-text", "--ob-btn-2-bg"},
	{"--ob-error-text", "--ob-error-bg"},
	{"--ob-console-ink", "--ob-nav-bg"},
	{"--ob-console-err", "--ob-nav-bg"},
	{"--ob-text-4", "--ob-nav-bg"},
}

// Базовый <style> объявляет токены и режим темы; light-токены при этом равны
// прежним hardcoded-цветам, светлая тема не меняется ни на пиксель.
func TestTheme_TokensInBaseStyle(t *testing.T) {
	css := headCSS(t)

	light := tokenBlock(t, css, ":root{")
	if !strings.Contains(light, "--ob-bg:#f5f5f5") || !strings.Contains(light, "--ob-surface:#fff") {
		t.Error("light-токены не совпадают с прежней палитрой")
	}
	dark := tokenBlock(t, css, `html[data-theme="dark"]{`)
	if !strings.Contains(dark, "--ob-surface:#111c2e") {
		t.Error("dark-токены surfaces не согласованы с тёмной шапкой")
	}
	if !strings.Contains(css, `html[data-theme="light"]{color-scheme:light}`) {
		t.Error("нет color-scheme:light для явного светлого режима (скролбары, natивные контролы)")
	}
	if !strings.Contains(dark, "color-scheme:dark") {
		t.Error("в dark-блоке нет color-scheme:dark")
	}
}

// Контраст dark-режима: ключевые пары >= 4.5:1 по WCAG.
func TestTheme_DarkContrast(t *testing.T) {
	dark := tokensIn(t, tokenBlock(t, headCSS(t), `html[data-theme="dark"]{`))
	for _, p := range contrastPairs {
		r := contrast(dark[p.fg], dark[p.bg])
		if r < 4.5 {
			t.Errorf("dark: %s на %s контраст %.2f < 4.5", p.fg, p.bg, r)
		}
	}
}

// Контраст light-режима не хуже прежнего: пары >= 4.5:1. Каркас light-токены
// перенёс побайтно, поэтому две пары с прежним уровнем (подсказки placeholder
// на самой светлой поверхности, текст ошибки на светлом фоне ошибки) тест
// фиксирует числами, а не меняет пиксели: за ними — оверрайд-PR.
func TestTheme_LightContrast(t *testing.T) {
	light := tokensIn(t, tokenBlock(t, headCSS(t), ":root{"))
	known := map[string]float64{
		"--ob-text-3 на --ob-surface-3":    4.03,
		"--ob-text-4 на --ob-surface-3":    2.34,
		"--ob-error-text на --ob-error-bg": 4.41,
	}
	for _, p := range contrastPairs {
		key := p.fg + " на " + p.bg
		r := contrast(light[p.fg], light[p.bg])
		if want, ok := known[key]; ok {
			if r < want-0.01 {
				t.Errorf("light: %s контраст %.2f стал хуже прежнего %.2f", key, r, want)
			}
			continue
		}
		if r < 4.5 {
			t.Errorf("light: %s на %s контраст %.2f < 4.5", p.fg, p.bg, r)
		}
	}
}

// Переключатель режима живёт в системном меню: три радиокнопки, выбор
// применяется сразу, без POST-формы.
func TestTheme_SwitchInSystemMenu(t *testing.T) {
	data := map[string]any{
		"Cfg": Config{AppName: "Test"}, "Lang": "ru",
		"Nav": nil, "Subsystems": nil, "SearchQuery": "", "FormOpenMode": "pages",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "nav", data); err != nil {
		t.Fatalf("ExecuteTemplate nav: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`name="ui-theme"`, `data-ob-theme-radio`, `value="light"`, `value="dark"`, `value="system"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в системном меню нет %q", want)
		}
	}
	if strings.Contains(html, `<form method="post" action="/ui/ui-theme`) {
		t.Error("переключатель темы сделан POST-формой — сервер о теме не знает")
	}
}

// data-theme ставится в <head>, до первой отрисовки: на «Страницах» каждый
// переход — перезагрузка, и запоздалая тема дала бы белое мигание карточек.
// По образцу nav-collapsed (#1122): чтение localStorage в начале файла,
// documentElement вместо body, запись режима при выборе.
func TestTheme_HeadScriptAppliesBeforeFirstPaint(t *testing.T) {
	js := string(uiJS)

	if !strings.Contains(js, "window.OB_UI_THEME_KEY = 'obUITheme'") {
		t.Error("ui.js не объявляет ключ темы obUITheme")
	}
	read := strings.Index(js, "localStorage.getItem(window.OB_UI_THEME_KEY)")
	if read < 0 {
		t.Fatal("ui.js не читает сохранённый режим темы")
	}
	if ready := strings.Index(js, "obReady("); ready >= 0 && read > ready {
		t.Error("тема применяется после obReady — будет белое мигание на каждом переходе")
	}
	if !strings.Contains(js, `el.setAttribute('data-theme', dark ? 'dark' : 'light')`) {
		t.Error("data-theme ставится не на documentElement (в <head> body ещё null)")
	}
	if !strings.Contains(js, "window.OB_UI_THEME_SET = setMode") {
		t.Error("нет точки применения выбора пользователя OB_UI_THEME_SET")
	}
	if !strings.Contains(js, "localStorage.setItem(window.OB_UI_THEME_KEY, mode)") {
		t.Error("выбранный режим не сохраняется в localStorage")
	}
	if !strings.Contains(js, "matchMedia('(prefers-color-scheme: dark)')") {
		t.Error("режим system не разрешается через prefers-color-scheme")
	}
	// Радио-кнопки синхронизируются с фактическим режимом при готовности DOM.
	if !strings.Contains(js, "querySelectorAll('[data-ob-theme-radio]')") {
		t.Error("радиокнопки темы не синхронизируются с режимом")
	}
}

// Оверрайд-поверхности (окно подбора, контекстное меню, панель сообщений)
// красятся токенами, а не фиксированным light-hex: иначе в тёмном режиме они
// останутся белыми пятнами.
func TestTheme_OverlaySurfacesUseTokens(t *testing.T) {
	js := string(uiJS)

	for _, want := range []string{
		"background:var(--ob-surface)",
		"border:1px solid var(--ob-border-2)",
		"color:var(--ob-text)",
		"background:var(--ob-surface-3)",
		"color:var(--ob-flag)",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("в JS-оверраях нет %q (окно подбора / контекстное меню / панель сообщений)", want)
		}
	}
}

// Консоли (запросов, кода) следуют теме enterprise-UI. Monaco принимает
// literal-цвета, поэтому ui.js объявляет об-ui-light/ob-ui-dark с палитрой,
// согласованной с --ob-* токенами, и подписывается на смену data-theme.
// В шаблонах консолей не остаётся фиксированных light-hex (#475569, #334155)
// и theme:'vs' — иначе в тёмном режиме редактор и вывод останутся белыми.
func TestTheme_DevConsolesFollowTheme(t *testing.T) {
	js := string(uiJS)

	// Monaco-помощник в ui.js.
	for _, want := range []string{
		"window.obMonacoThemeName",
		"window.obMonacoDefine",
		"defineTheme('ob-ui-light'",
		"defineTheme('ob-ui-dark'",
		"monaco.editor.setTheme(window.obMonacoThemeName())",
		"window.OB_UI_THEME_SUBSCRIBE(function () {",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("в ui.js нет %q (Monaco-помощник темы)", want)
		}
	}
	if !strings.Contains(js, "documentElement.getAttribute('data-theme')") {
		t.Error("obMonacoThemeName читает data-theme не с documentElement")
	}
	// Monaco не понимает CSS-переменные — цвета literal-hex.
	if !strings.Contains(js, "'editor.background': '#ffffff'") {
		t.Error("light-режим Monaco не согласован с --ob-surface (light #fff)")
	}
	if !strings.Contains(js, "'editor.background': '#111c2e'") {
		t.Error("dark-режим Monaco не согласован с --ob-surface (dark #111c2e)")
	}

	// Шаблоны консолей вызывают помощник и не держат фиксированный light-хром.
	data := map[string]any{
		"Cfg": Config{AppName: "Test"}, "Lang": "ru",
		"Nav": nil, "Subsystems": nil, "SearchQuery": "", "FormOpenMode": "pages",
		"Schema": template.JS("[]"),
	}
	for _, name := range []string{"page-query-console", "page-code-console"} {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
			t.Fatalf("ExecuteTemplate %s: %v", name, err)
		}
		html := buf.String()
		if !strings.Contains(html, "window.obMonacoDefine(monaco)") {
			t.Errorf("%s: редактор не определяeт темы ob-ui-light/ob-ui-dark", name)
		}
		if strings.Contains(html, "theme: 'vs'") {
			t.Errorf("%s: theme:'vs' заперт — в тёмном режиме редактор останется белым", name)
		}
		if !strings.Contains(html, "window.obMonacoThemeName()") {
			t.Errorf("%s: тема Monaco не берётся из data-theme", name)
		}
	}

	// Консоль запросов: переключатель конструктора красится токеном, а не
	// фиксированным #475569.
	var qbuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&qbuf, "page-query-console", data); err != nil {
		t.Fatalf("ExecuteTemplate page-query-console: %v", err)
	}
	qhtml := consoleMain(t, qbuf.String())
	if !strings.Contains(qhtml, "btn.style.color = 'var(--ob-text-2)'") {
		t.Error("qcToggleBuilder красит выключенный конструктор фиксированным light-hex")
	}

	// Консоль кода: вывод и разделитель — токены, #334155 не остаётся.
	// Проверка по <main>, чтобы не задеть <head> (там токены light-палитры
	// и #334155 как значение токена).
	var cbuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&cbuf, "page-code-console", data); err != nil {
		t.Fatalf("ExecuteTemplate page-code-console: %v", err)
	}
	chtml := consoleMain(t, cbuf.String())
	for _, want := range []string{
		"var(--ob-console-ink)",
		"var(--ob-console-sep)",
		"var(--ob-console-err)",
	} {
		if !strings.Contains(chtml, want) {
			t.Errorf("консоль кода не красит вывод токеном %q", want)
		}
	}
	for _, banned := range []string{"#334155", "var(--ob-red-border)", "var(--ob-btn-2-bg)"} {
		if strings.Contains(chtml, banned) {
			t.Errorf("консоль кода держит фиксированный light-токен %q — в тёмном режиме контраст сломан", banned)
		}
	}
}

// consoleMain возвращает содержимое страницы после <main> — тело консоли и
// её <script>-и, не трогая <head> (там токены light-палитры содержат
// фиксированные hex, они в head легальны: это значения самих токенов).
func consoleMain(t *testing.T, html string) string {
	t.Helper()
	i := strings.Index(html, "<main")
	if i < 0 {
		t.Fatalf("в рендере консоли нет <main>")
	}
	return html[i:]
}
