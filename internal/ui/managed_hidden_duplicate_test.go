package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Реквизит «Улица» стоит на форме дважды: одна копия видна, пока заявка не
// принята, другая — после. Команда «Принять» на сервере меняет стадию (копии
// меняются местами) и пишет новую улицу. Скрытая копия остаётся в DOM в
// disabled fieldset, и после смены видимости отправляется уже она, поэтому ответ
// события обязан дойти до обеих (#1759).
//
// Тест идёт путём пользователя: настоящая разметка формы → то, что браузер
// отправит при открытии → публичное событие формы → ответ, применённый кодом
// managed.js → то, что форма отправит теперь → публичная запись → база. Копию,
// которую покажет команда, ставим и второй, и первой в DOM: раньше ответ доходил
// только до первого контрола с этим name, а у переключателя браузер держит одну
// отмеченную кнопку на все копии сразу.
func TestУсловноеСкрытие_НовоеЗначениеСобытияСохраняетсяИзПоказаннойКопии(t *testing.T) {
	for _, kind := range []metadata.FormElementType{metadata.FormElementField, metadata.FormElementSwitch} {
		for _, shownFirst := range []bool{false, true} {
			order := "показанная копия второй"
			if shownFirst {
				order = "показанная копия первой"
			}
			t.Run(fmt.Sprintf("%s/%s", kind, order), func(t *testing.T) {
				ent := заявкаСКопиямиУлицы(t, kind, shownFirst)
				srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
				id := uuid.New()
				if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{
					"СтадияОформления": "Черновик", "Улица": "Старая"}, ent); err != nil {
					t.Fatal(err)
				}
				page := parseManagedPage(t, отрисоватьСУсловиями(t, ent, ent.Forms[0], map[string]string{
					"СтадияОформления": "Черновик", "Улица": "Старая"}))

				opened := submitManagedPage(t, page, nil)
				if got := opened["Улица"]; !reflect.DeepEqual(got, []string{"Старая"}) {
					t.Fatalf("при открытии форма отправляет Улица=%q, ожидалось значение базы из одной видимой копии", got)
				}

				event := url.Values{}
				for name, values := range opened {
					event[name] = values
				}
				event.Set("_id", id.String())
				event.Set("_element", "КнопкаПринять")
				event.Set("_event", string(metadata.FormEventOnClick))
				event.Set("_kind", "object")
				response := executeFormEvent(t, srv, ent, event).Body.Bytes()
				if decoded := decodeFormEventResponse(t, response); !decoded.OK {
					t.Fatalf("событие формы завершилось ошибкой: %q", decoded.Error)
				}

				submitted := submitManagedPage(t, page, response)
				if got := submitted["Улица"]; !reflect.DeepEqual(got, []string{"Новая"}) {
					t.Fatalf("после события форма отправляет Улица=%q, а обработчик записал «Новая»", got)
				}

				записатьЗаявку(t, srv, ent, id, submitted)
				row, err := srv.store.GetByID(ctx, ent.Name, id, ent)
				if err != nil {
					t.Fatal(err)
				}
				if row["Улица"] != "Новая" || row["СтадияОформления"] != "Принята" {
					t.Fatalf("в базе Улица=%v СтадияОформления=%v, ожидались «Новая» и «Принята»",
						row["Улица"], row["СтадияОформления"])
				}
			})
		}
	}
}

