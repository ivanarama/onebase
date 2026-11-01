const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('function obManagedNotifyListener(cfg, fire)');
const end = source.indexOf('obManagedReady(function', start);
if (start < 0 || end < 0) throw new Error('obManagedNotifyListener is absent from managed.js');
const ctx = vm.createContext({});
vm.runInContext(source.slice(start, end) + '\nthis.listener = obManagedNotifyListener;', ctx);

// Оповестить на сервере → SSE ui.оповестить {id, событие}: форма с
// обработчиком ОбработкаОповещения отправляет обычное событие формы уровня
// формы (без элемента) с идентификатором оповещения.
test('form with notify handler fires ОбработкаОповещения with the notification id', () => {
  const fired = [];
  const listener = ctx.listener({notify: true}, (...args) => fired.push(args));
  listener({detail: {id: 'abc123', 'событие': 'ВозвратВФормуЗвонка'}});
  assert.equal(fired.length, 1);
  assert.equal(fired[0][0], '');
  assert.equal(fired[0][1], 'ОбработкаОповещения');
  assert.equal(fired[0][2]._notify_id, 'abc123');
});

test('form without notify handler and malformed events fire nothing', () => {
  const fired = [];
  ctx.listener({notify: false}, (...args) => fired.push(args))({detail: {id: 'abc'}});
  const listener = ctx.listener({notify: true}, (...args) => fired.push(args));
  listener({detail: null});
  listener({detail: {}});
  listener({});
  assert.equal(fired.length, 0);
});

// Execute the production transport from EventSource through postMessage to the
// managed listener. Each document owns only its immediate iframe elements.
const ui = fs.readFileSync('static/ui.js', 'utf8');
const transportStart = ui.indexOf('/* План 74: real-time-шина');
const transportEnd = ui.indexOf('// BEGIN onebase-live-list', transportStart);
assert.ok(transportStart >= 0 && transportEnd > transportStart, 'realtime transport is absent');
const transport = ui.slice(transportStart, transportEnd);

function page(parent = null, notify = true) {
  const listeners = new Map();
  const frames = [];
  const fired = [];
  const document = {
    readyState: 'complete',
    body: {getAttribute() { return null; }},
    querySelectorAll(selector) {
      const selectors = selector.split(',').map(value => value.trim());
      return frames.filter(frame => selectors.includes(frame.selector));
    },
  };
  const window = {
    __obEmbedded: !!parent,
    location: {origin: 'https://onebase.test'},
    addEventListener(name, fn) {
      if (!listeners.has(name)) listeners.set(name, []);
      listeners.get(name).push(fn);
    },
    dispatchEvent(event) {
      for (const fn of listeners.get(event.type) || []) fn(event);
    },
    postMessage(data, origin) {
      assert.equal(origin, window.location.origin, 'relay must constrain its target origin');
      window.dispatchEvent({type: 'message', data, source: parent, origin: parent.location.origin});
    },
  };
  window.parent = parent || window;
  function CustomEvent(type, options) { this.type = type; this.detail = options.detail; }
  window.CustomEvent = CustomEvent;
  function EventSource(url) { assert.equal(url, '/ui/events'); }
  vm.runInNewContext(transport, {window, document, CustomEvent, EventSource});
  window.addEventListener('onebase:ui.оповестить', ctx.listener({notify}, (...args) => fired.push(args)));
  return {
    window, fired,
    child(selector, notify = true) {
      const child = page(window, notify);
      frames.push({selector, contentWindow: child.window});
      return child;
    },
    publish(data = {id: 'notification-1'}) {
      window.__obEvents.onmessage({data: JSON.stringify({name: 'ui.оповестить', data})});
    },
    message(data, origin = window.location.origin, source = window.parent) {
      window.dispatchEvent({type: 'message', data, origin, source});
    },
  };
}

test('SSE reaches a root reference-create form exactly once', () => {
  const root = page();
  const modal = root.child('#_ref-create-modal iframe');
  const unrelated = root.child('#unrelated iframe');
  root.publish();
  assert.equal(root.fired.length, 1);
  assert.equal(modal.fired.length, 1);
  assert.equal(modal.fired[0][2]._notify_id, 'notification-1');
  assert.equal(unrelated.fired.length, 0);
  assert.equal(modal.window.__obEvents, undefined, 'embedded forms must not open SSE');
});

test('SSE reaches tab and nested reference-create forms without duplicates', () => {
  const root = page();
  const tab = root.child('.ob-tabbody iframe');
  const modal = tab.child('#_ref-create-modal iframe');
  const nested = modal.child('#_ref-create-modal iframe');
  const disabled = tab.child('#_ref-create-modal iframe', false);
  root.publish();
  for (const form of [root, tab, modal, nested]) {
    assert.equal(form.fired.length, 1);
    assert.equal(form.fired[0][2]._notify_id, 'notification-1');
  }
  assert.equal(disabled.fired.length, 0);
  assert.equal(tab.window.__obEvents, undefined);
  root.publish({});
  for (const form of [root, tab, modal, nested]) assert.equal(form.fired.length, 1);
});

test('invalid message source, origin and envelope never reach nested forms', () => {
  const root = page();
  const tab = root.child('.ob-tabbody iframe');
  const modal = tab.child('#_ref-create-modal iframe');
  const valid = {source: 'obRealtime', name: 'ui.оповестить', data: {id: 'notification-2'}};
  tab.message(valid, 'https://untrusted.test');
  tab.message(valid, undefined, {});
  for (const data of [null, {}, {...valid, source: 'other'}, {...valid, name: ''}, {...valid, name: 7}]) {
    tab.message(data);
  }
  assert.equal(tab.fired.length, 0);
  assert.equal(modal.fired.length, 0);
  tab.message(valid);
  assert.equal(tab.fired.length, 1);
  assert.equal(modal.fired.length, 1);
});
