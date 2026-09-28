const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// План 182C (issue #1617): строка list-виджета с data-ob-row-url открывает
// карточку записи по клику и Enter; выделение текста и ссылки/кнопки внутри
// ячеек навигацию не перехватывают.
const uiSource = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const begin = uiSource.indexOf('// BEGIN onebase-widget-row-nav');
const end = uiSource.indexOf('// END onebase-widget-row-nav', begin);
if (begin < 0 || end < 0) throw new Error('widget row nav source markers not found');
const navSource = uiSource.slice(begin, end);

const NAV_SELECTOR = '[data-ob-row-url]';
const CELL_TAGS = 'a, button, select, input, textarea, label';

function element(tag, attrs, parent) {
  const el = {
    tag,
    parent: parent || null,
    attrs: new Map(),
    listeners: new Map(),
    textContent: '',
  };
  for (const [k, v] of Object.entries(attrs || {})) el.attrs.set(k, String(v));
  el.getAttribute = (name) => (el.attrs.has(name) ? el.attrs.get(name) : null);
  el.hasAttribute = (name) => el.attrs.has(name);
  el.addEventListener = (name, fn) => el.listeners.set(name, fn);
  el.matches = (selector) => {
    if (selector === NAV_SELECTOR) return el.attrs.has('data-ob-row-url');
    if (selector === CELL_TAGS) return CELL_TAGS.split(', ').includes(el.tag);
    return false;
  };
  el.closest = (selector) => {
    let cur = el;
    while (cur) {
      if (cur.matches(selector)) return cur;
      cur = cur.parent;
    }
    return null;
  };
  return el;
}

function setup() {
  const docHandlers = new Map();
  const navigated = [];
  const sandbox = {
    document: { addEventListener: (name, fn) => docHandlers.set(name, fn) },
    location: {
      assign(url) { navigated.push(url); },
    },
  };
  sandbox.window = sandbox;
  let selection = '';
  sandbox.getSelection = () => selection;
  sandbox.__setSelection = (value) => { selection = value; };
  sandbox.__fire = function (name, ev) {
    const fn = docHandlers.get(name);
    if (fn) fn(ev);
  };
  sandbox.__navigated = navigated;
  vm.createContext(sandbox);
  vm.runInContext(navSource, sandbox, { filename: 'widget-row-nav.js' });
  return sandbox;
}

const cardURL = '/ui/catalog/Товар/79f4d98a-3ce0-4da4-82ea-e9c8686e804f';

function buildRow(withLinkCell) {
  const row = element('tr', { 'data-ob-row-url': cardURL });
  const cell = element('td', {}, row);
  const inner = withLinkCell ? element('a', { href: '#' }, cell) : element('span', {}, cell);
  return { row, cell, inner };
}

test('клик по строке открывает карточку записи', () => {
  const app = setup();
  const { row } = buildRow(false);
  app.__fire('click', { target: row });
  assert.deepEqual(app.__navigated, [cardURL]);
});

test('клик по ссылке внутри ячейки не навигирует', () => {
  const app = setup();
  const { inner } = buildRow(true);
  app.__fire('click', { target: inner });
  assert.deepEqual(app.__navigated, []);
});

test('клик при выделенном тексте не навигирует', () => {
  const app = setup();
  const { cell } = buildRow(false);
  app.__setSelection('Гвозди');
  app.__fire('click', { target: cell });
  assert.deepEqual(app.__navigated, []);
});

test('Enter на строке в фокусе открывает карточку', () => {
  const app = setup();
  const { row } = buildRow(false);
  let prevented = false;
  app.__fire('keydown', { target: row, key: 'Enter', preventDefault: () => { prevented = true; } });
  assert.deepEqual(app.__navigated, [cardURL]);
  assert.equal(prevented, true);
});

test('Enter внутри ячейки-инпута не навигирует', () => {
  const app = setup();
  const row = element('tr', { 'data-ob-row-url': cardURL });
  const cell = element('td', {}, row);
  const input = element('input', {}, cell);
  app.__fire('keydown', { target: input, key: 'Enter', preventDefault: () => {} });
  assert.deepEqual(app.__navigated, []);
});

test('клик вне строки виджета не навигирует', () => {
  const app = setup();
  const elsewhere = element('div', {});
  app.__fire('click', { target: elsewhere });
  assert.deepEqual(app.__navigated, []);
});
