package ui

import (
	"net/http"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/csssafe"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/widget"
)

// sidePanelDefaultWidth — ширина правой панели, если в home_page.yaml не задана.
const sidePanelDefaultWidth = 320

// sidePanelFor — правая панель оболочки для пользователя запроса: настройка
// side_panel глобальной home_page.yaml и её виджет, если пользователю она
// положена по ролям и виджет существует. nil — панели нет.
func (s *Server) sidePanelFor(r *http.Request) (*metadata.HomeSidePanel, *metadata.Widget) {
	hp := s.reg.HomePage()
	if hp == nil || hp.SidePanel == nil || strings.TrimSpace(hp.SidePanel.Widget) == "" {
		return nil, nil
	}
	sp := hp.SidePanel
	if !sidePanelRolesMatch(sp, auth.UserFromContext(r.Context())) {
		return nil, nil
	}
	w := s.reg.GetWidget(strings.TrimSpace(sp.Widget))
	if w == nil || !widget.VisibleFor(w, auth.UserFromContext(r.Context())) {
		return nil, nil
	}
	return sp, w
}

// sidePanelRolesMatch: ни одной роли из except_roles и (если roles задан) хотя
// бы одна роль из roles. Без пользователя — видна, если roles не задан.
func sidePanelRolesMatch(sp *metadata.HomeSidePanel, user *auth.User) bool {
	has := func(name string) bool {
		if user == nil {
			return false
		}
		for _, r := range user.Roles {
			if r != nil && strings.EqualFold(strings.TrimSpace(r.Name), strings.TrimSpace(name)) {
				return true
			}
		}
		return false
	}
	for _, name := range sp.ExceptRoles {
		if has(name) {
			return false
		}
	}
	if len(sp.Roles) == 0 {
		return true
	}
	for _, name := range sp.Roles {
		if has(name) {
			return true
		}
	}
	return false
}

// sidePanelData — карточка виджета панели тем же путём, что карточка главной
// (Runner + decorateRefreshResult: обновление по refresh_on, кнопка ↻).
func (s *Server) sidePanelData(r *http.Request) map[string]any {
	sp, wMeta := s.sidePanelFor(r)
	if sp == nil {
		return nil
	}
	runner := widget.New(s.reg, s.store)
	if u := auth.UserFromContext(r.Context()); u != nil {
		runner.CurrentUser = u.Login
		runner.User = u
	}
	runner.Cache = s.widgetCache
	res := runner.RunWithOptions(r.Context(), wMeta, widget.RunOptions{Params: lenientWidgetFilterParams(r, wMeta)})
	if res.AccessDenied {
		return nil
	}
	res.Title = wMeta.DisplayTitle(s.resolveLang(r))
	s.decorateRefreshResult(r, wMeta, &res, runner)
	width := sp.Width
	if width <= 0 {
		width = sidePanelDefaultWidth
	}
	return map[string]any{"Card": res, "Width": width, "Background": csssafe.Color(sp.Background)}
}

// hiddenOnHomeBySidePanel — проверка «этот виджет главной пользователю не
// показывать»: кому видна правая панель, тому side_panel.hide_on_home прячет
// сам виджет панели (true) или перечисленные виджеты. nil — ничего не прячем.
func (s *Server) hiddenOnHomeBySidePanel(r *http.Request) func(name string) bool {
	sp, w := s.sidePanelFor(r)
	if sp == nil || (!sp.HideOnHome.Panel && len(sp.HideOnHome.Widgets) == 0) {
		return nil
	}
	return func(name string) bool { return sp.HideOnHome.Hides(name, w.Name) }
}
