const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/ui.js', 'utf8');
const contextStart = source.indexOf('function refContextForRequest(sel)');
const contextEnd = source.indexOf('// BEGIN onebase-question-modal', contextStart);
const snapshotStart = source.indexOf('function obRefPickerRequestSnapshot(sel)');
const snapshotEnd = source.indexOf('function obRefChoiceQuery(sel)', snapshotStart);
const start = source.indexOf('function openRefPicker(selOrId)');
const end = source.indexOf('function openRefCurrent(selOrId)', start);
if (contextStart < 0 || contextEnd < 0 || snapshotStart < 0 || snapshotEnd < 0 || start < 0 || end < 0) {
  throw new Error('reference picker block is absent from ui.js');
}
const contextSource = source.slice(contextStart, contextEnd);
const snapshotSource = source.slice(snapshotStart, snapshotEnd);
const pickerSource = source.slice(start, end);

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return {promise, resolve, reject};
}

function response(data, status = 200) {
  return {ok: status >= 200 && status < 300, status, json: async () => data};
}

class FakeElement {
  constructor(tag, document, id = '') {
    this.tagName = String(tag).toUpperCase();
    this.ownerDocument = document;
    this.id = id;
    this.style = {};
    this.children = [];
    this.listeners = {};
    this.attributes = {};
    this.className = '';
    this.textContent = '';
    this.value = '';
    this.isConnected = true;
    this._innerHTML = '';
  }

  set innerHTML(value) {
    this._innerHTML = String(value);
    if (this.id === '_ref-picker-modal') this.ownerDocument.installPickerChildren();
    if (this.id === '_rp-list' && value === '') this.children = [];
  }

  get innerHTML() { return this._innerHTML; }
  appendChild(child) { this.children.push(child); child.parentNode = this; return child; }
  addEventListener(name, listener) { this.listeners[name] = listener; }
  querySelectorAll(selector) {
    return selector === '._rp-item' ? this.children.filter((child) => child.className === '_rp-item') : [];
  }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  getAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attributes, name) ? this.attributes[name] : null; }
  hasAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attributes, name); }
  focus() {}
  remove() { this.isConnected = false; }
}

function runtime(fetchImpl) {
  const nodes = new Map();
  const document = {
    body: {
      appendChild(node) { nodes.set(node.id, node); node.isConnected = true; return node; },
    },
    getElementById(id) { return nodes.get(id) || null; },
    createElement(tag) { return new FakeElement(tag, document); },
    installPickerChildren() {
      for (const id of ['_rp-card', '_rp-body', '_rp-list', '_rp-preview', '_rp-status', '_rp-search', '_rp-cancel']) {
        nodes.set(id, new FakeElement(id === '_rp-search' ? 'input' : 'div', document, id));
      }
    },
  };

  const oldOption = {
    value: 'legacy-row', text: 'legacy row',
    getAttribute() { return null; },
  };
  const branchControl = {value: 'branch-a'};
  const attrs = {
    'data-ref-entity': 'Target',
    'data-ref-context': JSON.stringify({Branch: 'Object.Branch'}),
    'data-ref-source-entity': 'Source',
    'data-ref-source-form': 'ObjectForm',
    'data-ref-element': 'TargetField',
  };
  const select = {
    disabled: false,
    readOnly: false,
    options: [oldOption],
    value: 'legacy-row',
    isConnected: true,
    form: {elements: {namedItem(name) { return name === 'Branch' ? branchControl : null; }}},
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name); },
    appendChild(option) { this.options.push(option); return option; },
    dispatchEvent() {},
  };

  const window = {fetch: fetchImpl, AbortController: globalThis.AbortController};
  const sandbox = {
    window,
    fetch: fetchImpl,
    document,
    AbortController: globalThis.AbortController,
    Event: class Event {},
    Promise,
    JSON,
    Array,
    Object,
    String,
    Error,
    encodeURIComponent,
    setTimeout(callback) { callback(); return 1; },
    clearTimeout() {},
    obRefRequestSnapshot() { return null; },
    obRefChoiceQuery() { return ''; },
    obChoiceSelectIsLive() { return true; },
    openRefCreate() {},
  };
  vm.createContext(sandbox);
  vm.runInContext(
    contextSource + '\n' + snapshotSource + '\n' + pickerSource + '\nthis.openRefPicker = openRefPicker;',
    sandbox,
    {filename: 'ui.js#ref-picker'},
  );
  return {document, select, branchControl, open: sandbox.openRefPicker};
}

