package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

// Страница набора СтраницыФормы с hidden_when, ставшим истинным после события
// формы, обязана исчезнуть вместе с кнопкой закладки. У страницы нет своего
// якоря data-ob-el-path, и раньше ответ события с hidden=true для неё клиент
// молча пропускал: закладка оставалась на форме до перезагрузки (так оператор
// видел закладки, которые форма прятала в ПриОткрытии).

func заявкаСЗакладками() *metadata.Entity {
	ent := &metadata.Entity{
		Name: "ЗаявкаСЗакладками", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "СтадияОформления", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}
	form := managedObjectForm(
		fieldEl("ПолеСтадии", "Объект.СтадияОформления"),
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "КнопкаПринять",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Принять"},
		},
		&metadata.FormElement{
			Kind: metadata.FormElementPages, Name: "Закладки",
			Children: []*metadata.FormElement{
				{Kind: metadata.FormElementPage, Name: "СтрОсновное",
					Children: []*metadata.FormElement{fieldEl("ПолеКомментарий", "Объект.Комментарий")}},
				{Kind: metadata.FormElementPage, Name: "СтрДействия",
					HiddenWhen: `СтадияОформления = "Принята"`},
				{Kind: metadata.FormElementPage, Name: "СтрДеньги"},
			},
		},
	)
	ent.Forms = []*metadata.FormModule{form}
	return ent
}

type managedTabButton struct {
	Page   string `json:"page"`
	Path   string `json:"path"`
	Idx    string `json:"idx"`
	Active bool   `json:"active"`
	Hidden bool   `json:"hidden"`
}

type managedTabContent struct {
	Idx     string `json:"idx"`
	Display string `json:"display"`
}

type managedTabsState struct {
	Buttons  []managedTabButton  `json:"buttons"`
	Contents []managedTabContent `json:"contents"`
}

