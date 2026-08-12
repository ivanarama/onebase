package ui

import (
	"encoding/json"
	"html/template"
	"strings"
	"sync"

	"github.com/ivantit66/onebase/internal/webassets"
)

// Иконки навигации. Подход (B), план 73: весь Lucide одним SVG-спрайтом
// (internal/webassets/lucide/sprite.svg), страница ссылается на символ через
// <use>. Пришло на смену подходу (A) плана 72 — курируемому набору из 44
// инлайн-SVG в бинаре: чтобы добавить иконку, приходилось регенерировать файл
// данных, теперь работает любое имя Lucide. Здесь — обёртка, фолбэк и синонимы.

// LucideSpriteURL — путь, по которому спрайт смонтирован в обоих серверах (ui и
// launcher). Тот же URL подставляется в превью конфигуратора, поэтому браузер
// качает спрайт один раз на оба контура. Параметр v зависит от содержимого:
// после апгрейда Lucide новый спрайт не залипнет в кэше под прежним URL.
func LucideSpriteURL() string {
	return "/vendor/lucide/sprite.svg?v=" + webassets.LucideSpriteVersion()
}

// lucideAliases — синонимы: прежние имена Lucide (до переименований v1) и привычные
// сокращения → каноничное имя в спрайте. Позволяет старым значениям icon в
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
// не распозналось (повод поправить в конфигураторе). Без фолбэка ссылка на
// несуществующий символ просто ничего бы не нарисовала — молча и незаметно.
const lucideFallback = "square"

var (
	lucideSetOnce sync.Once
	lucideSet     map[string]struct{}
)

// lucideKnown сообщает, есть ли такой символ в спрайте. Множество строится из
// самого спрайта (webassets.LucideSymbolNames), поэтому не может разойтись с ним.
func lucideKnown(name string) bool {
	lucideSetOnce.Do(func() {
		names := webassets.LucideSymbolNames()
		lucideSet = make(map[string]struct{}, len(names))
		for _, n := range names {
			lucideSet[n] = struct{}{}
		}
	})
	_, ok := lucideSet[name]
	return ok
}

// LucideIcon возвращает ссылку на иконку Lucide в спрайте по её имени (kebab-case,
// регистронезависимо, лишние пробелы игнорируются).
//
//   - пустое имя        → пустая строка (иконка не рисуется, без отступа);
//   - известное имя     → <svg><use href="…#имя"></svg>;
//   - неизвестное имя   → иконка-фолбэк (квадрат), без битой ссылки на пустой символ.
//
// Результат — template.HTML (разметка наша, доверенная). В href подставляется
// только имя, которое нашлось среди символов спрайта; всё остальное уходит в
// фолбэк, поэтому произвольная строка из конфигурации в разметку не попадает.
// Атрибуты обводки остаются на внешнем <svg>: символы спрайта их не несут, а
// currentColor наследуется — цвет и размер по-прежнему задаются CSS-ом.
func LucideIcon(name string) template.HTML {
	key := NormalizeIconName(name)
	if key == "" {
		return ""
	}
	if canon, ok := lucideAliases[key]; ok {
		key = canon
	}
	if !lucideKnown(key) {
		key = lucideFallback
	}
	return template.HTML(`<svg class="lucide ob-icon" xmlns="http://www.w3.org/2000/svg" ` + //nolint:gosec // G203: в разметку идёт только имя символа, найденное в спрайте; всё прочее заменяется фолбэком
		`width="1em" height="1em" viewBox="0 0 24 24" fill="none" stroke="currentColor" ` +
		`stroke-width="2" stroke-linecap="round" stroke-linejoin="round" ` +
		`aria-hidden="true" focusable="false"><use href="` + LucideSpriteURL() + `#` + key + `"/></svg>`)
}

// LucideNames возвращает отсортированный список имён всех иконок спрайта — для
// подсказки (datalist) в конфигураторе. Синонимы из lucideAliases не включаются:
// показываем рекомендуемые каноничные имена.
func LucideNames() []string {
	return webassets.LucideSymbolNames()
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
	lucideAliasesJSONOnce sync.Once
	lucideAliasesJSON     template.JS
)

// LucideAliasesJSON возвращает JSON-объект синонимов «ввод → каноничное имя» для
// живого превью иконки в конфигураторе. Разметку иконок клиенту больше не
// передаём (при полном наборе это сотни килобайт на странице): превью само
// собирает <use> по имени, а список валидных имён берёт из datalist, который и
// так есть на странице. json.Marshal экранирует <, >, & в \uXXXX, поэтому
// встраивание в <script> безопасно; тип template.JS отключает повторное
// экранирование html/template. Результат вычисляется один раз и кешируется.
func LucideAliasesJSON() template.JS {
	lucideAliasesJSONOnce.Do(func() {
		b, err := json.Marshal(lucideAliases)
		if err != nil {
			lucideAliasesJSON = template.JS("{}")
			return
		}
		lucideAliasesJSON = template.JS(b) //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности, поэтому «</script>» из данных не разорвёт тег
	})
	return lucideAliasesJSON
}
