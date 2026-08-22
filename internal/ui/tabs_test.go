package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Оболочка вкладок (issue #129/#130, фаза 1) должна рендериться, переиспользуя
// head+nav, и содержать полосу вкладок + движок.
func TestAppShell_Render(t *testing.T) {
	data := map[string]any{
		"Cfg":              Config{AppName: "Test"},
		"Lang":             "ru",
		"Subsystems":       []any{},
		"CurrentSubsystem": "",
		"Nav":              []any{},
		"CollapsibleNav":   false,
		"IsAdmin":          false,
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-app-shell", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`id="ob-tabstrip"`,         // полоса вкладок
		`id="ob-tabbody"`,          // область фреймов
		`class="ob-shell-main"`,    // контент вместо <main>
		`window.obOpenTab=openTab`, // движок вкладок
		`'obTabs'`,                 // ключ sessionStorage для restore
		`source==='obOpenTab'`,     // приём запросов из iframe
		`source==='obCloseTab'`,    // закрытие вкладки по крестику внутри формы
		`<header class="topbar">`,  // переиспользован nav (хром оболочки)
		`ob-tab-dup`,               // кнопка «новый экземпляр» (#130)
		`{allowDup:true}`,          // дубликат = новый экземпляр
		`source==='obDirty'`,       // фаза 3: приём флага несохранённых правок
		`tabByWindow`,              // маршрутизация по окну-источнику
		`beforeunload`,             // предупреждение при уходе со страницы
		`ob-tabmenu`,               // фаза 4: контекст-меню вкладки
		`Закрыть другие`,           // пункт контекст-меню
		`scrollIntoView`,           // автоскролл активной вкладки
		`shellHomeURL`,             // переход по подсистемам строит URL через searchParams
		`#ob-tabhome a[href]`,      // ссылки рабочего стола открываются через shell
		// F5 в режиме вкладок не должен сбрасывать на первую: активная вкладка
		// запоминается отдельным ключом и восстанавливается, если ещё жива.
		`'obTabsActive'`,   // ключ sessionStorage с URL активной вкладки
		`restore||tabs[0]`, // fallback на первую, когда сохранённая закрыта/нет
	} {
		if !strings.Contains(html, want) {
			t.Errorf("оболочка вкладок не содержит %q", want)
		}
	}
}

func TestAppShell_RendersSubsystemDashboard(t *testing.T) {
	s := newServerForFormMode(t)
	s.reg.LoadWidgets([]*metadata.Widget{{
		Name:  "SalesActions",
		Type:  metadata.WidgetTypeActions,
		Title: "Действия продаж",
		Items: []metadata.WidgetAction{{Label: "Создать продажу", URL: "/ui/document/sale/new"}},
	}})
	s.reg.LoadSubsystems([]*metadata.Subsystem{{
		Name:  "Sales",
		Title: "Sales",
		HomePage: &metadata.HomePage{
			Title:  "Рабочий стол продаж",
			Layout: "rows",
			Rows:   []metadata.HomePageRow{{Widgets: []string{"SalesActions"}}},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/app?home=1&subsystem=Sales", nil)
	s.appShell(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получено %d: %s", rec.Code, rec.Body.String())
	}
	html := rec.Body.String()
	for _, want := range []string{
		`id="ob-tabhome"`,
		`Рабочий стол продаж`,
		`Действия продаж`,
		`Создать продажу`,
		`id="ob-widget-charts"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("оболочка вкладок не содержит рабочий стол подсистемы: %q", want)
		}
	}
}

// В партиале head есть детект встраивания и правило скрытия хрома (фаза 1):
// любая страница во фрейме оболочки прячет топбар/подсистемы.
func TestHead_EmbeddedChromeHidden(t *testing.T) {
	data := map[string]any{"Cfg": Config{AppName: "Test"}, "Lang": "ru"}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "head", data); err != nil {
		t.Fatalf("ExecuteTemplate head: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`src="/static/ui.js"`,
		`ob-embedded`,
		`.ob-embedded .topbar,.ob-embedded .subsys-bar,.ob-embedded #ob-nav{display:none`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("head не содержит embedded-подключение %q", want)
		}
	}
	js := string(uiJS)
	for _, want := range []string{
		`window.__obEmbedded = window.self !== window.top`,
		`obOpenableForm`,                           // фаза 2: перехват открытия форм во вкладку
		`window.obOpenInShell`,                     // общий helper для ссылок и JS-открытий списков
		`window.obCloseInShell`,                    // крестик формы закрывает вкладку оболочки
		`source: 'obOpenTab'`,                      // постит запрос родителю-оболочке
		`source: 'obCloseTab'`,                     // просит оболочку закрыть текущую вкладку
		`window.parent && window.parent.obOpenTab`, // guard: только если родитель — оболочка
		`source: 'obDirty'`,                        // фаза 3: трекер несохранённых правок
	} {
		if !strings.Contains(js, want) {
			t.Errorf("ui.js не содержит embedded-логику %q", want)
		}
	}
}

func TestTabs_OpenableAllowsRefOpen(t *testing.T) {
	data := map[string]any{
		"Cfg":              Config{AppName: "Test"},
		"Lang":             "ru",
		"Subsystems":       []any{},
		"CurrentSubsystem": "",
		"Nav":              []any{},
		"CollapsibleNav":   false,
		"IsAdmin":          false,
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-app-shell", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	want := `(admin|about|logout|login|logo|debug|app|_(?!ref-open|ref-create))`
	if !strings.Contains(html, want) {
		t.Errorf("оболочка вкладок не содержит исключение для _ref-open/_ref-create: %q", want)
	}
	js := string(uiJS)
	if !strings.Contains(js, want) {
		t.Errorf("ui.js не содержит исключение для _ref-open/_ref-create: %q", want)
	}
}
