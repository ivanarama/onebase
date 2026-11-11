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
	{ID: "sysbtn", Selector: ".sys-btn", Name: "Кнопка шапки (Система, Вкладки)", Props: []themeProp{
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "tabs", Selector: ".subsys-bar a", Name: "Вкладки разделов", Props: []themeProp{
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "nav", Selector: "#ob-nav", Name: "Боковая навигация (фон)", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
	}},
	{ID: "navlink", Selector: "#ob-nav a", Name: "Ссылка навигации", Props: []themeProp{
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
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
	{ID: "sec", Selector: ".sec", Name: "Заголовок секции", Props: []themeProp{
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
		{"font-weight", "Начертание", "weight"},
	}},
	{ID: "label", Selector: ".form-group label", Name: "Подпись поля", Props: []themeProp{
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "input", Selector: ".form-group input, .form-group select", Name: "Поле ввода / выбор", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"border-color", "Цвет границы", "color"},
		{"font-size", "Размер текста (px)", "size"},
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
	{ID: "breadcrumb", Selector: ".breadcrumb", Name: "Хлебные ссылки", Props: []themeProp{
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "table", Selector: ".tbl", Name: "Таблица (целиком)", Props: []themeProp{
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "thead", Selector: ".tbl th", Name: "Заголовок таблицы", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "tbody", Selector: ".tbl td", Name: "Строка таблицы", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "rowtop", Selector: ".row-top", Name: "Верх строк списка", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
	}},
	{ID: "rowlink", Selector: ".ob-row-link", Name: "Ссылка строки списка", Props: []themeProp{
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "search", Selector: ".search-results", Name: "Панель поиска", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"border-color", "Цвет границы", "color"},
	}},
	{ID: "searchhit", Selector: ".search-hit", Name: "Строка поиска", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
	}},
	{ID: "empty", Selector: ".empty", Name: "Пустой список", Props: []themeProp{
		{"color", "Цвет текста", "color"},
		{"font-size", "Размер текста (px)", "size"},
	}},
	{ID: "error", Selector: ".error", Name: "Блок ошибки", Props: []themeProp{
		{"background", "Цвет фона", "color"},
		{"color", "Цвет текста", "color"},
		{"border-color", "Цвет границы", "color"},
	}},
}

// themeElementDefaults — стандартные стили элементов (значения светлой темы
// встроенного стиля, internal/ui/templates.go). «По умолчанию» заполняет файл
// ими: правки идут от стандартных значений, а не от пустых полей.
var themeElementDefaults = map[string]map[string]string{
	"body":        {"font-family": "system-ui", "font-size": "16px", "color": "#1e293b", "background": "#f5f5f5"},
	"topbar":      {"background": "#1e293b", "color": "#ffffff"},
	"sysbtn":      {"color": "#cbd5e1", "font-size": "15px"},
	"tabs":        {"color": "#94a3b8", "font-size": "13px"},
	"nav":         {"background": "#1e293b", "color": "#cbd5e1"},
	"navlink":     {"color": "#cbd5e1", "font-size": "14px"},
	"card":        {"background": "#ffffff", "color": "#1e293b", "border-color": "#e2e8f0", "border-radius": "10px", "padding": "24px"},
	"heading":     {"font-size": "22px", "font-weight": "bold", "color": "#1e293b"},
	"sec":         {"color": "#94a3b8", "font-size": "11px", "font-weight": "normal"},
	"label":       {"color": "#475569", "font-size": "13px"},
	"input":       {"background": "#ffffff", "color": "#1e293b", "border-color": "#475569", "font-size": "14px"},
	"btn":         {"background": "#e2e8f0", "color": "#374151", "border-color": "#cbd5e1", "font-size": "14px", "border-radius": "7px"},
	"btn-primary": {"background": "#3b82f6", "color": "#ffffff", "border-radius": "7px"},
	"breadcrumb":  {"color": "#64748b", "font-size": "13px"},
	"table":       {"color": "#1e293b", "font-size": "14px"},
	"thead":       {"background": "#eef2f7", "color": "#334155", "font-size": "12px"},
	"tbody":       {"background": "#ffffff", "color": "#334155", "font-size": "13px"},
	"rowtop":      {"background": "#ffffff", "color": "#1e293b"},
	"rowlink":     {"color": "#3b82f6", "font-size": "14px"},
	"search":      {"background": "#ffffff", "color": "#1e293b", "border-color": "#e2e8f0"},
	"searchhit":   {"background": "#ffffff", "color": "#1e293b"},
	"empty":       {"color": "#94a3b8", "font-size": "15px"},
	"error":       {"background": "#fef2f2", "color": "#dc2626", "border-color": "#fecaca"},
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

// themeCSSElement — элемент каталога по ключу формы; неизвестный ключ
// возвращается к первому: форма всегда показывает хоть что-то.
func themeCSSElement(sel string) *themeElement {
	for i, el := range themeElementCatalog {
		if el.ID == sel {
			return &themeElementCatalog[i]
		}
	}
	return &themeElementCatalog[0]
}

// themeCSSPage — форма, данные которой собраны из текущих настроек: сохранение
// и сброс элемента рендерят форму на месте, без перезагрузки страницы.
func (s *Server) themeCSSPage(w http.ResponseWriter, r *http.Request, st themeCSSSettings, sel, msg, errMsg string) {
	cur := themeCSSElement(sel)
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
		"Msg":      msg,
		"Err":      errMsg,
	})
}

// adminThemeCSS — форма настройки внешних стилей. Выбранное поле списка
// элементов подставляет свои свойства; сохранение пишет JSON настроек и
// генерирует ui.css, сброс удаляет оба файла.
func (s *Server) adminThemeCSS(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	s.themeCSSPage(w, r, s.loadThemeSettings(), r.URL.Query().Get("el"),
		r.URL.Query().Get("msg"), r.URL.Query().Get("err"))
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
		s.themeCSSPage(w, r, s.loadThemeSettings(), r.FormValue("el"), "", s.tr(lang, "расположение конфигурации неизвестно — стили писать некуда"))
		return
	}
	if err := r.ParseForm(); err != nil {
		s.themeCSSPage(w, r, s.loadThemeSettings(), r.FormValue("el"), "", s.tr(lang, "не удалось прочитать форму")+": "+s.errText(r, err))
		return
	}
	st := s.loadThemeSettings()
	sel := r.FormValue("el")
	raw := r.FormValue("raw")
	if len(raw) > themeCSSMaxBytes {
		s.themeCSSPage(w, r, st, sel, "", fmt.Sprintf("%s: %d %s, %s %d %s", s.tr(lang, "свободный CSS"), len(raw), s.tr(lang, "байт"), s.tr(lang, "можно не больше"), themeCSSMaxBytes, s.tr(lang, "байт")))
		return
	}
	st.Raw = raw

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
		if len(vals) == 0 {
			delete(st.Elements, el.ID)
		} else {
			st.Elements[el.ID] = vals
		}
		break
	}
	s.themeCSSApply(w, r, st, sel, s.tr(lang, "применено — страницы перезагрузите, пересборка движка не нужна"))
}

