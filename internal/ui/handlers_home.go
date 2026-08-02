package ui

// Домашняя страница (виджеты), логотип и страница «О программе».
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

func (s *Server) about(w http.ResponseWriter, r *http.Request) {
	entities := s.reg.Entities()
	var catalogs, docs int
	for _, e := range entities {
		if e.Kind == "catalog" {
			catalogs++
		} else {
			docs++
		}
	}
	cfg := prepareAboutConfig(s.cfg)
	user := auth.UserFromContext(r.Context())
	s.render(w, r, "page-about", map[string]any{
		"Cfg":       cfg,
		"Catalogs":  catalogs,
		"Documents": docs,
		"Registers": len(s.reg.Registers()),
		"Reports":   len(s.reg.Reports()),
		"User":      user,
	})
}

func prepareAboutConfig(cfg Config) Config {
	if cfg.DatabaseLocation == "" {
		cfg.DatabaseLocation = cfg.DSN
	}
	cfg.DatabaseLocation = maskDSN(cfg.DatabaseLocation)
	// В database-режиме конфигурация находится там же, где данные. Значение
	// собираем здесь после маскирования DSN, чтобы пароль не попал в HTML.
	if cfg.ConfigSource == "database" {
		cfg.ConfigLocation = cfg.DatabaseLocation
	}
	return cfg
}

func maskDSN(dsn string) string {
	if i := strings.Index(dsn, "://"); i >= 0 {
		rest := dsn[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			userPart := rest[:at]
			if colon := strings.LastIndex(userPart, ":"); colon >= 0 {
				return dsn[:i+3+colon+1] + "***" + dsn[i+3+at:]
			}
		}
	}
	if i := strings.Index(dsn, "password="); i >= 0 {
		end := i + len("password=")
		rest := dsn[end:]
		if sp := strings.IndexByte(rest, ' '); sp >= 0 {
			return dsn[:end] + "***" + rest[sp:]
		}
		return dsn[:end] + "***"
	}
	return dsn
}