// Видимая, но readonly копия переключателя не отправляется: readonly_when
// отключает сами радиокнопки (applyElementStates ставит disabled), а fieldset
// при этом уже не disabled. Ответ события обязан отметить кнопку, которая уйдёт
// с формой. Иначе клиент отмечал «Новая» в readonly-копии, браузер снимал
// отметку с редактируемой, и у реквизита не оставалось отправляемой отмеченной
// кнопки — запись теряла значение обработчика (#1759, ревью круга 5).
//
// Первая копия скрыта при открытии и после «Принять» показывается только для
// чтения, вторая остаётся редактируемой. Обратный порядок копий в DOM — тот же
// контракт с другой стороны.
func TestУсловноеСкрытие_ReadonlyКопияПереключателяНеЗабираетВыбор(t *testing.T) {
	for _, readonlyFirst := range []bool{true, false} {
		order := "readonly-копия первой"
		if !readonlyFirst {
			order = "readonly-копия второй"
		}
		t.Run(order, func(t *testing.T) {
			ent := заявкаСReadonlyКопиейПереключателя(t, readonlyFirst)
			srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
			id := uuid.New()
			if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{
				"СтадияОформления": "Черновик", "Улица": "Старая"}, ent); err != nil {
				t.Fatal(err)
			}
			page := parseManagedPage(t, отрисоватьСУсловиями(t, ent, ent.Forms[0], map[string]string{
				"СтадияОформления": "Черновик", "Улица": "Старая"}))

			opened := submitManagedPage(t, page, nil)
			if got := opened["Улица"]; !reflect.DeepEqual(got, []string{"Старая"}) {
				t.Fatalf("при открытии форма отправляет Улица=%q, ожидалось значение базы из редактируемой копии", got)
			}

			event := url.Values{}
			for name, values := range opened {
				event[name] = values
			}
			event.Set("_id", id.String())
			event.Set("_element", "КнопкаПринять")
			event.Set("_event", string(metadata.FormEventOnClick))
			event.Set("_kind", "object")
			response := executeFormEvent(t, srv, ent, event).Body.Bytes()
			decoded := decodeFormEventResponse(t, response)
			if !decoded.OK {
				t.Fatalf("событие формы завершилось ошибкой: %q", decoded.Error)
			}

			submitted := submitManagedPage(t, page, response)
			if got := submitted["Улица"]; !reflect.DeepEqual(got, []string{"Новая"}) {
				t.Fatalf("после события форма отправляет Улица=%q, а обработчик записал «Новая»", got)
			}

			записатьЗаявку(t, srv, ent, id, submitted)
			row, err := srv.store.GetByID(ctx, ent.Name, id, ent)
			if err != nil {
				t.Fatal(err)
			}
			if row["Улица"] != "Новая" || row["СтадияОформления"] != "Принята" {
				t.Fatalf("в базе Улица=%v СтадияОформления=%v, ожидались «Новая» и «Принята»",
					row["Улица"], row["СтадияОформления"])
			}
		})
	}
}

// заявкаСReadonlyКопиейПереключателя — «Улица» двумя переключателями: одна
// копия скрыта до «Принять» и после неё только для чтения, другая всегда
// редактируема. readonlyFirst ставит readonly-копию первой в DOM.
func заявкаСReadonlyКопиейПереключателя(t *testing.T, readonlyFirst bool) *metadata.Entity {
	t.Helper()
	options := []metadata.FormOption{{Value: "Старая"}, {Value: "Новая"}}
	толькоЧтение := &metadata.FormElement{
		Kind: metadata.FormElementSwitch, Name: "ПолеУлицаТолькоЧтение", DataPath: "Объект.Улица", Options: options,
		HiddenWhen: `СтадияОформления <> "Принята"`, ReadOnlyWhen: `СтадияОформления = "Принята"`,
	}
	редактируемая := &metadata.FormElement{
		Kind: metadata.FormElementSwitch, Name: "ПолеУлицаРедактируемая", DataPath: "Объект.Улица", Options: options,
	}
	elements := []*metadata.FormElement{fieldEl("ПолеСтадии", "Объект.СтадияОформления"), толькоЧтение, редактируемая}
	if !readonlyFirst {
		elements = []*metadata.FormElement{fieldEl("ПолеСтадии", "Объект.СтадияОформления"), редактируемая, толькоЧтение}
	}
	elements = append(elements, &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "КнопкаПринять",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Принять"},
	})
	form := managedObjectForm(elements...)
	form.ProgramAST = mustParse(t, `
Процедура Принять()
	Объект.СтадияОформления = "Принята";
	Объект.Улица = "Новая";
КонецПроцедуры
`)
	ent := &metadata.Entity{
		Name: "ЗаявкаСReadonlyКопией", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "СтадияОформления", Type: metadata.FieldTypeString},
			{Name: "Улица", Type: metadata.FieldTypeString},
		},
	}
	ent.Forms = []*metadata.FormModule{form}
	return ent
}

