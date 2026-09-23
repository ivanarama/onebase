const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// План 182B (issue #1619): карточка виджета с refresh_on перечитывается по
// событиям шины через общий механизм живого списка — debounce склеивает пачку,
// скрытая вкладка копит dirty, reconnect перечитывает подписанные карточки.
const uiSource = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');

function block(beginMark, endMark) {
  const start = uiSource.indexOf(beginMark);
  const end = uiSource.indexOf(endMark, start);
  if (start < 0 || end < 0) throw new Error('source markers not found: ' + beginMark);
  return uiSource.slice(start, end + endMark.length);
}

const refreshSource = block('// BEGIN onebase-widget-refresh', '// END onebase-widget-refresh');
const liveSource = block('// BEGIN onebase-live-list', '// END onebase-live-list');

const DEBOUNCE_WAIT = 900;

function classList() {
  const values = new Set();
  return {
    add(value) { values.add(value); },
    remove(value) { values.delete(value); },
    contains(value) { return values.has(value); },
  };
}

function element(attrs) {
  const el = {
    attrs: new Map(),
    listeners: new Map(),
    disabled: false,
    textContent: '',
    innerHTML: 'old body',
    classList: classList(),
    getAttribute(name) { return el.attrs.has(name) ? el.attrs.get(name) : null; },
    setAttribute(name, value) { el.attrs.set(name, String(value)); },
    removeAttribute(name) { el.attrs.delete(name); },
    hasAttribute(name) { return el.attrs.has(name); },
    addEventListener(name, fn) { el.listeners.set(name, fn); },
  };
  for (const [k, v] of Object.entries(attrs || {})) el.attrs.set(k, v);
  return el;
}

// Карточка виджета в понимании контроллера слайса A: тело, кнопка, статус.
function widgetCard(name, refreshOn) {
  const body = element();
  const button = element();
  const status = element();
  const card = element({
    'data-ob-widget-card': '',
    'data-widget-name': name,
    'data-widget-url': '/ui/_widget/' + encodeURIComponent(name),
    'data-refresh-error': 'refresh failed',
  });
  if (refreshOn) {
    card.attrs.set('data-ob-refresh-on', refreshOn);
    card.attrs.set('data-ob-live', 'widget/' + name);
  }
  card.querySelector = function (selector) {
    if (selector === '[data-ob-widget-body]') return body;
    if (selector === '[data-ob-widget-refresh]') return button;
    if (selector === '[data-ob-widget-status]') return status;
    return null;
  };
  card._body = body;
  card._status = status;
  return card;
}

function response(payload) {
  return {
    ok: true,
    headers: { get() { return 'application/json; charset=utf-8'; } },
    json() { return Promise.resolve(payload); },
  };
}

function setup(cards) {
  const docListeners = new Map();
  const winListeners = new Map();
  const documentShim = {
    readyState: 'complete',
    visibilityState: 'visible',
    addEventListener(name, fn) { docListeners.set(name, fn); },
    querySelectorAll(selector) {
      const m = /^\[data-([a-z-]+)\]$/.exec(selector);
      if (!m) return [];
      return cards.filter((c) => c.attrs.has('data-' + m[1]));
    },
    querySelector() { return null; },
  };
  const fetchCalls = [];
  const sandbox = {
    setTimeout,
    clearTimeout,
    Promise,
    console,
    fetch(url) {
      fetchCalls.push(url);
      return Promise.resolve(response({ html: 'new body' }));
    },
  };
  sandbox.window = sandbox;
  sandbox.document = documentShim;
  sandbox.addEventListener = (name, fn) => winListeners.set(name, fn);
  sandbox.fetchCalls = fetchCalls;
  sandbox.__fire = function (name) {
    const fn = winListeners.get(name);
    if (fn) fn({ detail: {} });
  };
  sandbox.__fireDocument = function (name) {
    const fn = docListeners.get(name);
    if (fn) fn();
  };
  vm.createContext(sandbox);
  vm.runInContext(refreshSource, sandbox, { filename: 'widget-refresh.js' });
  vm.runInContext(liveSource, sandbox, { filename: 'live-list.js' });
  return sandbox;
}

const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

test('событие refresh_on перечитывает карточку через partial endpoint', async () => {
  const card = widgetCard('Продажи', 'данные.заказ');
  const app = setup([card]);
  app.__fire('onebase:данные.заказ');
  await wait(DEBOUNCE_WAIT);
  assert.deepEqual(app.fetchCalls, ['/ui/_widget/' + encodeURIComponent('Продажи')]);
  assert.equal(card._body.innerHTML, 'new body');
});

test('пачка событий склеивается в один запрос', async () => {
  const card = widgetCard('Продажи', 'данные.заказ данные.товар');
  const app = setup([card]);
  app.__fire('onebase:данные.заказ');
  app.__fire('onebase:данные.товар');
  app.__fire('onebase:данные.заказ');
  await wait(DEBOUNCE_WAIT);
  assert.equal(app.fetchCalls.length, 1);
});

test('скрытая вкладка копит dirty и перечитывает один раз при возврате', async () => {
  const card = widgetCard('Продажи', 'данные.заказ');
  const app = setup([card]);
  app.document.visibilityState = 'hidden';
  app.__fire('onebase:данные.заказ');
  await wait(DEBOUNCE_WAIT);
  assert.equal(app.fetchCalls.length, 0);
  app.document.visibilityState = 'visible';
  app.__fireDocument('visibilitychange');
  await wait(DEBOUNCE_WAIT);
  assert.equal(app.fetchCalls.length, 1);
});

test('SSE reconnect перечитывает только подписанные карточки', async () => {
  const subscribed = widgetCard('Продажи', 'данные.заказ');
  const plain = widgetCard('Выручка', null);
  const app = setup([subscribed, plain]);
  app.__fire('onebase:__oblive_refresh_all__');
  await wait(DEBOUNCE_WAIT);
  assert.deepEqual(app.fetchCalls, ['/ui/_widget/' + encodeURIComponent('Продажи')]);
});

test('неподписанная карточка не реагирует на чужое событие', async () => {
  const card = widgetCard('Продажи', null);
  const app = setup([card]);
  app.__fire('onebase:данные.заказ');
  await wait(DEBOUNCE_WAIT);
  assert.equal(app.fetchCalls.length, 0);
  assert.equal(card._body.innerHTML, 'old body');
});
