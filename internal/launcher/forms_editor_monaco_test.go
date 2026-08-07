package launcher

import (
	"bytes"
	"strings"
	"testing"
)

func renderFormsEditorHTML(t *testing.T) string {
	t.Helper()
	data := &configuratorData{
		Base: &Base{ID: "test-base"},
		EditingForm: &cfgManagedForm{
			Entity: "Контрагент", Name: "ФормаОбъекта", Kind: "object",
			YAML: "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n",
			OS:   "Процедура ПриОткрытииФормы()\nКонецПроцедуры\n",
		},
	}
	var buf bytes.Buffer
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", data); err != nil {
		t.Fatalf("ExecuteTemplate forms-editor: %v", err)
	}
	return buf.String()
}

// TestFormsEditor_LoadsMonaco — на странице конструктора форм не было ни одного
// <script src>: загрузчик Monaco не подключался, поэтому require оставался
// неопределён и редакторы ВСЕГДА уходили в textarea-фолбэк. Код инициализации
// Monaco (включая тему) был мёртв с первого дня — проверяем, что загрузчик на
// месте, иначе регресс повторится незаметно: страница продолжит «работать».
func TestFormsEditor_LoadsMonaco(t *testing.T) {
	out := renderFormsEditorHTML(t)
	if !strings.Contains(out, `<script src="/vendor/monaco/vs/loader.js"`) {
		t.Error("на странице конструктора форм нет загрузчика Monaco — редакторы молча деградируют в textarea")
	}
	// Загрузчик обязан идти РАНЬШЕ кода, который зовёт require: иначе тот же
	// фолбэк, только незаметнее.
	iLoader := strings.Index(out, "/vendor/monaco/vs/loader.js")
	iRequire := strings.Index(out, "require([")
	if iLoader < 0 || iRequire < 0 || iLoader > iRequire {
		t.Errorf("загрузчик Monaco (%d) должен подключаться раньше require([ (%d)", iLoader, iRequire)
	}
	// Фолбэк остаётся: при недоступном loader.js форма всё равно редактируется.
	if !strings.Contains(out, "buildFallback()") {
		t.Error("textarea-фолбэк убран — при сбое загрузки Monaco форму станет нечем править")
	}
	if !strings.Contains(out, `onerror="window._monacoLoadErr=`) {
		t.Error("у загрузчика Monaco нет onerror — сбой загрузки останется без следа")
	}
}

// TestFormsEditor_CodeThemeShared — конструктор форм и конфигуратор делят одну
// тему кода (один origin — один localStorage). Но пока выбор не сделан явно,
// эта страница остаётся светлой, какой была: общий дефолт перекрасил бы её
// молча, а конфигуратор по той же причине остаётся тёмным.
func TestFormsEditor_CodeThemeShared(t *testing.T) {
	out := renderFormsEditorHTML(t)
	if !strings.Contains(out, `<script src="/static/code-theme.js">`) {
		t.Error("конструктор форм не подключает общий code-theme.js")
	}
	if !strings.Contains(out, "window.cfgCodeThemeDefault='light'") {
		t.Error("у конструктора форм должен быть светлый дефолт — иначе страница молча почернеет")
	}
	if !strings.Contains(out, "cfgCodeThemeDefine(monaco)") {
		t.Error("конструктор форм не объявляет темы кода")
	}
	if strings.Contains(out, "theme: 'vs-light'") {
		t.Error("редакторы конструктора форм снова с жёстко зашитой темой")
	}
	if got := strings.Count(out, "theme: cfgCodeThemeName()"); got != 2 {
		t.Errorf("оба редактора (YAML и .os) должны брать текущую тему, найдено %d", got)
	}
	if !strings.Contains(out, `id="cfg-code-theme-toggle"`) || !strings.Contains(out, "cfgCodeThemeToggle()") {
		t.Error("на странице конструктора форм нет кнопки переключения темы")
	}
	// Класс восстанавливается до отрисовки — иначе редакторы мигнут чужой темой.
	iRestore := strings.Index(out, "cfgCodeTheme")
	iBody := strings.Index(out, "<body>")
	if iRestore < 0 || iBody < 0 || iRestore > iBody {
		t.Errorf("восстановление темы (%d) должно идти до <body> (%d)", iRestore, iBody)
	}
}