// заявкаСКопиямиУлицы — справочник с формой, на которой «Улица» нарисована
// двумя элементами одного вида с взаимоисключающими hidden_when. shownFirst
// ставит первой в DOM копию, которую покажет команда «Принять».
func заявкаСКопиямиУлицы(t *testing.T, kind metadata.FormElementType, shownFirst bool) *metadata.Entity {
	t.Helper()
	копия := func(name, hiddenWhen string) *metadata.FormElement {
		el := &metadata.FormElement{Kind: kind, Name: name, DataPath: "Объект.Улица", HiddenWhen: hiddenWhen}
		if kind == metadata.FormElementSwitch {
			el.Options = []metadata.FormOption{{Value: "Старая"}, {Value: "Новая"}}
		}
		return el
	}
	черновика := копия("ПолеУлицаЧерновика", `СтадияОформления = "Принята"`)
	принятой := копия("ПолеУлицаПринятой", `СтадияОформления <> "Принята"`)
	elements := []*metadata.FormElement{fieldEl("ПолеСтадии", "Объект.СтадияОформления"), черновика, принятой}
	if shownFirst {
		elements = []*metadata.FormElement{fieldEl("ПолеСтадии", "Объект.СтадияОформления"), принятой, черновика}
	}
	elements = append(elements, &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "КнопкаПринять",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Принять"},
	})
	form := managedObjectForm(elements...)
	form.ProgramAST = mustParse(t, `
Процедура Принять()
	Объект.СтадияОформления = "Принята";
	Объект.Улица = "Новая";
КонецПроцедуры
`)
	ent := &metadata.Entity{
		Name: "ЗаявкаСКопиями", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "СтадияОформления", Type: metadata.FieldTypeString},
			{Name: "Улица", Type: metadata.FieldTypeString},
		},
	}
	ent.Forms = []*metadata.FormModule{form}
	return ent
}

// managedPageAnchor — якорь data-ob-el элемента формы: то, что находит и правит
// applyElementStates.
type managedPageAnchor struct {
	Name            string `json:"name"`
	Fieldset        bool   `json:"fieldset"`
	ControlFieldset bool   `json:"controlFieldset"`
	Disabled        bool   `json:"disabled"`
	Hidden          bool   `json:"hidden"`
}

// managedPageControl — контрол формы в порядке DOM. Anchors — индексы
// якорей-предков, ближайший первым.
type managedPageControl struct {
	Tag      string            `json:"tag"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Value    string            `json:"value"`
	Checked  bool              `json:"checked"`
	Disabled bool              `json:"disabled"`
	Data     map[string]string `json:"data,omitempty"`
	Anchors  []int             `json:"anchors"`
}

type managedPage struct {
	Anchors  []managedPageAnchor  `json:"anchors"`
	Controls []managedPageControl `json:"controls"`
}

// parseManagedPage разбирает настоящую разметку формы: якоря элементов и
// контролы внутри #main-form в порядке DOM.
func parseManagedPage(t *testing.T, rendered string) managedPage {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatalf("parse managed form HTML: %v", err)
	}
	var form *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if form != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "form" {
			if id, _ := managedHTMLAttr(n, "id"); id == "main-form" {
				form = n
				return
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(doc)
	if form == nil {
		t.Fatal("managed form has no #main-form")
	}

	var page managedPage
	var walk func(n *html.Node, anchors []int)
	walk = func(n *html.Node, anchors []int) {
		if n.Type == html.ElementNode {
			if name, ok := managedHTMLAttr(n, "data-ob-el"); ok {
				_, disabled := managedHTMLAttr(n, "disabled")
				style, _ := managedHTMLAttr(n, "style")
				controlFieldset, _ := managedHTMLAttr(n, "data-ob-control-fieldset")
				page.Anchors = append(page.Anchors, managedPageAnchor{
					Name: name, Fieldset: n.Data == "fieldset", ControlFieldset: controlFieldset == "1",
					Disabled: disabled, Hidden: strings.Contains(strings.ReplaceAll(style, " ", ""), "display:none"),
				})
				anchors = append([]int{len(page.Anchors) - 1}, anchors...)
			}
			if control, ok := managedPageControlOf(n, anchors); ok {
				page.Controls = append(page.Controls, control)
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, anchors)
		}
	}
	walk(form, nil)
	return page
}

func managedPageControlOf(n *html.Node, anchors []int) (managedPageControl, bool) {
	if n.Data != "input" && n.Data != "select" && n.Data != "textarea" {
		return managedPageControl{}, false
	}
	name, _ := managedHTMLAttr(n, "name")
	if name == "" {
		return managedPageControl{}, false
	}
	control := managedPageControl{Tag: n.Data, Name: name, Anchors: anchors}
	_, control.Disabled = managedHTMLAttr(n, "disabled")
	switch n.Data {
	case "input":
		control.Type, _ = managedHTMLAttr(n, "type")
		if control.Type == "" {
			control.Type = "text"
		}
		switch control.Type {
		case "button", "submit", "reset", "image", "file":
			return managedPageControl{}, false
		}
		control.Value, _ = managedHTMLAttr(n, "value")
		_, control.Checked = managedHTMLAttr(n, "checked")
	case "textarea":
		control.Type = "textarea"
		var text strings.Builder
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.TextNode {
				text.WriteString(child.Data)
			}
		}
		control.Value = text.String()
	case "select":
		control.Type = "select-one"
		first := true
		for option := n.FirstChild; option != nil; option = option.NextSibling {
			if option.Type != html.ElementNode || option.Data != "option" {
				continue
			}
			value, _ := managedHTMLAttr(option, "value")
			if _, selected := managedHTMLAttr(option, "selected"); selected || first {
				control.Value = value
			}
			first = false
		}
	}
	for _, attr := range n.Attr {
		if strings.HasPrefix(attr.Key, "data-") {
			if control.Data == nil {
				control.Data = map[string]string{}
			}
			control.Data[attr.Key] = attr.Val
		}
	}
	return control, true
}

// managedPageHarness — модель браузера для одной страницы формы. Разметку она
// разбирает по правилам браузера (у радиокнопок с одним name отмечена одна, и
// из нескольких checked в разметке побеждает последняя), ответ события
// применяет кодом managed.js в порядке клиента и печатает успешные контролы —
// ровно то, что уйдёт при отправке формы.
const managedPageHarness = `
const fs = require('node:fs');
const source = fs.readFileSync(process.argv[1], 'utf8');
function region(name) {
  const begin = '// BEGIN ' + name;
  const end = '// END ' + name;
  const start = source.indexOf(begin);
  const stop = source.indexOf(end, start);
  if (start < 0 || stop < 0) throw new Error('managed.js has no region ' + name);
  return source.slice(start, stop + end.length);
}
function fn(name) {
  const start = source.indexOf('function ' + name + '(');
  if (start < 0) throw new Error('managed.js has no function ' + name);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}' && --depth === 0) return source.slice(start, i + 1);
  }
  throw new Error('unterminated function ' + name);
}
const payload = JSON.parse(fs.readFileSync(0, 'utf8'));

