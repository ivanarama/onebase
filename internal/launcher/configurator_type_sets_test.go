package launcher

import (
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
		{"перечисление регистра — с именем", "register",
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