// adminThemeCSSElementReset — сброс редактируемого элемента: его свойства
// снимаются, действует встроенный стиль; свободный CSS и остальные элементы
// не трогаются.
func (s *Server) adminThemeCSSElementReset(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	lang := s.resolveLang(r)
	if s.cfg.ConfigLocation == "" {
		s.themeCSSPage(w, r, s.loadThemeSettings(), r.FormValue("el"), "", s.tr(lang, "расположение конфигурации неизвестно — стили писать некуда"))
		return
	}
	st := s.loadThemeSettings()
	sel := themeCSSElement(r.FormValue("el")).ID
	delete(st.Elements, sel)
	s.themeCSSApply(w, r, st, sel, s.tr(lang, "свойства элемента сброшены — действует встроенный стиль"))
}

// themeCSSApply — запись настроек и генерируемого ui.css, форма остаётся на
// месте: сообщение показывается тем же рендером.
func (s *Server) themeCSSApply(w http.ResponseWriter, r *http.Request, st themeCSSSettings, sel, msg string) {
	lang := s.resolveLang(r)
	out, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		s.themeCSSPage(w, r, st, sel, "", s.tr(lang, "не удалось собрать настройки стилей"))
		return
	}
	if err := os.WriteFile(themeCSSSettingsPath(s.cfg.ConfigLocation), out, 0o644); err != nil {
		s.themeCSSPage(w, r, st, sel, "", s.tr(lang, "не удалось записать настройки стилей")+": "+s.errText(r, err))
		return
	}
	css := renderThemeCSS(st)
	if len(css) > themeCSSMaxBytes {
		s.themeCSSPage(w, r, st, sel, "", s.tr(lang, "сгенерированный файл стилей слишком большой"))
		return
	}
	if err := os.WriteFile(s.themeCSSPath(), []byte(css), 0o644); err != nil {
		s.themeCSSPage(w, r, st, sel, "", s.tr(lang, "не удалось записать файл стилей")+": "+s.errText(r, err))
		return
	}
	s.themeCSSPage(w, r, st, sel, msg, "")
}

// adminThemeCSSDefault — «По умолчанию»: заполняет настройки стандартными
// стилями встроенной светлой темы — файл генерируется из них, поля формы
// показывают стандартные значения. Свободный CSS не трогается.
func (s *Server) adminThemeCSSDefault(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	lang := s.resolveLang(r)
	if s.cfg.ConfigLocation == "" {
		s.themeCSSPage(w, r, s.loadThemeSettings(), r.FormValue("el"), "", s.tr(lang, "расположение конфигурации неизвестно — стили писать некуда"))
		return
	}
	st := s.loadThemeSettings()
	st.Elements = map[string]map[string]string{}
	for id, vals := range themeElementDefaults {
		cp := make(map[string]string, len(vals))
		for k, v := range vals {
			cp[k] = v
		}
		st.Elements[id] = cp
	}
	s.themeCSSApply(w, r, st, r.FormValue("el"), s.tr(lang, "файл заполнен стандартными стилями"))
}

func (s *Server) adminThemeCSSReset(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	lang := s.resolveLang(r)
	if !s.hasThemeCSS() {
		s.themeCSSPage(w, r, s.loadThemeSettings(), r.FormValue("el"), "", s.tr(lang, "файла стилей нет"))
		return
	}
	if err := os.Remove(s.themeCSSPath()); err != nil {
		s.themeCSSPage(w, r, s.loadThemeSettings(), r.FormValue("el"), "", s.tr(lang, "не удалось удалить файл стилей")+": "+s.errText(r, err))
		return
	}
	_ = os.Remove(themeCSSSettingsPath(s.cfg.ConfigLocation))
	s.themeCSSPage(w, r, themeCSSSettings{Elements: map[string]map[string]string{}}, r.FormValue("el"),
		s.tr(lang, "файл стилей удалён — страницы снова на встроенных стилях"), "")
}
