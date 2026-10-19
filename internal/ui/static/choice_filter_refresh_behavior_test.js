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

function runtime(fetchImpl, selected, withOwner, withChoice = true, collapsed = false) {
  const listeners = {};
  const sourceControl = {value: 'warehouse-a'};
  const sourceControls = [sourceControl];
  const ownerControl = {name: 'Контрагент', value: 'contractor-a'};
  const attrs = {
    'data-ref-entity': 'МестоХранения',
  };
  if (withChoice) attrs['data-ref-choice-context'] = JSON.stringify({
    form_entity: 'Инвентаризация',
    form: 'ФормаОбъекта',
    element: 'inventory-storage-choice',
    sources: {'Объект.Склад': 'Склад'},
  });
  if (withOwner) attrs['data-ref-filter'] = JSON.stringify({Владелец: {from: 'Контрагент', value: ownerControl.value}});
  if (collapsed) attrs['data-ref-choice-dropdown'] = 'false';
  const select = {
    options: [
      {value: '', textContent: '— выбрать —'},
      ...(selected ? [{value: selected, textContent: 'old location'}] : []),
    ],
    _value: selected || '',
    changeCount: 0,
    isConnected: true,
    form: {elements: {namedItem(name) { return name === 'Склад' ? (sourceControls.length === 1 ? sourceControls[0] : sourceControls) : null; }}},
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
      if (selector === 'select[data-ref-choice-context]') return withChoice ? [select] : [];
      if (selector === 'select[data-ref-filter]') return withOwner ? [select] : [];
      return [];
    },
    getElementsByName(name) { return name === 'Склад' ? [sourceControl] : name === 'Контрагент' ? [ownerControl] : []; },
    querySelector(selector) { return selector.includes('Контрагент') ? ownerControl : null; },
    getElementById() { return null; },
    createElement(tag) { return {tagName: String(tag).toUpperCase(), value: '', textContent: ''}; },
    createEvent() { return {initEvent() {}}; },
    addEventListener(name, listener) { listeners[name] = listener; },
  };
  let ready;
  const window = {fetch: fetchImpl, AbortController: globalThis.AbortController};
  const sandbox = {
    window,
    fetch: fetchImpl,
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
  return {api: sandbox, attrs, select, sourceControl, sourceControls, ownerControl};
}

test('owner-only refresh ignores a late A response after B has been selected', async () => {
  const a = deferred();
  const b = deferred();
  const calls = [];
  const env = runtime((url) => {
    calls.push(url);
    return [a, b][calls.length - 1].promise;
  }, '', true, false);

  env.api.obRefreshDependentSelects(env.ownerControl);
  env.ownerControl.value = 'contractor-b';
  env.api.obRefreshDependentSelects(env.ownerControl);
  b.resolve(response({items: [{id: 'b-location', _label: 'B'}]}));
  await new Promise(setImmediate);
  assert.deepEqual(env.select.options.map((option) => option.value), ['', 'b-location']);

  a.resolve(response({items: [{id: 'a-location', _label: 'A'}]}));
  await new Promise(setImmediate);
  assert.equal(calls.length, 2);
  assert.match(calls[0], /contractor-a/);
  assert.match(calls[1], /contractor-b/);
  assert.deepEqual(env.select.options.map((option) => option.value), ['', 'b-location']);
});

test('owner-only refresh ignores the first A response after A→B→A', async () => {
  const a1 = deferred();
  const b = deferred();
  const a2 = deferred();
  const calls = [];
  const env = runtime((url) => {
    calls.push(url);
    return [a1, b, a2][calls.length - 1].promise;
  }, '', true, false);

  env.api.obRefreshDependentSelects(env.ownerControl);
  env.ownerControl.value = 'contractor-b';
  env.api.obRefreshDependentSelects(env.ownerControl);
  env.ownerControl.value = 'contractor-a';
  env.api.obRefreshDependentSelects(env.ownerControl);
  a2.resolve(response({items: [{id: 'new-a', _label: 'New A'}]}));
  await new Promise(setImmediate);
  a1.resolve(response({items: [{id: 'old-a', _label: 'Old A'}]}));
  await new Promise(setImmediate);
  b.resolve(response({items: [{id: 'b-location', _label: 'B'}]}));
  await new Promise(setImmediate);
  assert.equal(calls.length, 3);
  assert.deepEqual(env.select.options.map((option) => option.value), ['', 'new-a']);
});

