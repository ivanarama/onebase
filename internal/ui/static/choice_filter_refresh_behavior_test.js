const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/ui.js', 'utf8');
const start = source.indexOf('function obRefFilterValues(sel)');
const end = source.indexOf('function openRefPicker(selOrId)', start);
if (start < 0 || end < 0) throw new Error('choice_filter refresh block is absent from ui.js');
const refreshSource = source.slice(start, end);

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return {promise, resolve, reject};
}

function response(data) {
  return {ok: true, status: 200, json: async () => data};
}

function runtime(fetchImpl, selected) {
  const listeners = {};
  const sourceControl = {value: 'warehouse-a'};
  const ownerControl = {value: 'owner-a'};
  const attrs = {
    'data-ref-choice-context': JSON.stringify({
      form_entity: 'Инвентаризация',
      form: 'ФормаОбъекта',
      element: 'inventory-storage-choice',
      sources: {'Объект.Склад': 'Склад'},
    }),
    'data-ref-filter': JSON.stringify({
      'Владелец': {from: 'Контрагент', value: 'owner-a'},
    }),
    'data-ref-entity': 'МестоХранения',
  };
  const select = {
    options: [
      {value: '', textContent: '— выбрать —'},
      ...(selected ? [{value: selected, textContent: 'old location'}] : []),
    ],
    _value: selected || '',
    changeCount: 0,
    isConnected: true,
    form: {elements: {namedItem(name) { return name === 'Склад' ? sourceControl : null; }}},
    get value() { return this._value; },
    set value(value) {
      const text = String(value == null ? '' : value);
      this._value = this.options.some((option) => String(option.value) === text) ? text : '';
    },
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    setAttribute(name, value) { attrs[name] = String(value); },
    removeAttribute(name) { delete attrs[name]; },
    appendChild(option) { this.options.push(option); return option; },
    remove(index) { this.options.splice(index, 1); },
    dispatchEvent(event) {
      this.changeCount++;
      if (listeners.change) listeners.change(event);
      return true;
    },
  };
  const document = {
    documentElement: {contains() { return true; }},
    querySelectorAll(selector) {
      return selector === 'select[data-ref-choice-context],select[data-ref-filter]' ? [select] : [];
    },
    querySelector(selector) {
      if (selector === '[name="Контрагент"]') return ownerControl;
      if (selector === '[name="Склад"]') return sourceControl;
      return null;
    },
    getElementsByName(name) { return name === 'Склад' ? [sourceControl] : []; },
    getElementById() { return null; },
    createElement(tag) { return {tagName: String(tag).toUpperCase(), value: '', textContent: ''}; },
    createEvent() { return {initEvent() {}}; },
    addEventListener(name, listener) { listeners[name] = listener; },
  };
  let ready;
  const window = {fetch: fetchImpl, AbortController: globalThis.AbortController};
  const sandbox = {
    window,
    document,
    Event: class Event { constructor(type, options) { this.type = type; this.bubbles = !!(options && options.bubbles); } },
    AbortController: globalThis.AbortController,
    Promise,
    JSON,
    Array,
    Object,
    String,
    Error,
    encodeURIComponent,
    obReady(callback) { ready = callback; },
  };
  vm.createContext(sandbox);
  vm.runInContext(refreshSource, sandbox, {filename: 'ui.js#choice-filter-refresh'});
  ready();
  return {api: sandbox, attrs, select, sourceControl, ownerControl};
}

test('owner and choice_filter share one sequenced A-B-A request', async () => {
  const first = deferred();
  const second = deferred();
  const third = deferred();
  const calls = [];
  const env = runtime((url) => {
    calls.push(url);
    return [first.promise, second.promise, third.promise][calls.length - 1];
  });

  const oldARequest = env.api.obRefreshChoiceSelect(env.select, true);
  env.sourceControl.value = 'warehouse-b';
  env.ownerControl.value = 'owner-b';
  const bRequest = env.api.obRefreshChoiceSelect(env.select, false);
  second.resolve(response({items: [{id: 'b-location', _label: 'B location'}], total: 1}));
  await bRequest;
  env.sourceControl.value = 'warehouse-a';
  env.ownerControl.value = 'owner-a';
  const latestARequest = env.api.obRefreshChoiceSelect(env.select, false);
  third.resolve(response({items: [{id: 'latest-a-location', _label: 'Latest A location'}], total: 1}));
  await latestARequest;
  first.resolve(response({items: [{id: 'a-location', _label: 'A location'}], total: 1}));
  await oldARequest;

  assert.equal(calls.length, 3);
  calls.forEach((url) => {
    assert.match(url, /form_entity=/);
    assert.match(url, /sources=/);
    assert.match(url, /flt=/);
  });
  assert.match(decodeURIComponent(calls[1]), /warehouse-b/);
  assert.match(decodeURIComponent(calls[1]), /owner-b/);
  assert.match(decodeURIComponent(calls[2]), /warehouse-a/);
  assert.match(decodeURIComponent(calls[2]), /owner-a/);
  assert.deepEqual(env.select.options.map((option) => option.value), ['', 'latest-a-location']);
  assert.equal(env.select.options.some((option) => option.value === 'a-location'), false);
  assert.equal(env.select.options.some((option) => option.value === 'b-location'), false);
});

test('server selected_allowed keeps a value outside the page and clears only an incompatible value', async () => {
  const replies = [
    response({items: [], total: 51, selected_allowed: true}),
    response({items: [], total: 0, selected_allowed: false}),
  ];
  const env = runtime(async () => replies.shift(), 'legacy-location');

  env.sourceControl.value = 'warehouse-b';
  await env.api.obRefreshChoiceSelect(env.select, false);
  assert.equal(env.select.value, 'legacy-location');
  assert.equal(env.select.options.some((option) => option.value === 'legacy-location'), true);
  assert.equal(env.select.changeCount, 0);

  env.sourceControl.value = 'warehouse-c';
  await env.api.obRefreshChoiceSelect(env.select, false);
  assert.equal(env.select.value, '');
  assert.equal(env.select.options.some((option) => option.value === 'legacy-location'), false);
  assert.equal(env.select.changeCount, 1);
});

test('network failure is visible and preserves the previously filtered options and value', async () => {
  const env = runtime(async () => { throw new Error('offline'); }, 'legacy-location');
  const before = env.select.options.map((option) => [option.value, option.textContent]);

  env.sourceControl.value = 'warehouse-b';
  await env.api.obRefreshChoiceSelect(env.select, false);

  assert.equal(env.select.value, 'legacy-location');
  assert.deepEqual(env.select.options.map((option) => [option.value, option.textContent]), before);
  assert.equal(env.attrs['data-ob-choice-error'], '1');
  assert.equal(env.attrs['data-ob-choice-loading'], undefined);
});
