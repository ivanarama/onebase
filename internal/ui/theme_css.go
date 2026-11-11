package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Внешние стили конфигурации (план 218).
//
// Конфигурация кладёт файл «ui.css» в корень проекта: он подключается после
// встроенных стилей и переопределяет их, пересборка движка не нужна. Файл
// читается с диска при каждом запросе, поэтому правка стиля видна сразу.
// Если файла нет — страницы остаются ровно на встроенных стилях (head не
// получает ссылку, запросы к /ui/theme.css не возникают).
//
// Форма «Система» → «Стили интерфейса» правит стили элемента за элементом
// (шрифт, размер, цвета, скругление, поля), а не голый CSS: настройки живут в
// JSON рядом с проектом, а ui.css — сгенерированный из них артефакт.

// themeCSSMaxBytes — потолок файла стилей: правка через форму администратора
// не должна превращать проект в свалку, а 256 КБ заведомо больше любой
// разумной темы интерфейса.
const themeCSSMaxBytes = 256 << 10

type themeProp struct {
	Key   string // CSS-свойство
	Label string // подпись в форме
	Kind  string // color|size|weight|family
}

type themeElement struct {
	ID       string // ключ в настройках
	Selector string // селектор генерируемого CSS
	Name     string // подпись в форме
	Props    []themeProp
}

// themeElementCatalog — элементы enterprise-UI, стиль которых конфигурация
// задаёт через форму: селекторы совпадают с классами базового стиля
// internal/ui/templates.go.
var themeElementCatalog = []themeElement{
	{ID: "body", Selector: "body", Name: "Вся страница (фон, базовый текст)", Props: []themeProp{
		{"font-family", "Шрифт", "family"},
		{"font-size", "Размер текста (px)", "size"},
		{"color", "Цвет текста", "color"},
		{"background", "Цвет фона", "color"},
	}},
	{ID: "topbar", Selector: ".topbar", Name: "Верхняя шапка", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
	}},
	{ID: "nav", Selector: "#ob-nav", Name: "Боковая навигация", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
	}},
	{ID: "card", Selector: ".card", Name: "Карточка (панель формы)", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"border-color", "Цвет границы", "color"},
		{"border-radius", "Скругление (px)", "size"},
		{"padding", "Поля (px)", "size"},
	}},
	{ID: "heading", Selector: "h2", Name: "Заголовок страницы", Props: []themeProp{
		{"font-size", "Размер (px)", "size"},
		{"font-weight", "Начертание", "weight"},
		{"color", "Цвет текста", "color"},
	}},
	{ID: "btn", Selector: ".btn", Name: "Кнопка (обычная)", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"border-color", "Цвет границы", "color"},
		{"font-size", "Размер текста (px)", "size"},
		{"border-radius", "Скругление (px)", "size"},
	}},
	{ID: "btn-primary", Selector: ".btn.btn-primary", Name: "Кнопка (главная)", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"border-radius", "Скругление (px)", "size"},
	}},
	{ID: "input", Selector: ".form-group input, .form-group select", Name: "Поле ввода / выбор", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"border-color", "Цвет границы", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "thead", Selector: "table th", Name: "Заголовок таблицы", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "tbody", Selector: "table td", Name: "Строка таблицы", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
}

// themeFontFamilies — ограниченный список: форма не позволяет вписать в CSS
// произвольную строку со скобами и точкой с запятой.
var themeFontFamilies = []string{"system-ui", "serif", "monospace", "ui-monospace"}

// themeCSSSettings — источник истины формы; ui.css генерируется из неё.
type themeCSSSettings struct {
	Raw      string                       `json:"raw"`
	Elements map[string]map[string]string `json:"elements"`
}

func themeCSSSettingsPath(cfgLocation string) string {
	return filepath.Join(cfgLocation, ".ui_theme.json")
}

func (s *Server) loadThemeSettings() themeCSSSettings {
	st := themeCSSSettings{Elements: map[string]map[string]string{}}
	data, err := os.ReadFile(themeCSSSettingsPath(s.cfg.ConfigLocation))
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return themeCSSSettings{Elements: map[string]map[string]string{}}
	}
	if st.Elements == nil {
		st.Elements = map[string]map[string]string{}
	}
	return st
}

var themeColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{3}$|^#[0-9a-fA-F]{6}$`)
var themeSizeRe = regexp.MustCompile(`^[0-9]{1,4}$`)

// themeSanitizeValue — форма принимает ограниченные значения; произвольный
// текст в генерируемый CSS не попадает.
func themeSanitizeValue(kind, v string) string {
	v = strings.TrimSpace(v)
	switch kind {
	case "color":
		if themeColorRe.MatchString(v) {
			return strings.ToLower(v)
		}
		return ""
	case "size":
		if themeSizeRe.MatchString(v) && v != "0" {
			return v + "px"
		}
		return ""
	case "weight":
		if v == "normal" || v == "bold" {
			return v
		}
		return ""
	case "family":
		for _, f := range themeFontFamilies {
			if v == f {
				return v
			}
		}
		return ""
	}
	return ""
}