const anchors = payload.page.anchors.map((a) => ({
  tagName: a.fieldset ? 'FIELDSET' : 'DIV',
  name: a.name,
  disabled: a.disabled,
  style: {display: a.hidden ? 'none' : ''},
  members: [],
  getAttribute(attr) { return attr === 'data-ob-control-fieldset' && a.controlFieldset ? '1' : null; },
  querySelectorAll(selector) {
    if (selector === 'input, textarea') return this.members.filter((m) => m.tagName !== 'SELECT');
    if (selector === 'select, button:not([data-ob-ref-current])') return this.members.filter((m) => m.tagName === 'SELECT');
    throw new Error('anchor.querySelectorAll: unsupported selector ' + selector);
  },
}));

// Радиогруппа — все радиокнопки формы с одним name: отмечена не больше одной.
const radioGroups = new Map();
const controls = payload.page.controls.map((c) => {
  const dataset = {};
  for (const [key, value] of Object.entries(c.data || {})) {
    dataset[key.slice(5).replace(/-([a-z])/g, (_, ch) => ch.toUpperCase())] = value;
  }
  const node = {
    tagName: c.tag.toUpperCase(), name: c.name, type: c.type, value: c.value,
    disabled: c.disabled, readOnly: false, dataset,
    classList: {contains() { return false; }},
    ancestors: (c.anchors || []).map((i) => anchors[i]),
    closest(selector) {
      if (selector === '[data-ob-el]') return this.ancestors[0] || null;
      if (selector === '[data-ob-tp]') return null;
      if (selector === 'fieldset[disabled]') {
        return this.ancestors.find((a) => a.tagName === 'FIELDSET' && a.disabled) || null;
      }
      throw new Error('closest: unsupported selector ' + selector);
    },
  };
  if (c.type === 'radio') {
    Object.defineProperty(node, 'checked', {
      get() { return radioGroups.get(node.name) === node; },
      set(on) {
        if (on) radioGroups.set(node.name, node);
        else if (radioGroups.get(node.name) === node) radioGroups.delete(node.name);
      },
    });
    if (c.checked) node.checked = true;
  } else {
    node.checked = c.checked;
  }
  node.ancestors.forEach((a) => a.members.push(node));
  return node;
});

