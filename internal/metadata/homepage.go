package metadata

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// HomePageWidget is a single widget reference inside the home page layout.
type HomePageWidget struct {
	Name string `yaml:"name"`
	Span int    `yaml:"span"` // for layout=grid, default 1; chart-row widgets often span 3
}

// HomePageRow groups widgets that should render side-by-side.
type HomePageRow struct {
	Widgets []string `yaml:"widgets"`
}

// HomePage describes the dashboard layout for /ui/.
type HomePage struct {
	Title   string            `yaml:"title"`
	Titles  map[string]string `yaml:"titles"`
	Layout  string            `yaml:"layout"` // grid | rows (default rows)
	Rows    []HomePageRow     `yaml:"rows"`
	Widgets []HomePageWidget  `yaml:"widgets"` // flat list, used when layout=grid
	// Nav опционально ограничивает левое меню глобальной «Главной» так же, как
	// contents подсистемы. nil/пусто = плоский список всех читаемых объектов.
	Nav  *SubsystemContents `yaml:"nav,omitempty"`
	Menu *Menu              `yaml:"menu,omitempty"`
	// NavByRole — левое меню «Главной» по ролям: имя роли → состав (как nav).
	// Пользователь с ролями из этого блока видит объединение составов своих
	// ролей вместо nav; пустой состав роли — пустое меню. Остальные и
	// администратор видят nav. Нужен, когда объект читается ради ссылок и
	// подбора (кладовщику — заявки из ордера), но в меню роли ему не место.
	NavByRole map[string]*SubsystemContents `yaml:"nav_by_role,omitempty"`
	// Hidden прячет глобальную «Главную» целиком: ведущая ссылка в панели
	// разделов не показывается, а вход (/ui/) уводит на первый раздел —
	// навигация идёт только по подсистемам (issue #304). Осмыслен только для
	// глобальной config/home_page.yaml; у подсистем игнорируется. Фейл-сейф:
	// без подсистем прятать нечем, поэтому флаг не срабатывает.
	Hidden bool `yaml:"hidden,omitempty"`
}

// NavForRoles — состав левого меню «Главной» для ролей пользователя:
// объединение составов nav_by_role его ролей (имя роли — без учёта регистра).
// ok=false — ни одна роль в nav_by_role не указана, действует nav.
func (h *HomePage) NavForRoles(roles []string) (contents *SubsystemContents, ok bool) {
	if h == nil || len(h.NavByRole) == 0 {
		return nil, false
	}
	union := &SubsystemContents{}
	for _, role := range roles {
		for name, c := range h.NavByRole {
			if !strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(role)) {
				continue
			}
			ok = true
			union.merge(c)
		}
	}
	if !ok {
		return nil, false
	}
	return union, true
}

// DisplayTitle возвращает заголовок главной страницы с учётом языка.
// Если ни перевода, ни Title нет — возвращает русский «Главная».
func (h *HomePage) DisplayTitle(lang string) string {
	if h == nil {
		return "Главная"
	}
	if lang != "" {
		if v, ok := h.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if h.Title != "" {
		return h.Title
	}
	return "Главная"
}

// applyDefaults fills in zero-value fields with sensible defaults.
//
// Раскладка по умолчанию — "auto": все виджеты идут одним потоком и переносятся
// по ширине. Для flat-виджетов сохраняем "grid" (рендерится так же, как auto).
//
// Исключение: если автор задал несколько рядов (len(Rows) > 1) и не указал
// layout явно, это осознанная раскладка по рядам — ставим "rows", чтобы и
// рабочий стол, и конфигуратор уважали границы рядов. Один ряд (или его
// отсутствие) трактуется как нейтральный «авто»-старт.
func (h *HomePage) applyDefaults() {
	if h.Title == "" {
		h.Title = "Главная"
	}
	if h.Layout == "" {
		switch {
		case len(h.Widgets) > 0:
			h.Layout = "grid"
		case len(h.Rows) > 1:
			h.Layout = "rows"
		default:
			h.Layout = "auto"
		}
	}
}

// LoadHomePage reads config/home_page.yaml. Returns nil, nil when file does not exist —
// caller is expected to fall back to a default page in that case.
func LoadHomePage(path string) (*HomePage, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hp HomePage
	if err := yaml.Unmarshal(data, &hp); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	hp.applyDefaults()
	return &hp, nil
}

// WidgetNames returns every widget name referenced by the page in the order it
// would be rendered.
func (h *HomePage) WidgetNames() []string {
	if h == nil {
		return nil
	}
	var out []string
	for _, r := range h.Rows {
		out = append(out, r.Widgets...)
	}
	for _, w := range h.Widgets {
		out = append(out, w.Name)
	}
	return out
}

// RowGroups returns widget names grouped by configured row, preserving order.
// Each Rows[i] becomes one group; any flat Widgets are appended as a final
// group. Used by the dashboard renderer in "rows" (WYSIWYG) layout so that the
// configured row boundaries are honoured instead of being flattened.
func (h *HomePage) RowGroups() [][]string {
	if h == nil {
		return nil
	}
	var groups [][]string
	for _, r := range h.Rows {
		if len(r.Widgets) > 0 {
			groups = append(groups, append([]string(nil), r.Widgets...))
		}
	}
	if len(h.Widgets) > 0 {
		var names []string
		for _, w := range h.Widgets {
			names = append(names, w.Name)
		}
		groups = append(groups, names)
	}
	return groups
}