// managedTabsFromHTML снимает с настоящей разметки формы кнопки закладок и
// блоки содержимого одного набора — ровно то, с чем работает managed.js.
func managedTabsFromHTML(t *testing.T, rendered, setName string) managedTabsState {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatalf("parse managed form HTML: %v", err)
	}
	var set *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if set != nil {
			return
		}
		if n.Type == html.ElementNode {
			if v, ok := managedHTMLAttr(n, "data-tabs"); ok && v == setName {
				set = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if set == nil {
		t.Fatalf("на форме нет набора закладок %q", setName)
	}
	var st managedTabsState
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class, _ := managedHTMLAttr(n, "class")
			switch {
			case strings.Contains(" "+class+" ", " managed-tab-btn "):
				page, _ := managedHTMLAttr(n, "data-tab-page")
				path, _ := managedHTMLAttr(n, "data-ob-tab-path")
				idx, _ := managedHTMLAttr(n, "data-tab-idx")
				st.Buttons = append(st.Buttons, managedTabButton{
					Page: page, Path: path, Idx: idx,
					Active: strings.Contains(" "+class+" ", " active "),
				})
			case strings.Contains(" "+class+" ", " managed-tab-content "):
				idx, _ := managedHTMLAttr(n, "data-tab-content")
				style, _ := managedHTMLAttr(n, "style")
				display := "none"
				if strings.Contains(style, "display:block") {
					display = "block"
				}
				st.Contents = append(st.Contents, managedTabContent{Idx: idx, Display: display})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(set)
	return st
}

// applyManagedTabStatesInNode открывает закладку open (как щелчок
// пользователя), затем по очереди применяет ответы событий формы функцией
// applyElementStates из production managed.js и возвращает состояние закладок
// после каждого ответа.
func applyManagedTabStatesInNode(t *testing.T, tabs managedTabsState, open string, states ...*elementStates) []managedTabsState {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for managed tab state integration test")
	}
	payload, err := json.Marshal(struct {
		Tabs   managedTabsState `json:"tabs"`
		Open   string           `json:"open"`
		States []*elementStates `json:"states"`
	}{Tabs: tabs, Open: open, States: states})
	if err != nil {
		t.Fatal(err)
	}

	const script = `
const fs = require('node:fs');
const source = fs.readFileSync(process.argv[1], 'utf8');
function extract(name) {
  const start = source.indexOf('function ' + name + '(');
  if (start < 0) throw new Error('managed.js has no function ' + name);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') {
      depth--;
      if (depth === 0) return source.slice(start, i + 1);
    }
  }
  throw new Error('unterminated function ' + name);
}
const payload = JSON.parse(fs.readFileSync(0, 'utf8'));
function classList(initial) {
  const set = new Set(initial);
  return {
    add(c) { set.add(c); },
    remove(c) { set.delete(c); },
    contains(c) { return set.has(c); },
  };
}
const headers = {children: []};
const tabs = {
  getAttribute(name) { return name === 'data-tabs' ? 'Закладки' : null; },
  querySelectorAll(selector) {
    if (selector === '.managed-tab-btn') return buttons;
    if (selector === '.managed-tab-content') return contents;
    const m = selector.match(/^\.managed-tab-content\[data-tab-content="([^"]*)"\]$/);
    if (m) return contents.filter((c) => c.idx === m[1]);
    throw new Error('unexpected selector ' + selector);
  },
  querySelector(selector) {
    const m = selector.match(/^\.managed-tab-content\[data-tab-content="([^"]*)"\]$/);
    if (!m) throw new Error('unexpected selector ' + selector);
    return contents.find((c) => c.idx === m[1]) || null;
  },
};
const buttons = payload.tabs.buttons.map((b) => ({
  tagName: 'BUTTON',
  page: b.page, path: b.path, idx: b.idx,
  style: {display: ''},
  classList: classList(b.active ? ['managed-tab-btn', 'active'] : ['managed-tab-btn']),
  parentElement: headers,
  getAttribute(name) {
    if (name === 'data-tab-idx') return this.idx;
    if (name === 'data-tab-page') return this.page;
    if (name === 'data-ob-tab-path') return this.path;
    return null;
  },
  closest(selector) { return selector === '.managed-tabs' ? tabs : null; },
}));
headers.children = buttons;
const contents = payload.tabs.contents.map((c) => ({
  idx: c.idx, style: {display: c.display},
  closest(selector) { return selector === '.managed-tabs' ? tabs : null; },
}));
global.window = {CSS: null};
global.location = {pathname: '/ui/catalog/x'};
global.sessionStorage = {setItem() {}, getItem() { return null; }};
global.document = {
  querySelectorAll() { return []; }, // Required inputs are covered in Chromium.
  querySelector(selector) {
    if (selector.startsWith('[data-ob-el-path=')) return null;
    const m = selector.match(/^\.managed-tab-btn\[data-ob-tab-path="([^"]*)"\]$/);
    if (!m) throw new Error('unexpected selector ' + selector);
    return buttons.find((b) => b.path === m[1]) || null;
  },
};
const fns = new Function(
  extract('obManagedTabKey') + extract('obManagedOwnTabParts') + extract('obManagedSwitchTab') +
  extract('obManagedSetPageHidden') + extract('obManagedSyncPageRequired') + extract('applyElementStates') +
  '\nreturn {applyElementStates, obManagedSwitchTab};'
)();
const open = buttons.find((b) => b.page === payload.open);
if (!open) throw new Error('no tab ' + payload.open);
fns.obManagedSwitchTab(open);
const snapshots = [];
for (const st of payload.states) {
  fns.applyElementStates(st);
  snapshots.push({
    buttons: buttons.map((b) => ({
      page: b.page, path: b.path, idx: b.idx,
      active: b.classList.contains('active'),
      hidden: b.style.display === 'none',
    })),
    contents: contents.map((c) => ({idx: c.idx, display: c.style.display})),
  });
}
process.stdout.write(JSON.stringify(snapshots));
`
	cmd := exec.CommandContext(t.Context(), node, "-e", script, "static/managed.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execute managed.js applyElementStates: %v\n%s", err, output)
	}
	var result []managedTabsState
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode managed.js result: %v; output=%s", err, output)
	}
	return result
}

