package ui

import (
	"encoding/json"
	"html/template"
	"sort"
	"strings"
	"sync"
)

// Иконки навигации (план 72). Подход (A): курируемый набор инлайн-SVG Lucide
// (lucide-icons/lucide) прямо в бинаре — без JS и клиентских ассетов. Сами иконки
// лежат в сгенерированном icons_data.go (карта lucideIcons: имя → внутренняя
// разметка <svg>). Здесь — обёртка, фолбэк и синонимы имён.

// lucideAliases — синонимы: прежние имена Lucide (до переименований v1) и привычные
// сокращения → каноничное имя в lucideIcons. Позволяет старым значениям icon в
// конфигурациях и плейсхолдерам продолжать работать.
var lucideAliases = map[string]string{
	"home":        "house",
	"cart":        "shopping-cart",
	"ruble":       "russian-ruble",
	"rub":         "russian-ruble",
	"bar-chart":   "chart-column",
	"bar-chart-3": "chart-column",
	"barchart":    "chart-column",
	"pie-chart":   "chart-pie",
	"line-chart":  "chart-line",
	"settings-2":  "settings",
	"cog":         "settings",
	"folder-open": "folder",
}

// lucideFallback — иконка для непустого, но неизвестного имени: нейтральный квадрат.
// Так навигация не остаётся с битой/пустой разметкой, а пользователь видит, что имя
// не распозналось (повод поправить в конфигураторе).
const lucideFallback = "square"

// LucideIcon возвращает инлайн-SVG иконки Lucide по её имени (kebab-case,
// регистронезависимо, лишние пробелы игнорируются).
//
//   - пустое имя        → пустая строка (иконка не рисуется, без отступа);
//   - известное имя     → SVG этой иконки;
//   - неизвестное имя   → SVG иконки-фолбэка (квадрат), без паники и без битой разметки.
//
// Результат — template.HTML (разметка наша, доверенная). Имя пользователя в HTML не
// подставляется: оно лишь ключ в карте, поэтому XSS через значение icon невозможен.
// Используется в funcMap шаблонов ui (навигация) и launcher (превью в конфигураторе).
func LucideIcon(name string) template.HTML {
	key := NormalizeIconName(name)
	if key == "" {
		return ""
	}
	if canon, ok := lucideAliases[key]; ok {
		key = canon
	}
	body, ok := lucideIcons[key]
	if !ok {
		body = lucideIcons[lucideFallback]
	}
	return template.HTML(`<svg class="lucide ob-icon" xmlns="http://www.w3.org/2000/svg" ` + //nolint:gosec // G203: разметка константная, из данных сюда ничего не подставляется
		`width="1em" height="1em" viewBox="0 0 24 24" fill="none" stroke="currentColor" ` +
		`stroke-width="2" stroke-linecap="round" stroke-linejoin="round" ` +
		`aria-hidden="true" focusable="false">` + body + `</svg>`)
}

// LucideNames возвращает отсортированный список канонических имён доступных иконок —
// для подсказки (datalist) в конфигураторе. Синонимы из lucideAliases не включаются:
// показываем рекомендуемые каноничные имена.
func LucideNames() []string {
	names := make([]string, 0, len(lucideIcons))
	for n := range lucideIcons {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// NormalizeIconName приводит имя иконки к каноничному виду Lucide: нижний регистр,
// без обрамляющих пробелов, внутренние пробелы и подчёркивания → дефис, повторы
// дефисов схлопываются (kebab-case). Пустое имя остаётся пустым. Применяется при
// сохранении подсистем/страниц, чтобы ввод вроде «Shopping Cart» или «shopping_cart»
// сохранялся как «shopping-cart». Синонимы (home→house) не разворачиваются — это
// делает LucideIcon при рендере.
func NormalizeIconName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		if r == ' ' || r == '_' || r == '-' {
			if b.Len() > 0 && !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
			continue
		}
		b.WriteRune(r)
		prevDash = false
	}
	return strings.TrimRight(b.String(), "-")
}

var (
	lucideJSONOnce sync.Once
	lucideJSON     template.JS
)

// LucideIconsJSON возвращает JSON-объект «имя → готовый <svg>» (каноничные имена и
// синонимы) для живого превью иконки в конфигураторе: один источник правды — та же
// карта lucideIcons, что и на сервере. json.Marshal экранирует <, >, & в \uXXXX,
// поэтому встраивание в <script> безопасно; тип template.JS отключает повторное
// экранирование html/template. Результат вычисляется один раз и кешируется.
func LucideIconsJSON() template.JS {
	lucideJSONOnce.Do(func() {
		m := make(map[string]string, len(lucideIcons)+len(lucideAliases))
		for name := range lucideIcons {
			m[name] = string(LucideIcon(name))
		}
		for alias := range lucideAliases {
			m[alias] = string(LucideIcon(alias))
		}
		b, err := json.Marshal(m)
		if err != nil {
			lucideJSON = template.JS("{}")
			return
		}
		lucideJSON = template.JS(b) //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности, поэтому «</script>» из данных не разорвёт тег
	})
	return lucideJSON
}
