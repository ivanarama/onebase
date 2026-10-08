const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// План 182D (#1620): контрол фильтра пересобирает адрес карточки из её
// собственных ключей (чужие не утекают — сервер отвергает их), синхронизирует
// адрес строки с сохранением ключей других карточек, перечитывает карточку;
// сброс очищает; popstate восстанавливает значения из адреса.
const uiSource = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const begin = uiSource.indexOf('// BEGIN onebase-widget-filters');
const end = uiSource.indexOf('// END onebase-widget-filters', begin);
if (begin < 0 || end < 0) throw new Error('widget filters source markers not found');
const filtersSource = uiSource.slice(begin, end);

function element(tag, attrs, parent) {
  const el = {
    tag,
    parent: parent || null,
    attrs: new Map(),
    value: '',
    listeners: new Map(),
    setAttribute(name, v) { el.attrs.set(name, String(v)); },
    getAttribute(name) { return el.attrs.has(name) ? el.attrs.get(name) : null; },
    hasAttribute(name) { return el.attrs.has(name); },
  };
  for (const [k, v] of Object.entries(attrs || {})) el.attrs.set(k, String(v));
  el.matches = (sel) => {
    if (sel === '[data-ob-widget-card]') return el.attrs.has('data-ob-widget-card');
    if (sel === '[data-ob-filter-reset]') return el.attrs.has('data-ob-filter-reset');
    return false;
  };
  el.closest = (sel) => {
    let cur = el;
    while (cur) {
      if (cur.matches(sel)) return cur;
      cur = cur.parent;
    }
    return null;
  };
  return el;
}

const KEY_A = 'w.QQ.0Vj';   // ключ фильтра карточки A (проверка: любые строки)
const KEY_B = 'w.88.0Vj';

function setup(initialSearch) {
  const docHandlers = new Map();
  const winHandlers = new Map();
  const refreshed = [];
  const cardA = element('div', { 'data-ob-widget-card': '', 'data-widget-url': '/ui/_widget/A' });
  const selectA = element('select', { 'data-ob-filter': KEY_A }, cardA);
  const cardB = element('div', { 'data-ob-widget-card': '', 'data-widget-url': '/ui/_widget/B?' + KEY_B + '=100' });
  element('select', { 'data-ob-filter': KEY_B }, cardB);

  const all = [cardA, selectA, cardB];
  function isDescendant(node, ancestor) {
    let cur = node;
    while (cur) {
      if (cur === ancestor) return true;
      cur = cur.parent;
    }
    return false;
  }
  cardA.querySelectorAll = (sel) => (sel === '[data-ob-filter]' ? all.filter((n) => n.hasAttribute('data-ob-filter') && isDescendant(n, cardA)) : []);
  cardB.querySelectorAll = (sel) => (sel === '[data-ob-filter]' ? all.filter((n) => n.hasAttribute('data-ob-filter') && isDescendant(n, cardB)) : []);
  const sandbox = {
    URLSearchParams,
    document: {
      addEventListener: (n, f) => docHandlers.set(n, f),
      querySelectorAll(sel) {
        if (sel === '[data-ob-widget-card]') return [cardA, cardB];
        return [];
      },
    },
    location: { search: initialSearch, pathname: '/ui/' },
  };
  sandbox.window = sandbox;
  sandbox.history = {
    pushState(_, __, url) { sandbox.location = { search: url.startsWith('?') ? url.slice(1) : '', pathname: url }; },
  };
  sandbox.addEventListener = (n, f) => winHandlers.set(n, f);
  sandbox.obRefreshWidgetCard = (card) => refreshed.push(card);
  sandbox.__fire = (n, ev) => docHandlers.get(n)(ev);
  sandbox.__fireWindow = (n) => winHandlers.get(n)();
  sandbox.__cardA = cardA;
  sandbox.__cardB = cardB;
  sandbox.__selectA = selectA;
  sandbox.__refreshed = refreshed;
  vm.createContext(sandbox);
  vm.runInContext(filtersSource, sandbox, { filename: 'widget-filters.js' });
  return sandbox;
}

test('изменение фильтра обновляет адрес карточки и перечитывает её', () => {
  const app = setup('');
  app.__selectA.value = 'Гвозди';
  app.__fire('change', { target: app.__selectA });
  assert.equal(app.__cardA.getAttribute('data-widget-url'), '/ui/_widget/A?' + KEY_A + '=' + encodeURIComponent('Гвозди'));
  assert.deepEqual(app.__refreshed, [app.__cardA]);
  // Адрес строки получил ключ карточки A.
  assert.ok(app.location.search.includes(KEY_A));
});

test('пустое значение исчезает из адреса карточки', () => {
  const app = setup('');
  app.__selectA.value = 'x';
  app.__fire('change', { target: app.__selectA });
  app.__selectA.value = '';
  app.__fire('change', { target: app.__selectA });
  assert.equal(app.__cardA.getAttribute('data-widget-url'), '/ui/_widget/A');
});

test('адрес строки сохраняет чужие ключи, адрес карточки — нет', () => {
  const app = setup('?' + KEY_B + '=100');
  app.__selectA.value = 'z';
  app.__fire('change', { target: app.__selectA });
  // Карточка A получила только свой ключ.
  const urlA = app.__cardA.getAttribute('data-widget-url');
  assert.ok(urlA.includes(KEY_A));
  assert.ok(!urlA.includes(KEY_B));
  // Адрес строки — оба ключа.
  assert.ok(app.location.search.includes(KEY_A) && app.location.search.includes(KEY_B));
});

test('сброс очищает контролы карточки', () => {
  const app = setup('');
  app.__selectA.value = 'y';
  app.__fire('change', { target: app.__selectA });
  const reset = element('button', { 'data-ob-filter-reset': '' }, app.__cardA);
  app.__fire('click', { target: reset });
  assert.equal(app.__selectA.value, '');
  assert.equal(app.__cardA.getAttribute('data-widget-url'), '/ui/_widget/A');
});

test('popstate восстанавливает значение контрола из адреса', () => {
  const app = setup('?' + KEY_A + '=abc');
  app.__fireWindow('popstate');
  assert.equal(app.__selectA.value, 'abc');
  assert.equal(app.__cardA.getAttribute('data-widget-url'), '/ui/_widget/A?' + KEY_A + '=abc');
});
