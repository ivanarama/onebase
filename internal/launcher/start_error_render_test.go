package launcher

// Окно ошибки запуска (#1067).
//
// До этой правки нативный путь показывал ошибку через alert(): текст в нём не
// выделяется — его нельзя ни скопировать разработчику, ни дочитать, когда
// причина длиной в абзац. Тест смотрит на то же, что и пользователь: разметку
// страницы, отданной лаунчером.

import (
	"strings"
	"testing"
)

func startErrorPage(t *testing.T) string {
	t.Helper()
	vm := &baseVM{Base: &Base{
		ID: "b1", Name: "Торговля", ConfigSource: "file",
		Path: `C:\proj`, DBType: "sqlite", DBPath: `C:\onebase\trade.db`, Port: 8080,
	}}
	return renderIndex(t, []*baseVM{vm}, vm)
}

func TestIndex_StartErrorDialogIsCopyable(t *testing.T) {
	html := startErrorPage(t)
	for _, want := range []string{
		`id="start-error-modal"`,      // диалог, а не alert
		`id="start-error-text"`,       // текст ошибки отдельным узлом
		`class="err-pre"`,             // выделяемый прокручиваемый <pre>
		`id="start-error-copy"`,       // кнопка «Скопировать текст ошибки»
		"function copyStartError()",   // и её обработчик
		"navigator.clipboard",         // штатный путь копирования
		"function fallbackCopy(text)", // и запасной, если Clipboard API нет
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в странице нет %q — ошибку запуска снова нельзя скопировать", want)
		}
	}
}

// Ровно тот дефект, с которого началась заявка: нативный путь звал alert.
func TestIndex_StartPathsDoNotAlert(t *testing.T) {
	html := startErrorPage(t)
	if strings.Contains(html, "alert('Ошибка запуска") {
		t.Error("ошибка запуска снова показывается через alert — текст в нём не выделяется")
	}
	for _, want := range []string{
		"showStartErrorModal(d.error, d.fix, id)", // нативное окно
		"showStartError(win, d.error",             // окно браузера — и попап, и диалог
	} {
		if !strings.Contains(html, want) {
			t.Errorf("путь запуска не показывает диалог с ошибкой: нет %q", want)
		}
	}
}

// Кнопка «исправить» рисуется только когда сервер прислал fix, но её разметка
// и обработчик обязаны быть на странице заранее.
func TestIndex_StartErrorFixButtonPresent(t *testing.T) {
	html := startErrorPage(t)
	for _, want := range []string{
		`id="start-error-fix-btn"`,
		`id="start-error-skipped"`,
		`id="start-error-skipped-list"`,
		`id="start-error-continue-btn"`,
		"function runStartFix()",
		"function continueStartAfterRenumber()",
		"'/bases/' + id + '/renumber?write=1'",
		"var _onebaseStartFixBegin = true;",
		"var _onebaseStartFixEnd = true;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в странице нет %q — предложенное лечение нажать нечем", want)
		}
	}
}