test('owner and choice refresh stay together through A→B→A responses', async () => {
  const a1 = deferred();
  const b = deferred();
  const a2 = deferred();
  const calls = [];
  const env = runtime((url) => {
    calls.push(url);
    return [a1, b, a2][calls.length - 1].promise;
  }, '', true);
  const first = env.api.obRefreshChoiceSelect(env.select, true);
  env.ownerControl.value = 'contractor-b';
  const second = env.api.obRefreshChoiceSelect(env.select, false);
  b.resolve(response({items: [{id: 'b-location', _label: 'B'}], total: 1}));
  await second;
  env.ownerControl.value = 'contractor-a';
  const third = env.api.obRefreshChoiceSelect(env.select, false);
  a2.resolve(response({items: [{id: 'latest-a', _label: 'Latest A'}], total: 1}));
  await third;
  a1.resolve(response({items: [{id: 'stale-a', _label: 'Stale A'}], total: 1}));
  await first;
  assert.equal(calls.length, 3);
  for (const url of calls) {
    assert.match(url, /sources=/);
    assert.match(url, /flt=/);
  }
  assert.deepEqual(env.select.options.map((option) => option.value), ['', 'latest-a']);
});

test('a slower stale source response cannot overwrite the latest source', async () => {
  const first = deferred();
  const second = deferred();
  const calls = [];
  const env = runtime((url) => {
    calls.push(url);
    return calls.length === 1 ? first.promise : second.promise;
  });

  const oldRequest = env.api.obRefreshChoiceSelect(env.select, true);
  env.sourceControl.value = 'warehouse-b';
  const latestRequest = env.api.obRefreshChoiceSelect(env.select, false);

  second.resolve(response({items: [{id: 'b-location', _label: 'B location'}], total: 1}));
  await latestRequest;
  first.resolve(response({items: [{id: 'a-location', _label: 'A location'}], total: 1}));
  await oldRequest;

  assert.equal(calls.length, 2);
  assert.deepEqual(env.select.options.map((option) => option.value), ['', 'b-location']);
  assert.equal(env.select.options.some((option) => option.value === 'a-location'), false);
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

test('processor form context forwards form_kind; entity context does not add it (#1840)', async () => {
  const calls = [];
  const env = runtime(async (url) => { calls.push(url); return response({items: [], total: 0}); });
  await env.api.obRefreshChoiceSelect(env.select, true);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].includes('form_kind='), false);

  env.attrs['data-ref-choice-context'] = JSON.stringify({
    form_entity: 'ПоискЗаявок',
    form_kind: 'processor',
    form: 'ФормаОбъекта',
    element: 'inventory-storage-choice',
    sources: {'Объект.Склад': 'Склад'},
  });
  await env.api.obRefreshChoiceSelect(env.select, true);
  assert.equal(calls.length, 2);
  assert.match(calls[1], /&form_entity=%D0%9F[^&]*&form_kind=processor&form=/);
});

test('choice_dropdown false keeps only the selected option after a source refresh', async () => {
  const env = runtime(async () => response({
    items: [{id: 'legacy-location', _label: 'Saved'}, {id: 'other-location', _label: 'Other'}],
    total: 2, selected_allowed: true,
  }), 'legacy-location', false, true, true);
  env.sourceControl.value = 'warehouse-b';
  await env.api.obRefreshChoiceSelect(env.select, false);
  assert.deepEqual(env.select.options.map((option) => option.value), ['', 'legacy-location']);
  assert.equal(env.select.value, 'legacy-location');
});

