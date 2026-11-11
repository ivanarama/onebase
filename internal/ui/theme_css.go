package ui

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Внешние стили конфигурации (план 218, минимальный контур).
//
// Конфигурация кладёт файл «ui.css» в корень проекта: он подключается после
// встроенных стилей и переопределяет их, пересборка движка не нужна. Файл
// читается с диска при каждом запросе, поэтому правка стиля видна сразу.
// Если файла нет — страницы остаются ровно на встроенных стилях (head не
// получает ссылку, запросы к /ui/theme.css не возникают).

// themeCSSMaxBytes — потолок файла стилей: правка через форму администратора
// не должна превращать проект в свалку, а 256 КБ заведомо больше любой
// разумной темы интерфейса.
const themeCSSMaxBytes = 256 << 10

func (s *Server) themeCSSPath() string {
	return filepath.Join(s.cfg.ConfigLocation, "ui.css")
}

func (s *Server) hasThemeCSS() bool {
	if s.cfg.ConfigLocation == "" {
		return false
	}
	st, err := os.Stat(s.themeCSSPath())
	return err == nil && !st.IsDir()
}

// themeCSS отдаёт файл стилей конфигурации. Отсутствующий файл — пустой
// ответ 200: браузер не показывает ошибку консоли на конфигурациях без тем.
func (s *Server) themeCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	data, err := os.ReadFile(s.themeCSSPath())
	if err != nil {
		w.Write([]byte(""))
		return
	}
	w.Write(data)
}

// adminThemeCSS — форма настройки внешних стилей («Система» → «Стили
// интерфейса»). Файл правится администратором на месте, без пересборки
// движка; сохранение сразу вступает в силу, потому что стили читаются с диска.
func (s *Server) adminThemeCSS(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	data, _ := os.ReadFile(s.themeCSSPath())
	st, _ := os.Stat(s.themeCSSPath())
	s.render(w, r, "page-theme-css", map[string]any{
		"CSS":     string(data),
		"Path":    s.themeCSSPath(),
		"Exists":  s.hasThemeCSS(),
		"Size":    len(data),
		"Edited":  formatThemeCSSEdited(st),
		"Msg":     r.URL.Query().Get("msg"),
		"Err":     r.URL.Query().Get("err"),
		"Preview": r.URL.Query().Get("msg") != "",
	})
}

func formatThemeCSSEdited(st os.FileInfo) string {
	if st == nil {
		return ""
	}
	return st.ModTime().Format("02.01.2006 15:04")
}

func (s *Server) adminThemeCSSSave(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	lang := s.resolveLang(r)
	if err := r.ParseForm(); err != nil {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "не удалось прочитать форму")+": "+s.errText(r, err))
		return
	}
	css := r.FormValue("css")
	if len(css) > themeCSSMaxBytes {
		s.themeCSSRedirect(w, r, "", fmt.Sprintf("%s: %d %s, %d %s", s.tr(lang, "файл стилей"), len(css), s.tr(lang, "байт"), themeCSSMaxBytes, s.tr(lang, "можно не больше")))
		return
	}
	if s.cfg.ConfigLocation == "" {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "расположение конфигурации неизвестно — файл стилей писать некуда"))
		return
	}
	if err := os.WriteFile(s.themeCSSPath(), []byte(css), 0o644); err != nil {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "не удалось записать файл стилей")+": "+s.errText(r, err))
		return
	}
	s.themeCSSRedirect(w, r, s.tr(lang, "стили сохранены — страницы перезагрузите, пересборка не нужна"), "")
}

func (s *Server) adminThemeCSSReset(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	lang := s.resolveLang(r)
	if !s.hasThemeCSS() {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "файла стилей нет"))
		return
	}
	if err := os.Remove(s.themeCSSPath()); err != nil {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "не удалось удалить файл стилей")+": "+s.errText(r, err))
		return
	}
	s.themeCSSRedirect(w, r, s.tr(lang, "файл стилей удалён — страницы снова на встроенных стилях"), "")
}

func (s *Server) themeCSSRedirect(w http.ResponseWriter, r *http.Request, msg, errMsg string) {
	q := "msg=" + strings.TrimSpace(msg)
	if errMsg != "" {
		q = "err=" + strings.TrimSpace(errMsg)
	}
	http.Redirect(w, r, "/ui/admin/theme-css?"+themeCSSQueryEscape(q), http.StatusSeeOther)
}

func themeCSSQueryEscape(s string) string {
	var b strings.Builder
	for _, ch := range s {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		case ch == ' ':
			b.WriteString("%20")
		default:
			b.WriteString(fmt.Sprintf("%%%02X", ch))
		}
	}
	return b.String()
}