const form = {
  querySelector(selector) {
    if (selector.startsWith('[data-ob-file-content-for=')) return null;
    const byName = /^\[name="([^"]*)"\]$/.exec(selector);
    if (byName) return controls.find((c) => c.name === byName[1]) || null;
    throw new Error('form.querySelector: unsupported selector ' + selector);
  },
  querySelectorAll(selector) {
    const byName = /^\[name="([^"]*)"\]$/.exec(selector);
    if (byName) return controls.filter((c) => c.name === byName[1]);
    const radios = /^input\[type="radio"\]\[name="([^"]*)"\]$/.exec(selector);
    if (radios) return controls.filter((c) => c.type === 'radio' && c.name === radios[1]);
    throw new Error('form.querySelectorAll: unsupported selector ' + selector);
  },
};
global.window = {CSS: null};
// Конфиг страницы (ob-managed-config) и sessionStorage — для сохранения
// реквизитов формы перед отправкой и их восстановления после перезагрузки.
global.cfg = payload.config || {};
const storage = new Map(payload.storage == null ? [] : [['stash', payload.storage]]);
const stashKey = () => 'ob-form-attrs:' + String(global.cfg.entity || '');
global.sessionStorage = {
  getItem(k) { return k === stashKey() && storage.has('stash') ? storage.get('stash') : null; },
  setItem(k, v) { if (k === stashKey()) storage.set('stash', String(v)); },
  removeItem(k) { if (k === stashKey()) storage.delete('stash'); },
};
global.document = {
  getElementById(id) { return id === 'main-form' ? form : null; },
  querySelector(selector) {
    const match = /^\[data-ob-el="([^"]*)"\]$/.exec(selector);
    if (!match) throw new Error('document.querySelector: unsupported selector ' + selector);
    return anchors.find((a) => a.name === match[1]) || null;
  },
};
const client = new Function(
  fn('managedRefParts') + '\n' + fn('ensureRefOption') + '\n' +
    region('onebase-ro-apply-states') + '\n' + region('onebase-ro-apply-values') + '\n' +
    region('onebase-form-attr-stash') +
    '\nreturn {applyElementStates, applyValues, applyRadioValue, stashFormAttrs, restoreFormAttrs};'
)();
if (payload.response) {
  // Порядок клиента при ответе события (managed.js): состояния, затем значения.
  client.applyElementStates(payload.response.elementStates);
  client.applyValues(payload.response.values, payload.response.refOptions);
}
const submitted = (c) => !c.disabled && !c.closest('fieldset[disabled]');
// Действия оператора и клиента по порядку:
//   {setAll: {name: value}} — значение во всех копиях (как после ответа события);
//   {edit: {name: value}}   — оператор правит копию, которую видит;
//   {stash: true}           — «Записать»: реквизиты формы уходят в sessionStorage;
//   {restore: true}         — загрузка страницы после редиректа.
for (const action of payload.actions || []) {
  for (const [name, v] of Object.entries(action.setAll || {})) {
    const copies = controls.filter((c) => c.name === name);
    const radios = copies.filter((c) => c.type === 'radio');
    if (radios.length) client.applyRadioValue(radios, v);
    copies.forEach((c) => { if (c.type !== 'radio') c.value = v; });
  }
  for (const [name, v] of Object.entries(action.edit || {})) {
    const own = controls.filter((c) => c.name === name && submitted(c));
    if (!own.length) throw new Error('no visible copy of ' + name);
    if (own[0].type === 'radio') {
      const target = own.find((r) => r.value === v);
      if (!target) throw new Error('no option ' + v + ' in visible copy of ' + name);
      target.checked = true;
    } else {
      own[0].value = v;
    }
  }
  if (action.stash) client.stashFormAttrs();
  if (action.restore) client.restoreFormAttrs();
}
const values = {};
for (const c of controls) {
  if (!submitted(c)) continue;
  if ((c.type === 'checkbox' || c.type === 'radio') && !c.checked) continue;
  (values[c.name] = values[c.name] || []).push(String(c.value));
}
if (payload.actions) {
  process.stdout.write(JSON.stringify({values, storage: storage.has('stash') ? storage.get('stash') : null}));
} else {
  process.stdout.write(JSON.stringify(values));
}
`

// submitManagedPage возвращает то, что отправит форма: при открытии страницы
// (response == nil) или после ответа события, применённого кодом managed.js.
func submitManagedPage(t *testing.T, page managedPage, response json.RawMessage) url.Values {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed form submission test")
	}
	payload, err := json.Marshal(struct {
		Page     managedPage     `json:"page"`
		Response json.RawMessage `json:"response,omitempty"`
	}{Page: page, Response: response})
	if err != nil {
		t.Fatal(err)
	}
	var values url.Values
	runManagedPageHarness(t, node, payload, &values)
	return values
}

func runManagedPageHarness(t *testing.T, node string, payload []byte, out any) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), node, "-e", managedPageHarness, "static/managed.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execute managed.js page harness: %v\n%s", err, output)
	}
	if err := json.Unmarshal(output, out); err != nil {
		t.Fatalf("decode page harness output: %v; output=%s", err, output)
	}
}
