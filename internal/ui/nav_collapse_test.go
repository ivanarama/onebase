package ui

import (
	"bytes"
	"strings"
	"testing"
)

// Схлопывание панели объектов на широком экране (#1122, план 157).
//
// Проверяется то, что раньше делало переключатель бессмысленным на десктопе:
// кнопка была заперта в `@media (max-width:820px)`, а класс, который она
// ставила, на широком экране не адресовался ни одним правилом CSS.

// mediaBlock возвращает тело @media-блока, начинающегося с prefix, считая
// фигурные скобки. Простой поиск подстроки тут не годится: надо отличить
// правило внутри блока от одноимённого правила снаружи.
func mediaBlock(t *testing.T, css, prefix string) string {
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

func headCSS(t *testing.T) string {
	t.Helper()
	data := map[string]any{"Cfg": Config{AppName: "Test"}, "Lang": "ru"}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "head", data); err != nil {
		t.Fatalf("ExecuteTemplate head: %v", err)
	}
	return buf.String()
}

// Кнопка-гамбургер видна на любой ширине, а не только внутри мобильного @media.
func TestNavToggle_VisibleOnWideScreen(t *testing.T) {
	css := headCSS(t)

	if strings.Contains(css, ".nav-toggle{display:none}") {
		t.Error("`.nav-toggle{display:none}` вернулось: на широком экране кнопки снова нет")
	}
	if !strings.Contains(css, ".nav-toggle{display:inline-flex") {
		t.Error("нет базового правила видимости `.nav-toggle`")
	}
	if narrow := mediaBlock(t, css, "@media (max-width:820px){"); strings.Contains(narrow, ".nav-toggle{") {
		t.Error("видимость `.nav-toggle` снова заперта в @media (max-width:820px)")
	}
}

// Схлопывание адресовано только широким экраном: на узком ширину панели
// забирает шторка `nav-open`, и вмешательство `nav-collapsed` там сломало бы её.
func TestNavCollapsed_ScopedToWideScreen(t *testing.T) {
	css := headCSS(t)

	wide := mediaBlock(t, css, "@media (min-width:821px){")
	if !strings.Contains(wide, "html.nav-collapsed #ob-nav{display:none}") {
		t.Error("в @media (min-width:821px) нет правила, скрывающего #ob-nav по nav-collapsed")
	}
	narrow := mediaBlock(t, css, "@media (max-width:820px){")
	if strings.Contains(narrow, "nav-collapsed") {
		t.Error("nav-collapsed адресуется на узком экране — конфликтует со шторкой nav-open")
	}
	if !strings.Contains(narrow, "body.nav-open aside{transform:translateX(0)}") {
		t.Error("мобильная шторка задета: пропало правило body.nav-open")
	}
}

// Кнопка есть в разметке, и она указывает на панель для скринридера.
func TestNavToggle_Markup(t *testing.T) {
	data := map[string]any{
		"Cfg": Config{AppName: "Test"}, "Lang": "ru",
		"Nav": nil, "Subsystems": nil, "SearchQuery": "", "FormOpenMode": "pages",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "nav", data); err != nil {
		t.Fatalf("ExecuteTemplate nav: %v", err)
	}
	html := buf.String()
	for _, want := range []string{`class="nav-toggle"`, `data-ob-nav-toggle`, `aria-controls="ob-nav"`} {
		if !strings.Contains(html, want) {
			t.Errorf("в разметке топбара нет %q", want)
		}
	}
}

// Класс восстанавливается синхронно, до отрисовки. Режим открытия форм по
// умолчанию — «Страницы», то есть перезагрузка на каждом переходе: класс,
// поставленный после DOMContentLoaded, дал бы мигание панелью на каждой
// странице. Поэтому чтение localStorage обязано стоять в начале файла, а не
// внутри obReady.
func TestNavCollapsed_RestoredSynchronously(t *testing.T) {
	js := string(uiJS)

	read := strings.Index(js, "localStorage.getItem(window.OB_NAV_COLLAPSED_KEY)")
	if read < 0 {
		t.Fatal("ui.js не читает сохранённое состояние панели")
	}
	if ready := strings.Index(js, "obReady("); ready >= 0 && read > ready {
		t.Error("состояние панели восстанавливается после obReady — панель будет мигать на каждом переходе")
	}
	// В <head> body ещё null, поэтому класс идёт на documentElement — отсюда и
	// селектор `html.nav-collapsed` в CSS.
	if !strings.Contains(js, "document.documentElement.className += ' nav-collapsed'") {
		t.Error("класс nav-collapsed ставится не на documentElement — в <head> body ещё нет")
	}
	// Переключатель обязан развилкой различать шторку и схлопывание.
	if !strings.Contains(js, "if (navNarrow()) setNav(") {
		t.Error("obNavToggle не разводит узкий и широкий экран")
	}
}
