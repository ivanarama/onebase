package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestStaticUIJS(t *testing.T) {
	r := chi.NewRouter()
	mountStatic(r)

	req := httptest.NewRequest(http.MethodGet, "/static/ui.js", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("/static/ui.js status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/javascript") {
		t.Fatalf("/static/ui.js content-type = %q", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"window.obOpenInShell",
		"window.obCloseInShell",
		"openRefPicker",
		"function obImageUpload",
		"function addTpRow",
		"function obInitFormDelegates",
		"data-ob-popup-cancel",
		"data-ob-add-tp-row",
		"data-ob-image-upload",
		"data-ob-ref-current",
		"function openItemPicker",
		"function obInitRichText",
		"function listMenuItems",
		"function obInitListDelegates",
		"data-ob-list-actions",
		"data-ob-list-row",
		"data-ob-auto-submit",
		"data-ob-nav-toggle",
		"data-ob-toggle-target",
		"function obInitFeed",
		"function toggleTreeNode",
		"obInitMappedCharts",
		"window.rsBeforeSubmit",
		"function obInitReportDelegates",
		"function obInitJournalDelegates",
		"function jlCollect",
		"function listRowClick",
		"function listRowDblClick",
		"data-ob-rs-before-submit",
		"data-ob-report-variant-submit",
		"data-ob-attachments",
		"data-ob-select-on-click",
		"window.onebaseDevice",
		"new EventSource('/ui/events')",
		"window.requestAnimationFrame ||",
		"document.createEvent('CustomEvent')",
		"msg.name === 'уведомление'",
		"msg.name === 'notify'",
		// Browser sync dev-режима: страница сама перечитывается после
		// перезагрузки конфигурации и после перезапуска пересобранного сервера.
		"msg.system === 'dev-generation'",
		"msg.system === 'dev-reload'",
		"data-ob-dev",
		"onebase:звонок.входящий",
		// CSS плавающих виджетов инжектирует сам ui.js: их разметку строит он же,
		// а страницы с собственным <head> (админские «Система» → …) без этого
		// показывали голую разметку ИИ-чата и панели сообщений внизу страницы.
		"ob-widget-style",
		"#ob-ai-panel{position:fixed",
		"#ob-msg-bar{position:fixed",
		// Ручка изменения ширины панели ИИ и карточки отложенных действий
		// (план 51: создание черновиков с подтверждением в чате).
		"#ob-ai-rs{position:absolute",
		"localStorage.getItem('obAiW')",
		"'/ui/ai/action'",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/static/ui.js не содержит %q", want)
		}
	}
}

func TestStaticManagedJS(t *testing.T) {
	r := chi.NewRouter()
	mountStatic(r)

	req := httptest.NewRequest(http.MethodGet, "/static/managed.js", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("/static/managed.js status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/javascript") {
		t.Fatalf("/static/managed.js content-type = %q", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"function obManagedConfig",
		"window.obFire",
		"function addVtRow",
		"window.obGridSync",
		"function gridCellEventParams",
		"function obManagedInitDelegates",
		"function obManagedNormalizeHotkey",
		"keydown",
		"window.obResolveActionableFormHotkey",
		"data-ob-fire-click",
		"data-ob-add-tp",
		"obManagedSwitchTab",
		"ПриОткрытии",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/static/managed.js не содержит %q", want)
		}
	}
	if strings.Contains(body, "{{") {
		t.Error("/static/managed.js содержит Go-template маркеры")
	}
}

// TestToggleNextSingleHandler фиксирует инвариант issue #309: делегат
// data-ob-toggle-next (dropdown «Печать ▾» / «Ввести на основании») должен
// висеть на document ровно один раз. managed-форма грузит и ui.js (из шаблона
// "head"), и managed.js — если бы оба вешали этот обработчик, один клик
// переключал бы display дважды (none→block→none) и dropdown не открывался бы.
// Владелец — ui.js (грузится на всех страницах); managed.js не должен дублировать.
func TestToggleNextSingleHandler(t *testing.T) {
	if !strings.Contains(string(uiJS), "[data-ob-toggle-next]") {
		t.Fatal("ui.js должен обрабатывать data-ob-toggle-next (единственный владелец делегата)")
	}
	if strings.Contains(string(managedJS), "[data-ob-toggle-next]") {
		t.Fatal("managed.js не должен дублировать делегат data-ob-toggle-next: " +
			"managed-форма грузит и ui.js, двойной обработчик ломает dropdown (issue #309)")
	}
}

// TestTabShellSingleSSEAndEventForwarding фиксирует инвариант issue #322/#323: во вкладочной
// оболочке /ui/app каждая вкладка — это <iframe>, который грузит полный ui.js.
// Если каждый фрейм открывает свой EventSource('/ui/events') и поллит
// /ui/messages, то N вкладок дают N+1 постоянных соединений: браузер упирается
// в лимит ~6 соединений на хост, переключение вкладок «зависает» (а тосты
// дублируются — Hub.Publish доставляет каждому подписчику). Поэтому оба
// постоянных канала во фрейме (window.__obEmbedded) должны быть отключены —
// единственное соединение держит верхнее окно оболочки, которое тоже грузит ui.js.
// При этом документированный контракт onebase:<имя> сохраняется: оболочка
// ретранслирует событие во фреймы, а те проверяют source и origin отправителя.
func TestTabShellSingleSSEAndEventForwarding(t *testing.T) {
	// go:embed вкомпилирует worktree-содержимое: в клоне без свежего чекаута
	// (до .gitattributes eol=lf) ui.js может лежать с CRLF — нормализуем, чтобы
	// проверки точных "\n"-строк не зависели от EOL чекаута.
	src := strings.ReplaceAll(string(uiJS), "\r\n", "\n")
	// Глобальный ИИ-помощник принадлежит верхнему окну оболочки. Иначе каждая
	// активная вкладка рисует поверх его кнопки собственного робота.
	aiStart := strings.Index(src, "if (window.__obAiInit) return;")
	aiBuild := strings.Index(src, "function buildUI()")
	if aiStart < 0 || aiBuild <= aiStart ||
		!strings.Contains(src[aiStart:aiBuild], "if (window.__obEmbedded) return;") {
		t.Error("ui.js должен строить ИИ-помощника только в верхнем окне вкладочной оболочки")
	}
	// SSE /ui/events подключается только в верхнем окне, не во фрейме оболочки.
	if !strings.Contains(src, "if (!window.__obEmbedded) {") {
		t.Error("ui.js должен подключать SSE /ui/events только вне вкладочного фрейма (гейт window.__obEmbedded)")
	}
	if got := strings.Count(src, "new EventSource('/ui/events')"); got != 1 {
		t.Errorf("ui.js должен содержать ровно один конструктор SSE /ui/events, найдено %d", got)
	}
	// Один поток в оболочке не должен ломать публичный JS-мост произвольных
	// событий: top пересылает сообщение всем вкладкам, а iframe принимает его
	// только от своей same-origin оболочки и поднимает локальный CustomEvent.
	for _, want := range []string{
		"document.querySelectorAll('.ob-tabbody iframe')",
		"frames[i].contentWindow.postMessage",
		"source: 'obRealtime'",
		"ev.source !== window.parent",
		"ev.origin !== window.location.origin",
		"msg.source !== 'obRealtime'",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("ui.js: отсутствует часть безопасной ретрансляции realtime-событий %q", want)
		}
	}
	if got := strings.Count(src, "emitOnebaseEvent('onebase:' + msg.name, msg.data)"); got != 2 {
		t.Errorf("ui.js должен поднимать onebase-событие в оболочке и во фрейме, найдено вызовов: %d", got)
	}
	if !strings.Contains(src, "if (!window.__obEmbedded) {\n    window.addEventListener('onebase:звонок.входящий'") {
		t.Error("ui.js: встроенный тост входящего звонка должен оставаться только в оболочке")
	}
	// Верхнее окно выставляет хук, которым фрейм после submit просит обновить
	// панель сообщений — иначе сообщение появлялось бы только к следующему поллу.
	if !strings.Contains(src, "window.obReloadMessages = load") {
		t.Error("ui.js: верхнее окно должно выставлять window.obReloadMessages для фреймов оболочки")
	}
	if !strings.Contains(src, "window.top.obReloadMessages") {
		t.Error("ui.js: embedded-фрейм после submit должен просить верхнее окно обновить сообщения")
	}
}

// TestModalsCloseOnlyExplicitly фиксирует поведение из отзыва пользователя:
// модальное окно не должно закрываться кликом мимо него. Браузер шлёт click
// общему предку mousedown и mouseup, поэтому «размашистое» выделение текста в
// поле ввода, где кнопку мыши отпустили уже за границей окна, приходило как
// клик по фону: окно закрывалось и уносило введённые данные (воспроизведено в
// браузере — подбор терял проставленные количества). Закрывать модальное окно
// должны только явные действия: «Отмена», «×», Esc.
func TestModalsCloseOnlyExplicitly(t *testing.T) {
	for name, src := range map[string]string{
		"ui.js":         string(uiJS),
		"managed.js":    string(managedJS),
		"консоль строк": tplQueryConsole, // модальный пикер dev-инструментов
	} {
		for _, bad := range []string{
			"e.target === modal",
			"e.target === overlay",
			"event.target===this",
		} {
			if strings.Contains(src, bad) {
				t.Errorf("%s: вернулось закрытие модального окна по клику мимо него (%q) — "+
					"выделение текста мышью с выходом за границу окна теряет введённые данные", name, bad)
			}
		}
	}
	// Быстрый выход остаётся: Esc закрывает подбор и выбор ссылки. В managed-форме
	// это делает managed.js (в фазе перехвата), в автогенерируемой — ui.js.
	for _, want := range []string{"window.__obModalEsc", "_item-picker-modal", "_ref-picker-modal"} {
		if !strings.Contains(string(uiJS), want) {
			t.Errorf("ui.js: нет закрытия модальных окон по Esc (%q)", want)
		}
	}
	if !strings.Contains(string(managedJS), "_item-picker-modal") {
		t.Error("managed.js: пропало закрытие модальных окон по Esc")
	}
}

func TestStaticQueryBuilderJS(t *testing.T) {
	r := chi.NewRouter()
	mountStatic(r)

	req := httptest.NewRequest(http.MethodGet, "/static/query-builder.js", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("/static/query-builder.js status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/javascript") {
		t.Fatalf("/static/query-builder.js content-type = %q", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"function qbReadSchema",
		"function qbGenerate",
		"function qbInitDelegates",
		"ob-query-builder-schema",
		"data-ob-qb-action",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/static/query-builder.js не содержит %q", want)
		}
	}
	if strings.Contains(body, "{{") {
		t.Error("/static/query-builder.js содержит Go-template маркеры")
	}
}

// TestStaticJSRevalidates проверяет, что приложенческий JS отдаётся с ETag и
// ревалидацией (а не immutable-кэшем на год по неверсионированному пути): после
// обновления билда клиент не должен залипнуть на старом скрипте.
func TestStaticJSRevalidates(t *testing.T) {
	r := chi.NewRouter()
	mountStatic(r)

	for _, path := range []string{"/static/ui.js", "/static/managed.js", "/static/query-builder.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rr.Code)
		}
		etag := rr.Header().Get("ETag")
		if etag == "" {
			t.Fatalf("%s не отдаёт ETag", path)
		}
		if cc := rr.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
			t.Fatalf("%s: immutable-кэш по неверсионированному пути залипает после обновления, Cache-Control=%q", path, cc)
		}

		// Повторный запрос с If-None-Match должен вернуть 304 без тела.
		req2 := httptest.NewRequest(http.MethodGet, path, nil)
		req2.Header.Set("If-None-Match", etag)
		rr2 := httptest.NewRecorder()
		r.ServeHTTP(rr2, req2)
		if rr2.Code != http.StatusNotModified {
			t.Fatalf("%s с совпавшим ETag: status = %d, ожидался 304", path, rr2.Code)
		}
		if rr2.Body.Len() != 0 {
			t.Fatalf("%s: 304 не должен нести тело (%d байт)", path, rr2.Body.Len())
		}
	}
}
