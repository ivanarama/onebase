package launcher

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/ui"
)

var launcherBundle *i18n.Bundle

var cfgTmpl = template.Must(template.New("cfg").Funcs(template.FuncMap{
	"t": func(lang, key string) string {
		if launcherBundle != nil {
			return launcherBundle.T(lang, key)
		}
		return key
	},
	"selIf": func(a, b string) string {
		if a == b {
			return " selected"
		}
		return ""
	},
	"dict": func(pairs ...any) map[string]any {
		m := make(map[string]any, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			if k, ok := pairs[i].(string); ok {
				m[k] = pairs[i+1]
			}
		}
		return m
	},
	"lower":  strings.ToLower,
	"join":   strings.Join,
	"printf": fmt.Sprintf,
	// Иконки навигации (план 72): рендер инлайн-SVG Lucide, список имён и JSON для
	// живого превью в конфигураторе. Один источник — internal/ui (карта lucideIcons).
	"lucideIcon":      ui.LucideIcon,
	"lucideNames":     ui.LucideNames,
	"lucideIconsJSON": ui.LucideIconsJSON,
	"js": func(v any) template.JS {
		// json.Marshal экранирует <, >, & в \uXXXX; возвращаем template.JS,
		// чтобы html/template не экранировал повторно (двойное экранирование).
		b, err := json.Marshal(v)
		if err != nil {
			return template.JS("null")
		}
		return template.JS(b) //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности, поэтому «</script>» из данных не разорвёт тег
	},
	"fieldTypeLabel": func(typ, ref string) string {
		switch typ {
		case "string":
			return "строка"
		case "number":
			return "число"
		case "date":
			return "дата"
		case "bool":
			return "булево"
		case "reference":
			return "→ " + ref
		case "enum":
			return "перечисление"
		default:
			return typ
		}
	},
	"fieldTypeClass": func(typ string) string {
		switch typ {
		case "reference":
			return "ft-ref"
		case "number":
			return "ft-num"
		case "date":
			return "ft-date"
		case "bool":
			return "ft-bool"
		case "enum":
			return "ft-ref"
		default:
			return "ft-str"
		}
	},
	// filterFormsByEntity — фильтрует срез cfgManagedForm по имени сущности
	// (без учёта регистра). Возвращает новый срез; в шаблоне используется
	// {{$mine := filterFormsByEntity .ManagedForms $e.Name}} вместо
	// присваивания флага внутри {{range}} (которое в Go templates не
	// «вытекает» из цикла), что устраняет ложный «нет управляемых форм».
	"filterFormsByEntity": func(forms []cfgManagedForm, entity string) []cfgManagedForm {
		entLower := strings.ToLower(entity)
		out := make([]cfgManagedForm, 0, len(forms))
		for _, f := range forms {
			if strings.ToLower(f.Entity) == entLower {
				out = append(out, f)
			}
		}
		return out
	},
	"formLabel": func(name string) string {
		lower := strings.ToLower(name)
		switch lower {
		case "формаобъекта":
			return "Форма объекта"
		case "формасписка":
			return "Форма списка"
		case "формавыбора":
			return "Форма выбора"
		case "форма":
			return "Форма"
		default:
			return name
		}
	},
}).Parse(cfgTitlesBlock + cfgHead + cfgMain + cfgTabTree + cfgRegDetail + cfgTabConvert + cfgTabFiles + cfgTabBackup + cfgSyntaxRef + cfgFoot))

// Шаблонные строки конфигуратора вынесены в configurator_tmpl_*.go.
