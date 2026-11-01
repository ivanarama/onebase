// Enter в форме переводит фокус к следующему полю, а не отправляет форму
// (#1486). Регрессия здесь не видна глазами: форма выглядит одинаково, просто
// начинает записываться от случайного Enter в середине ввода — ровно то, на что
// жаловался автор заявки.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');

const source = fs.readFileSync('static/ui.js', 'utf8');

function extract(name) {
  const start = source.indexOf('function ' + name);
  if (start < 0) throw new Error('в ui.js нет функции ' + name);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') {
      depth--;
      if (depth === 0) return source.slice(start, i + 1);
    }
  }
  throw new Error('не закрыта функция ' + name);
}

// Узел ровно того объёма, который трогает навигация: разметка, атрибуты,
// классы и фокус. Настоящего DOM нет намеренно — проверяем поведение функции.
function node(tag, props) {
  const el = Object.assign({
    nodeType: 1,
    tagName: String(tag || '').toUpperCase(),
    children: [],
    parent: null,
    attrs: {},
    className: '',
    type: '',
    value: '',
    disabled: false,
    readOnly: false,
    tabIndex: 0,
    hidden: false,
    isContentEditable: false,
    focused: false,
    selected: false,
    style: {display: '', visibility: ''},
    appendChild(child) { this.children.push(child); child.parent = this; return child; },
    setAttribute(name, value) { this.attrs[name] = String(value); },
    getAttribute(name) {
      return Object.prototype.hasOwnProperty.call(this.attrs, name) ? this.attrs[name] : null;
    },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attrs, name); },
    focus() { this.focused = true; },
    select() { this.selected = true; },
    get classList() {
      const self = this;
      return {contains(name) { return String(self.className || '').split(/\s+/).includes(name); }};
    },
    closest(sel) {
      for (let cur = this; cur; cur = cur.parent) if (matches(cur, sel)) return cur;
      return null;
    },
    querySelectorAll(sel) {
      const parts = sel.split(',').map((s) => s.trim());
      const out = [];
      walk(this, (el) => { if (parts.some((p) => matches(el, p))) out.push(el); });
      return out;
    },
  }, props || {});
  return el;
}

// Селекторы ровно тех видов, что встречаются в obEnterStops/obEnterNavigateFrom.
function matches(el, sel) {
  if (sel === 'form') return el.tagName === 'FORM';
  if (sel === '.ob-grid[data-sg-tp]') {
    return String(el.className || '').split(/\s+/).includes('ob-grid') && el.getAttribute('data-sg-tp') !== null;
  }
  return el.tagName === sel.toUpperCase();
}

function walk(el, visit) {
  el.children.forEach((child) => { visit(child); walk(child, visit); });
}

function api(extras) {
  const window = Object.assign({}, extras || {});
  return new Function(
    'window', 'obElementVisible',
    extract('obEnterOwnedByElement') + '\n' +
      extract('obEnterFieldUsable') + '\n' +
      extract('obEnterStops') + '\n' +
      extract('obEnterNavigateFrom') + '\n' +
      'return {navigate: obEnterNavigateFrom, stops: obEnterStops, owned: obEnterOwnedByElement};',
  )(window, (el) => !el.hidden);
}

function form(props) {
  return node('form', Object.assign({id: 'main-form'}, props || {}));
}

test('Enter переводит фокус к следующему полю, а не отправляет форму', () => {
  const f = form();
  const first = f.appendChild(node('input', {type: 'text'}));
  const second = f.appendChild(node('input', {type: 'text'}));
  assert.equal(api().navigate(first), true, 'нажатие не обработано — форма отправится');
  assert.equal(second.focused, true, 'фокус не перешёл на следующее поле');
  assert.equal(second.selected, true, 'значение следующего поля не выделено');
});

test('скрытые, readonly и disabled поля пропускаются', () => {
  const f = form();
  const first = f.appendChild(node('input', {type: 'text'}));
  f.appendChild(node('input', {type: 'hidden'}));
  f.appendChild(node('input', {type: 'text', readOnly: true}));
  f.appendChild(node('input', {type: 'text', disabled: true}));
  f.appendChild(node('input', {type: 'text', hidden: true}));
  const reachable = f.appendChild(node('select'));
  api().navigate(first);
  assert.equal(reachable.focused, true, 'переход остановился на недоступном поле');
});

test('на последнем поле фокус остаётся на месте, но форма не отправляется', () => {
  const f = form();
  const only = f.appendChild(node('input', {type: 'text'}));
  assert.equal(api().navigate(only), true, 'последнее поле отправило форму');
  assert.equal(only.focused, false, 'фокус зачем-то сместился');
});

test('enter_submits_form возвращает прежнее поведение', () => {
  const f = form();
  f.setAttribute('data-ob-enter-submits', '1');
  const first = f.appendChild(node('input', {type: 'text'}));
  const second = f.appendChild(node('input', {type: 'text'}));
  assert.equal(api().navigate(first), false, 'форма с флагом обязана отправляться по Enter');
  assert.equal(second.focused, false);
});

test('textarea и кнопки оставляют Enter себе', () => {
  const nav = api();
  const f = form();
  const area = f.appendChild(node('textarea'));
  const button = f.appendChild(node('button'));
  const submit = f.appendChild(node('input', {type: 'submit'}));
  f.appendChild(node('input', {type: 'text'}));
  for (const el of [area, button, submit]) {
    assert.equal(nav.navigate(el), false, el.tagName + '/' + el.type + ': Enter перехвачен');
  }
});

test('поиск в шапке и прочие формы отправляются по Enter как раньше', () => {
  const search = node('form', {id: ''});
  search.className = 'topbar-search';
  const q = search.appendChild(node('input', {type: 'text'}));
  search.appendChild(node('input', {type: 'text'}));
  assert.equal(api().navigate(q), false, 'Enter в поиске перехвачен навигацией');
});

test('из последнего поля шапки фокус уходит в табличную часть', () => {
  const f = form();
  const head = f.appendChild(node('input', {type: 'text'}));
  const grid = f.appendChild(node('div', {className: 'ob-grid'}));
  grid.setAttribute('data-sg-tp', 'Строки');
  // Редактор ячейки грида не является отдельной остановкой маршрута.
  grid.appendChild(node('input', {type: 'text'}));
  let asked = null;
  const nav = api({obGridFocusFirstCell(host) { asked = host; return true; }});
  assert.equal(nav.navigate(head), true);
  assert.equal(asked, grid, 'фокус не ушёл в табличную часть');
});

test('внутри табличной части Enter обслуживает сам грид', () => {
  const f = form();
  const grid = f.appendChild(node('div', {className: 'ob-grid'}));
  grid.setAttribute('data-sg-tp', 'Строки');
  const editor = grid.appendChild(node('input', {type: 'text'}));
  const nav = api({obGridFocusFirstCell() { throw new Error('грид не должен просить фокус у самого себя'); }});
  assert.equal(nav.navigate(editor), false, 'ui.js перехватил Enter внутри грида');
});

test('пустая табличная часть не съедает переход', () => {
  const f = form();
  const head = f.appendChild(node('input', {type: 'text'}));
  const grid = f.appendChild(node('div', {className: 'ob-grid'}));
  grid.setAttribute('data-sg-tp', 'Строки');
  const after = f.appendChild(node('input', {type: 'text'}));
  const nav = api({obGridFocusFirstCell() { return false; }});
  assert.equal(nav.navigate(head), true);
  assert.equal(after.focused, true, 'после пустой ТЧ переход не продолжился');
});