// renderThemeCSS — артефакт ui.css: сначала свободный текст формы (сырой CSS
// конфигурации), затем сгенерированные правила по каталогу.
func renderThemeCSS(st themeCSSSettings) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(st.Raw))
	b.WriteString("\n/* стили из формы «Стили интерфейса» */\n")
	for _, el := range themeElementCatalog {
		vals := st.Elements[el.ID]
		var decls []string
		for _, p := range el.Props {
			v := strings.TrimSpace(vals[p.Key])
			if v == "" {
				continue
			}
			decls = append(decls, p.Key+":"+v)
		}
		if len(decls) == 0 {
			continue
		}
		b.WriteString(el.Selector + "{" + strings.Join(decls, ";") + "}\n")
	}
	return b.String()
}

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

// adminThemeCSS — форма настройки внешних стилей. Выбранное поле списка
// элементов подставляет свои свойства; сохранение пишет JSON настроек и
// генерирует ui.css, сброс удаляет оба файла.
func (s *Server) adminThemeCSS(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	sel := r.URL.Query().Get("el")
	if sel == "" {
		sel = "body"
	}
	var cur *themeElement
	for i, el := range themeElementCatalog {
		if el.ID == sel {
			cur = &themeElementCatalog[i]
			break
		}
	}
	if cur == nil {
		cur = &themeElementCatalog[0]
	}
	st := s.loadThemeSettings()
	vals := st.Elements[cur.ID]
	if vals == nil {
		vals = map[string]string{}
	}
	data, _ := os.ReadFile(s.themeCSSPath())
	fi, _ := os.Stat(s.themeCSSPath())
	s.render(w, r, "page-theme-css", map[string]any{
		"Catalog":  themeElementCatalog,
		"Element":  cur,
		"Sel":      cur.ID,
		"Vals":     vals,
		"Families": themeFontFamilies,
		"Raw":      st.Raw,
		"Path":     s.themeCSSPath(),
		"Exists":   s.hasThemeCSS(),
		"Size":     len(data),
		"Edited":   formatThemeCSSEdited(fi),
		"Msg":      r.URL.Query().Get("msg"),
		"Err":      r.URL.Query().Get("err"),
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
	if s.cfg.ConfigLocation == "" {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "расположение конфигурации неизвестно — стили писать некуда"))
		return
	}
	if err := r.ParseForm(); err != nil {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "не удалось прочитать форму")+": "+s.errText(r, err))
		return
	}
	st := s.loadThemeSettings()
	raw := r.FormValue("raw")
	if len(raw) > themeCSSMaxBytes {
		s.themeCSSRedirect(w, r, "", fmt.Sprintf("%s: %d %s, %s %d %s", s.tr(lang, "свободный CSS"), len(raw), s.tr(lang, "байт"), s.tr(lang, "можно не больше"), themeCSSMaxBytes, s.tr(lang, "байт")))
		return
	}
	st.Raw = raw

	sel := r.FormValue("el")
	for _, el := range themeElementCatalog {
		if el.ID != sel {
			continue
		}
		vals := st.Elements[el.ID]
		if vals == nil {
			vals = map[string]string{}
		}
		for _, p := range el.Props {
			v := themeSanitizeValue(p.Kind, r.FormValue("p_"+p.Key))
			if v == "" {
				delete(vals, p.Key)
				continue
			}
			vals[p.Key] = v
		}
		st.Elements[el.ID] = vals
		break
	}

	out, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "не удалось собрать настройки стилей"))
		return
	}
	if err := os.WriteFile(themeCSSSettingsPath(s.cfg.ConfigLocation), out, 0o644); err != nil {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "не удалось записать настройки стилей")+": "+s.errText(r, err))
		return
	}
	css := renderThemeCSS(st)
	if len(css) > themeCSSMaxBytes {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "сгенерированный файл стилей слишком большой"))
		return
	}
	if err := os.WriteFile(s.themeCSSPath(), []byte(css), 0o644); err != nil {
		s.themeCSSRedirect(w, r, "", s.tr(lang, "не удалось записать файл стилей")+": "+s.errText(r, err))
		return
	}
	s.themeCSSRedirect(w, r, s.tr(lang, "стили сохранены — страницы перезагрузите, пересборка движка не нужна"), "")
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
	_ = os.Remove(themeCSSSettingsPath(s.cfg.ConfigLocation))
	s.themeCSSRedirect(w, r, s.tr(lang, "файл стилей удалён — страницы снова на встроенных стилях"), "")
}

func (s *Server) themeCSSRedirect(w http.ResponseWriter, r *http.Request, msg, errMsg string) {
	q := "msg=" + themeCSSQueryEscape(strings.TrimSpace(msg))
	if errMsg != "" {
		q = "err=" + themeCSSQueryEscape(strings.TrimSpace(errMsg))
	}
	http.Redirect(w, r, "/ui/admin/theme-css?"+q, http.StatusSeeOther)
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
