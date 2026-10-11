package launcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/report"
	"golang.org/x/net/html"
)

// Исполняем repAddParam и выбираем только реально предложенный option.
// Ручная подстановка param.0.type=bool не обнаружила бы дефект кнопки «+».
func TestConfiguratorAddReportParam_BoolRoundTrip(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	p := writeCfgFile(t, cfgDir, "reports", "продажи.yaml", `name: Продажи
title: Продажи
query: ВЫБРАТЬ 1 КАК Один
`)
	router := chi.NewRouter()
	router.Get("/bases/{id}/configurator", h.configuratorPage)
	router.Post("/bases/{id}/configurator/report", h.configuratorSaveReport)
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/bases/test/configurator?tab=tree", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", get.Code, get.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(get.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	form := findForm(doc, "/configurator/report")
	if form == nil {
		t.Fatal("форма отчёта не найдена")
	}
	var table *html.Node
	var findTable func(*html.Node)
	findTable = func(n *html.Node) {
		if id, _ := attr(n, "id"); n.Data == "table" && id == "params-Продажи" {
			table = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findTable(c)
		}
	}
	findTable(form)
	if table == nil {
		t.Fatal("таблица параметров не найдена")
	}
	var row *html.Node
	rowDoc, err := html.Parse(strings.NewReader("<table><tr>" + reportAddedParamRow(t) + "</tr></table>"))
	if err != nil {
		t.Fatal(err)
	}
	var picked bool
	var fillRow func(*html.Node)
	fillRow = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			row = n
		}
		if name, _ := attr(n, "name"); n.Data == "input" && name == "param.0.name" {
			for i := range n.Attr {
				if n.Attr[i].Key == "value" {
					n.Attr[i].Val = "Флаг"
				}
			}
		}
		if value, _ := attr(n, "value"); n.Data == "option" && value == "bool" {
			n.Attr = append(n.Attr, html.Attribute{Key: "selected"})
			picked = true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			fillRow(c)
		}
	}
	fillRow(rowDoc)
	if !picked {
		t.Fatal("кнопка добавления параметра не предлагает bool")
	}
	row.Parent.RemoveChild(row)
	table.AppendChild(row)
	var page strings.Builder
	if err := html.Render(&page, doc); err != nil {
		t.Fatal(err)
	}
	values := browserSubmit(t, page.String(), "/configurator/report")
	req := httptest.NewRequest(http.MethodPost, "/bases/test/configurator/report", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Onebase-Ajax", "1")
	post := httptest.NewRecorder()
	router.ServeHTTP(post, req)
	if ok, errText := cfgResponse(t, post); !ok {
		t.Fatalf("POST: %s", errText)
	}
	after, err := report.LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Params) != 1 || after.Params[0].Name != "Флаг" || after.Params[0].Type != "bool" {
		t.Fatalf("новый bool-параметр не сохранился: %+v", after.Params)
	}
	get = httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/bases/test/configurator?tab=tree", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("повторный GET: %d %s", get.Code, get.Body.String())
	}
	if got := browserSubmit(t, get.Body.String(), "/configurator/report").Get("param.0.type"); got != "bool" {
		t.Errorf("повторное открытие нового параметра: type=%q, нужен bool", got)
	}
}
