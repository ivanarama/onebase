package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// РМК показывается только там, где приложение его объявило (issue #1331).
//
// Раньше блок «Платформенные возможности» со ссылкой на кассу рендерился
// каждому не-администратору в ЛЮБОМ приложении: у казначейства и склада
// пользователь видел кассовый интерфейс, которого в его домене никогда не было.
// Соседний пункт того же блока («Этапы») был закрыт признаком конфигурации,
// а РМК — нет.
//
// Проверяем через сервер: рендер страницы и прямой запрос `/ui/pos` идут теми
// же обработчиками, что у пользователя, а не сборкой шаблона с готовой картой
// данных — иначе тест доказывал бы только вёрстку, но не то, что признак
// доезжает из конфигурации.

func posServer(t *testing.T, enabled bool) *Server {
	t.Helper()
	s := newServerForFormMode(t)
	s.cfg.POSEnabled = enabled
	s.tmpl = tmpl
	return s
}

// «Все функции» — путь администратора, и он же показывает, доехал ли признак
// из конфигурации до шаблона: обработчик собирает данные сам.
func TestPOS_AllFunctionsHidesPOSWithoutFeature(t *testing.T) {
	s := posServer(t, false)
	rec := httptest.NewRecorder()
	s.allFunctions(rec, httptest.NewRequest(http.MethodGet, "/ui/all-functions", nil))
	html := rec.Body.String()
	if strings.Contains(html, `href="/ui/pos"`) {
		t.Error("ссылка на РМК показана приложению, которое его не объявляло")
	}
	if strings.Contains(html, "Платформенные возможности") {
		t.Error("пустая группа «Платформенные возможности» осталась на странице")
	}
}

func TestPOS_AllFunctionsShowsPOSWithFeature(t *testing.T) {
	s := posServer(t, true)
	rec := httptest.NewRecorder()
	s.allFunctions(rec, httptest.NewRequest(http.MethodGet, "/ui/all-functions", nil))
	html := rec.Body.String()
	for _, want := range []string{"Платформенные возможности", `href="/ui/pos"`} {
		if !strings.Contains(html, want) {
			t.Errorf("приложение объявило features.pos, но на странице нет %q", want)
		}
	}
}

// Меню обычного пользователя — тот случай, из-за которого заявка и заведена.
func TestPOS_UserMenuFollowsFeature(t *testing.T) {
	if nav := renderNav(t, false, map[string]any{"HasPOS": false}); strings.Contains(nav, `href="/ui/pos"`) {
		t.Error("обычный пользователь видит РМК в приложении, где его нет")
	}
	nav := renderNav(t, false, map[string]any{"HasPOS": true})
	if !strings.Contains(nav, `href="/ui/pos"`) || !strings.Contains(nav, "Платформенные возможности") {
		t.Error("в торговом приложении обычный пользователь потерял РМК")
	}
}

func TestPOS_DirectURLNotFoundWithoutFeature(t *testing.T) {
	// Спрятанный пункт меню, доступный по прямой ссылке, — это не «в этом
	// приложении РМК нет», а «мы просто не показали кнопку».
	s := posServer(t, false)
	rec := httptest.NewRecorder()
	s.posPage(rec, httptest.NewRequest(http.MethodGet, "/ui/pos", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("прямой /ui/pos вернул %d, ожидался 404", rec.Code)
	}
}

func TestPOS_DirectURLServedWithFeature(t *testing.T) {
	s := posServer(t, true)
	rec := httptest.NewRecorder()
	s.posPage(rec, httptest.NewRequest(http.MethodGet, "/ui/pos", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("прямой /ui/pos вернул %d, ожидался 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Рабочее место кассира") {
		t.Error("страница РМК не отрендерилась")
	}
}
