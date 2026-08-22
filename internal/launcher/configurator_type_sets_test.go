package launcher

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// typeSelectValues собирает значения пунктов всех выпадающих списков ТИПА на
// странице: имя списка либо оканчивается на «.type», либо равно «type».
func typeSelectValues(t *testing.T, page string) map[string][]string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("разбор HTML: %v", err)
	}
	out := map[string][]string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "select" {
			if name, ok := attr(n, "name"); ok && (name == "type" || strings.HasSuffix(name, ".type")) {
				var vals []string
				var opts func(*html.Node)
				opts = func(x *html.Node) {
					if x.Type == html.ElementNode && x.Data == "option" {
						if v, ok := attr(x, "value"); ok {
							vals = append(vals, v)
						}
						return
					}
					for c := x.FirstChild; c != nil; c = c.NextSibling {
						opts(c)
					}
				}
				opts(n)
				out[name] = vals
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// Набор типов в cfgTypeSets обязан совпадать с пунктами выпадающего списка в
// разметке. Списки живут в двух местах вынужденно: подписи пунктов должны
// оставаться литералами {{t $.Lang "…"}}, иначе ключи выпадут из-под гейта
// переводов (tools/i18ncheck ищет их регуляркой по шаблонам). Значит, за
// расхождением обязан следить тест, а не внимательность: разойдясь, набор
// перестанет добавлять запасной пункт там, где он нужен, и порча типов
// вернётся молча — ровно так и случилось в #1090.
func TestCfgTypeSets_MatchTemplateOptions(t *testing.T) {
	// Поля намеренно строковые: с типом из набора запасной пункт не рисуется,
	// и в списке остаются только «штатные» значения.
	str := []cfgField{{Name: "Поле", Type: "string"}}
	cases := []struct {
		set  string
		data *configuratorData
	}{
		{"entity", &configuratorData{Catalogs: []cfgEntity{{
			Name: "Товар", Kind: "Справочник", Fields: str,
			TableParts: []cfgTablePart{{Name: "Состав", Fields: str}},
		}}}},
		{"register", &configuratorData{Registers: []cfgRegister{{
			Name: "Остатки", Dimensions: str, Resources: str, Attributes: str,
		}}}},
		{"register", &configuratorData{InfoRegisters: []cfgInfoRegister{{
			Name: "Курсы", Dimensions: str, Resources: str,
		}}}},
		{"account", &configuratorData{AccountRegisters: []cfgAccountRegister{{
			Name: "Бухучёт", Resources: str,
		}}}},
		{"constant", &configuratorData{Constants: []cfgConstant{{
			Name: "Организация", Type: "string",
		}}}},
	}

	for _, c := range cases {
		c.data.Base = &Base{ID: "b", Name: "X", ConfigSource: "file"}
		c.data.Lang = "ru"
		c.data.Tab = "tree"
		selects := typeSelectValues(t, renderCfgTree(t, c.data))
		if len(selects) == 0 {
			t.Fatalf("набор %s: на странице нет ни одного списка типа", c.set)
		}
		want := append([]string(nil), cfgTypeSets[c.set]...)
		sort.Strings(want)
		for name, got := range selects {
			sorted := append([]string(nil), got...)
			sort.Strings(sorted)
			if strings.Join(sorted, ",") != strings.Join(want, ",") {
				t.Errorf("список %q (набор %s): пункты %v, а cfgTypeSets — %v",
					name, c.set, got, cfgTypeSets[c.set])
			}
		}
	}
}

// Строку для НОВОГО поля рисует не шаблон, а cfgAddField в
// static/configurator.js — и её список типов расходился с серверным незаметно:
// пункты «картинка» и «форматированный текст» появились в разметке, а в
// скрипте их не было, из-за чего завести поле-картинку кнопкой «+ Добавить»
// по-прежнему было нельзя. Сторож читает набор прямо из скрипта.
func TestCfgTypeSets_MatchScriptOptions(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("static", "configurator.js"))
	if err != nil {
		t.Fatalf("чтение configurator.js: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "function cfgTypeOptionsHTML")
	if start < 0 {
		t.Fatal("в configurator.js нет cfgTypeOptionsHTML — набор типов новой строки переехал")
	}
	end := strings.Index(body[start:], "\nfunction ")
	if end < 0 {
		t.Fatal("не найден конец cfgTypeOptionsHTML")
	}
	fn := body[start : start+end]

	// base — общая часть, к ней entity дописывает свои пункты через concat.
	values := regexp.MustCompile(`\['([a-z]+)',\s*T\(`).FindAllStringSubmatch(fn, -1)
	if len(values) == 0 {
		t.Fatal("в cfgTypeOptionsHTML не разобрать ни одного значения типа")
	}
	var base, entityExtra []string
	concat := strings.Index(fn, "base.concat(")
	for _, m := range values {
		if idx := strings.Index(fn, m[0]); concat >= 0 && idx > concat {
			entityExtra = append(entityExtra, m[1])
			continue
		}
		base = append(base, m[1])
	}

	got := map[string][]string{
		"register-new": base,
		"entity-new":   append(append([]string(nil), base...), entityExtra...),
	}
	for set, list := range got {
		want := append([]string(nil), cfgTypeSets[set]...)
		sort.Strings(want)
		sorted := append([]string(nil), list...)
		sort.Strings(sorted)
		if strings.Join(sorted, ",") != strings.Join(want, ",") {
			t.Errorf("набор %s в configurator.js — %v, а в cfgTypeSets — %v", set, list, cfgTypeSets[set])
		}
	}

	// Новую строку реквизита обязан рисовать набор «entity-new», иначе
	// картинку по-прежнему нельзя завести. Проверяем сам вызов из разметки.
	if !strings.Contains(cfgTabTree, `'new_field','{{$e.Name}}','entity'`) {
		t.Error("кнопка «+ Добавить поле» у реквизитов объекта зовёт cfgAddField без набора entity")
	}
}

// Запасной пункт обязан появляться ровно тогда, когда типа нет в наборе, и
// нести ПОЛНУЮ запись типа: у перечисления в регистре короткое «enum» без
// имени дало бы на сохранении заведомо нерабочий тип.
func TestUnlistedFieldType(t *testing.T) {
	cases := []struct {
		name string
		set  string
		f    cfgField
		want string
	}{
		{"строка в наборе", "entity", cfgField{Type: "string"}, ""},
		{"картинка теперь в наборе", "entity", cfgField{Type: "image"}, ""},
		{"форматированный текст в наборе", "entity", cfgField{Type: "richtext"}, ""},
		{"неизвестный тип сохраняется как есть", "entity", cfgField{Type: "text"}, "text"},
		{"перечисление регистра теперь в наборе", "register",
			cfgField{Type: "enum", EnumName: "РезультатСделки"}, ""},
		// Запасной пункт обязан нести ПОЛНУЮ запись типа: короткое «enum» без
		// имени дало бы на сохранении заведомо нерабочий тип. Проверяем на
		// наборе, который перечисления не предлагает.
		{"перечисление вне набора — с именем", "account",
			cfgField{Type: "enum", EnumName: "РезультатСделки"}, "enum:РезультатСделки"},
		{"картинка в регистре — не из набора", "register", cfgField{Type: "image"}, "image"},
		{"дата у ресурса плана счетов", "account", cfgField{Type: "date"}, "date"},
		{"разрядность числа не теряется", "account",
			cfgField{Type: "number(10,2)", Length: 10, Scale: 2}, "number(10,2)"},
		{"пустой тип не даёт пустого пункта", "entity", cfgField{Type: ""}, ""},
	}
	for _, c := range cases {
		if got := cfgUnlistedFieldType(c.set, c.f); got != c.want {
			t.Errorf("%s: получено %q, ожидалось %q", c.name, got, c.want)
		}
	}
	// Булева константа: метаданные пишут "bool", а список предлагает "boolean".
	if got := cfgUnlistedType("constant", "bool"); got != "bool" {
		t.Errorf("булева константа: получено %q, ожидалось \"bool\"", got)
	}
	if got := cfgUnlistedType("constant", "enum:СтавкаНДС"); got != "enum:СтавкаНДС" {
		t.Errorf("константа-перечисление: получено %q", got)
	}
}