async function settle() {
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
}

for (const scenario of [
  ['403', async () => ({ok: false, status: 403, json: async () => ({})})],
  ['500', async () => ({ok: false, status: 500, json: async () => ({})})],
  ['422', async () => ({ok: false, status: 422, json: async () => ({})})],
  ['network failure', async () => { throw new Error('offline'); }],
]) {
  test(`preview-only picker fails closed on ${scenario[0]}`, async () => {
    const env = runtime(scenario[1]);
    env.open(env.select);
    const list = env.document.getElementById('_rp-list');
    assert.equal(list.querySelectorAll('._rp-item').length, 1, 'preloaded local option establishes stale-row regression');

    await settle();

    assert.equal(list.querySelectorAll('._rp-item').length, 0, 'failed preview must clear every selectable row');
    assert.match(env.document.getElementById('_rp-status').textContent, /недоступно/i);
    assert.equal(env.document.getElementById('_ref-picker-modal').style.visibility, 'visible');
  });
}

test('stale 422 cannot clear rows from a newer successful search', async () => {
  const first = deferred();
  const second = deferred();
  const calls = [];
  const env = runtime((url, options) => {
    calls.push({url, options});
    return calls.length === 1 ? first.promise : second.promise;
  });
  env.open(env.select);

  const search = env.document.getElementById('_rp-search');
  search.value = 'new';
  search.listeners.input.call(search);
  assert.equal(calls.length, 2);

  second.resolve(response({items: [{id: 'row-b', _label: 'Row B'}], total: 1}));
  await settle();
  const list = env.document.getElementById('_rp-list');
  assert.deepEqual(list.querySelectorAll('._rp-item').map((item) => item.getAttribute('data-id')), ['row-b']);

  first.resolve(response({}, 422));
  await settle();
  assert.deepEqual(list.querySelectorAll('._rp-item').map((item) => item.getAttribute('data-id')), ['row-b']);
  assert.doesNotMatch(env.document.getElementById('_rp-status').textContent, /недоступно/i);
});

test('preview context change reloads once and stale response cannot overwrite it', async () => {
  const first = deferred();
  const second = deferred();
  const calls = [];
  const env = runtime((url, options) => {
    calls.push({url, options});
    return calls.length === 1 ? first.promise : second.promise;
  });
  env.open(env.select);

  const modal = env.document.getElementById('_ref-picker-modal');
  env.branchControl.value = 'branch-b';
  modal._obChoiceReloadIfChanged();
  modal._obChoiceReloadIfChanged();

  assert.equal(calls.length, 2, 'unchanged exact context must not start a duplicate POST');
  assert.deepEqual(JSON.parse(calls[0].options.body).context, {Branch: 'branch-a'});
  assert.deepEqual(JSON.parse(calls[1].options.body).context, {Branch: 'branch-b'});

  second.resolve(response({items: [{id: 'branch-b-row', _label: 'Branch B'}], total: 1}));
  await settle();
  first.resolve(response({items: [{id: 'branch-a-row', _label: 'Branch A'}], total: 1}));
  await settle();

  const ids = env.document.getElementById('_rp-list').querySelectorAll('._rp-item')
    .map((item) => item.getAttribute('data-id'));
  assert.deepEqual(ids, ['branch-b-row']);
});