// choice_dropdown: false на owner-only пути — без choice_filter и без
// choice_folders. Закрытый список обещан закрытым: первые 50 строк ответа — не
// список, а случайная выборка, и раскрывать его при смене владельца нельзя.
// Выбирают в таком поле кнопкой подбора, она рядом и никуда не делась.
test('closed owner-only list stays closed after the owner changes', async () => {
  const answer = deferred();
  const env = runtime(() => answer.promise, 'b-1', true, false, true);

  env.api.obRefreshDependentSelects(env.ownerControl);
  answer.resolve(response({items: [{id: 'b-1', _label: 'B one'}, {id: 'b-2', _label: 'B two'}]}));
  await new Promise(setImmediate);

  assert.deepEqual(env.select.options.map((option) => option.value), ['', 'b-1']);
  assert.equal(env.select.value, 'b-1');
  assert.equal(env.select.options[1].textContent, 'B one');
});

// Смена владельца может сделать текущее значение чужим. Тогда закрытый список
// обязан его отпустить, а не хранить ссылку на элемент соседнего владельца.
test('closed owner-only list drops a value the new owner does not have', async () => {
  const answer = deferred();
  const env = runtime(() => answer.promise, 'a-1', true, false, true);

  env.api.obRefreshDependentSelects(env.ownerControl);
  answer.resolve(response({items: [{id: 'b-1', _label: 'B one'}, {id: 'b-2', _label: 'B two'}]}));
  await new Promise(setImmediate);

  assert.deepEqual(env.select.options.map((option) => option.value), ['']);
  assert.equal(env.select.value, '');
});

// Обычное поле без ключа ведёт себя как прежде: страница ответа видна целиком.
test('open owner-only list keeps showing the whole page', async () => {
  const answer = deferred();
  const env = runtime(() => answer.promise, 'b-1', true, false);

  env.api.obRefreshDependentSelects(env.ownerControl);
  answer.resolve(response({items: [{id: 'b-1', _label: 'B one'}, {id: 'b-2', _label: 'B two'}]}));
  await new Promise(setImmediate);

  assert.deepEqual(env.select.options.map((option) => option.value), ['', 'b-1', 'b-2']);
  assert.equal(env.select.value, 'b-1');
});

// Verify the request consumer too: duplicated names must follow the same
// disabled/radio/mirror semantics as the form submitted by the browser.
for (const [description, controls, expected] of [
  ['disabled first copy', [{value: 'old', disabled: true}, {value: 'current'}], 'current'],
  ['disabled last copy', [{value: 'current'}, {value: 'old', disabled: true}], 'current'],
  ['disabled fieldset', [{value: 'old', matches: () => true}, {value: 'current', matches: () => false}], 'current'],
  ['readonly mirror', [{value: 'old', disabled: true}, {type: 'hidden', value: 'current'}], 'current'],
  ['checked radio', [{type: 'radio', value: 'old', checked: false}, {type: 'radio', value: 'current', checked: true}], 'current'],
  ['disabled checked radio', [{type: 'radio', value: 'old', checked: true, disabled: true}, {type: 'radio', value: 'current', checked: true}], 'current'],
  ['unchecked checkbox and hidden false value', [{type: 'checkbox', value: 'true', checked: false}, {type: 'hidden', value: 'false'}], 'false'],
  ['only disabled controls', [{value: 'old', disabled: true}], ''],
]) {
  test(`choice source request uses ${description}`, async () => {
    let requested;
    const env = runtime(async (url) => {
      requested = new URL(url, 'http://localhost');
      return response({items: [], total: 0});
    });
    env.sourceControls.splice(0, env.sourceControls.length, ...controls);
    await env.api.obRefreshChoiceSelect(env.select, true);
    assert.equal(JSON.parse(requested.searchParams.get('sources'))['Объект.Склад'], expected);
  });
}