func закладка(t *testing.T, st managedTabsState, page string) (managedTabButton, managedTabContent) {
	t.Helper()
	for _, b := range st.Buttons {
		if b.Page != page {
			continue
		}
		for _, c := range st.Contents {
			if c.Idx == b.Idx {
				return b, c
			}
		}
		t.Fatalf("у закладки %q нет содержимого: %#v", page, st)
	}
	t.Fatalf("нет закладки %q: %#v", page, st)
	return managedTabButton{}, managedTabContent{}
}

func TestСтраницаСкрытаСобытием_ЗакладкаИсчезает(t *testing.T) {
	ent := заявкаСЗакладками()
	form := ent.Forms[0]
	form.ProgramAST = mustParse(t, `
Процедура Принять()
	Объект.СтадияОформления = "Принята";
КонецПроцедуры
`)
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"СтадияОформления": "НаОформлении"}, ent); err != nil {
		t.Fatal(err)
	}

	formRequest := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
	formResponse := httptest.NewRecorder()
	srv.formEdit(formResponse, formRequest)
	if formResponse.Code != http.StatusOK {
		t.Fatalf("публичная отрисовка формы: статус=%d body=%s", formResponse.Code, formResponse.Body.String())
	}
	tabs := managedTabsFromHTML(t, formResponse.Body.String(), "Закладки")
	путь := путьСостояния(t, form, "СтрДействия")
	if b, _ := закладка(t, tabs, "СтрДействия"); b.Path != путь {
		t.Fatalf("кнопка закладки без якоря страницы: data-ob-tab-path=%q, ожидался %q", b.Path, путь)
	}

	body := url.Values{}
	body.Set("_id", id.String())
	body.Set("_element", "КнопкаПринять")
	body.Set("_event", string(metadata.FormEventOnClick))
	body.Set("_kind", "object")
	body.Set("СтадияОформления", "НаОформлении")
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if !resp.OK {
		t.Fatalf("событие формы завершилось ошибкой: %q", resp.Error)
	}
	if resp.ElementStates == nil || !resp.ElementStates.Hidden[путь] {
		t.Fatalf("ответ события не скрыл страницу %q: %#v", путь, resp.ElementStates)
	}

	// Пользователь стоял на скрываемой закладке; затем условие снова ложно.
	shown := &elementStates{Hidden: map[string]bool{путь: false}}
	got := applyManagedTabStatesInNode(t, tabs, "СтрДействия", resp.ElementStates, shown)

	hiddenBtn, hiddenContent := закладка(t, got[0], "СтрДействия")
	if !hiddenBtn.Hidden || hiddenBtn.Active || hiddenContent.Display != "none" {
		t.Fatalf("скрытая страница осталась на форме: кнопка=%#v содержимое=%#v", hiddenBtn, hiddenContent)
	}
	firstBtn, firstContent := закладка(t, got[0], "СтрОсновное")
	if !firstBtn.Active || firstContent.Display != "block" {
		t.Fatalf("вместо скрытой не открылась первая видимая закладка: кнопка=%#v содержимое=%#v", firstBtn, firstContent)
	}
	if money, _ := закладка(t, got[0], "СтрДеньги"); money.Hidden {
		t.Fatalf("страница без условия скрыта: %#v", money)
	}

	backBtn, backContent := закладка(t, got[1], "СтрДействия")
	if backBtn.Hidden || backBtn.Active || backContent.Display != "none" {
		t.Fatalf("hidden=false должен вернуть кнопку, не открывая страницу: кнопка=%#v содержимое=%#v", backBtn, backContent)
	}
	if firstBtn, _ := закладка(t, got[1], "СтрОсновное"); !firstBtn.Active {
		t.Fatalf("возврат кнопки переключил открытую закладку: %#v", got[1])
	}
}