func (s *Server) logo(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Logo == "" {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(s.cfg.Logo)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(s.cfg.Logo))
	switch ext {
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".jpg", ".jpeg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".gif":
		w.Header().Set("Content-Type", "image/gif")
	case ".webp":
		w.Header().Set("Content-Type", "image/webp")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeBody(w, data)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	tabs := s.store.EffectiveFormOpenMode(r.Context(), currentUserLogin(r)) == storage.FormModeTabs
	// Глобальная «Главная» скрыта конфигурацией (home_page.hidden) → уводим на
	// первый раздел (issue #304). В режиме вкладок цель — оболочка /ui/app,
	// поэтому вычисляем базу до редиректа вкладок, чтобы не делать лишний хоп.
	base := "/ui/"
	if tabs {
		base = "/ui/app"
	}
	if target, ok := s.hiddenHomeRedirect(r, base); ok {
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	// Режим вкладок: входная страница уводит в оболочку /ui/app (issue #129/#130).
	if tabs {
		q := url.Values{"home": []string{"1"}}
		if sub := r.URL.Query().Get("subsystem"); sub != "" {
			q.Set("subsystem", sub)
		}
		target := "/ui/app?" + q.Encode()
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	if !s.requireSubsystemVisible(w, r) {
		return
	}
	s.render(w, r, "page-index", s.homeDashboardData(r))
}

// hideGlobalHome сообщает, прячется ли глобальная «Главная»: флаг
// config/home_page.yaml → hidden, но только когда есть хотя бы одна подсистема
// (иначе навигации не останется — фейл-сейф, флаг игнорируется). Используется
// и панелью разделов (убрать ведущую ссылку), и редиректом входа.
func (s *Server) hideGlobalHome() bool {
	hp := s.reg.HomePage()
	return hp != nil && hp.Hidden && len(s.reg.Subsystems()) > 0
}

// hiddenHomeRedirect решает, нужно ли увести запрос глобальной «Главной» на
// первый раздел. Срабатывает, когда глобальная «Главная» скрыта (hideGlobalHome)
// и в запросе нет ?subsystem= (сам раздел показываем как есть). base — путь
// назначения: "/ui/" для режима страниц, "/ui/app" для вкладок.
func (s *Server) hiddenHomeRedirect(r *http.Request, base string) (string, bool) {
	if r.URL.Query().Get("subsystem") != "" || !s.hideGlobalHome() {
		return "", false
	}
	// Целимся в первый ВИДИМЫЙ пользователю раздел; если после фильтрации по
	// правам не осталось ни одного — фейл-сейф: показываем скрытую «Главную»,
	// иначе пользователь упёрся бы в 403 недоступного раздела.
	subs := s.visibleSubsystems(r)
	if len(subs) == 0 {
		return "", false
	}
	q := url.Values{"subsystem": []string{subs[0].Name}}
	if base == "/ui/app" {
		q.Set("home", "1")
	}
	return base + "?" + q.Encode(), true
}

func (s *Server) homeDashboardData(r *http.Request) map[string]any {
	hp := s.reg.HomePage()
	if sub := r.URL.Query().Get("subsystem"); sub != "" {
		if ss := s.reg.GetSubsystem(sub); ss != nil && ss.HomePage != nil {
			hp = ss.HomePage
		}
	}
	// Соблюдение настроенных рядов (WYSIWYG) включается явно через layout: rows.
	honor := hp != nil && hp.Layout == "rows"
	groups, defaulted := homePageWidgetGroups(hp, s.reg, honor)

	login := ""
	if u := auth.UserFromContext(r.Context()); u != nil {
		login = u.Login
	}
	runner := widget.New(s.reg, s.store)
	runner.CurrentUser = login
	runner.User = auth.UserFromContext(r.Context())
	runner.Cache = s.widgetCache

	lang := s.resolveLang(r)
	// rows — для раскладки по рядам, flat — для инициализации графиков в JS.
	rows := make([][]widget.Result, 0, len(groups))
	var flat []widget.Result
	run := func(wMeta *metadata.Widget) widget.Result {
		if wMeta.Type == "missing" {
			return widget.Result{
				Name:  wMeta.Name,
				Title: wMeta.DisplayTitle(lang),
				Error: s.tr(lang, "виджет не найден:") + " " + wMeta.Name,
			}
		}
		res := runner.Run(r.Context(), wMeta)
		res.Title = wMeta.DisplayTitle(lang)
		return res
	}
	for _, group := range groups {
		rowRes := make([]widget.Result, 0, len(group))
		for _, wMeta := range group {
			res := run(wMeta)
			// Нет прав на источник — карточку не показываем: ошибка «нет
			// доступа» на чужом рабочем столе только пугает пользователя.
			// Настоящие ошибки (compile/SQL) остаются видимыми.
			if res.AccessDenied {
				continue
			}
			rowRes = append(rowRes, res)
			flat = append(flat, res)
		}
		if len(rowRes) == 0 {
			continue // ряд опустел целиком — не оставляем пустую полосу
		}
		rows = append(rows, rowRes)
	}

	title := s.tr(lang, "Главная")
	if hp != nil {
		if t := hp.DisplayTitle(lang); t != "" && t != "Главная" {
			title = t
		}
	}

	return map[string]any{
		"HomeTitle":     title,
		"WidgetRows":    rows,
		"WidgetResults": flat,
		"DefaultedHome": defaulted,
	}
}

// homePageWidgetGroups resolves the dashboard layout into ordered groups of
// widget metadata, one group per rendered row. Behaviour:
//   - With a HomePage and honor=true (layout: rows): one group per configured
//     row (RowGroups), so row boundaries are preserved (WYSIWYG).
//   - With a HomePage otherwise: a single group with every widget in order
//     (auto-flow — the template wraps them by width).
//   - Unknown widget names become a synthetic "widget not found" entry so the
//     dashboard still renders and the user can spot the typo.
//   - Without a HomePage but with registered widgets: one group in load order.
//   - Otherwise: a transient "recent" widget so a fresh install is never blank.
func homePageWidgetGroups(hp *metadata.HomePage, reg *runtime.Registry, honor bool) ([][]*metadata.Widget, bool) {
	resolve := func(names []string) []*metadata.Widget {
		out := make([]*metadata.Widget, 0, len(names))
		for _, n := range names {
			if w := reg.GetWidget(n); w != nil {
				out = append(out, w)
			} else {
				out = append(out, &metadata.Widget{Name: n, Type: "missing", Title: n})
			}
		}
		return out
	}
	if hp != nil {
		if honor {
			if rg := hp.RowGroups(); len(rg) > 0 {
				groups := make([][]*metadata.Widget, 0, len(rg))
				for _, names := range rg {
					groups = append(groups, resolve(names))
				}
				return groups, false
			}
		}
		if names := hp.WidgetNames(); len(names) > 0 {
			return [][]*metadata.Widget{resolve(names)}, false
		}
	}
	if registered := reg.Widgets(); len(registered) > 0 {
		return [][]*metadata.Widget{registered}, true
	}
	def := &metadata.Widget{
		Name:  "_default_recent",
		Type:  metadata.WidgetTypeRecent,
		Title: "Последние документы",
		Limit: 10,
		Scope: "all",
	}
	return [][]*metadata.Widget{{def}}, true
}
