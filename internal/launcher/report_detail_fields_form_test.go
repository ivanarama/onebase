package launcher

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/report"
	"golang.org/x/net/html"
)

// Форма конфигуратора пересобирает composition целиком из своих полей, поэтому
// detail_fields (редактируются в YAML) едут в ней скрытыми полями: сохранение
// отчёта из конфигуратора не должно их терять. Круг: настоящий шаблон →
// поля формы, как их отправит браузер → штатный разбор.
func TestReportBuilderKeepsDetailFieldsOnSave(t *testing.T) {
	want := []report.DetailField{{Field: "Номер", Title: "Номер заявки"}, {Field: "Наименование"}}
	page := renderConfiguratorReport(t, &report.Composition{
		Groupings: []string{"Мастер"}, Detail: true,
		Measures:     []report.Measure{{Field: "Штраф", Agg: "sum"}},
		DetailFields: want,
	})
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			var name, value, typ string
			checked := false
			for _, a := range n.Attr {
				switch a.Key {
				case "name":
					name = a.Val
				case "value":
					value = a.Val
				case "type":
					typ = a.Val
				case "checked":
					checked = true
				}
			}
			if strings.HasPrefix(name, "comp.") && ((typ != "checkbox" && typ != "radio") || checked) {
				form.Add(name, value)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	form.Set("comp.present", "1")
	c, present := parseCompositionForm(form)
	if !present || c == nil {
		t.Fatal("composition form not parsed")
	}
	if !reflect.DeepEqual(c.DetailFields, want) {
		t.Fatalf("detail_fields after save = %+v, want %+v", c.DetailFields, want)
	}
}
