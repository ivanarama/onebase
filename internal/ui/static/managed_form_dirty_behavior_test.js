'use strict';

// The Go test supplies actual HTTP form-event responses and verifies persisted
// values. Run the entire browser runtime, including obFire and beforeunload.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');
const fixtures = JSON.parse(fs.readFileSync(process.env.ONEBASE_FORM_DIRTY_FIXTURES, 'utf8'));

function browser(fixture, {delay = false, loadUI = false, grid = false} = {}) {
  const docListeners = new Map();
  const winListeners = new Map();
  const controls = {};
  const gridAttrs = {
    'data-sg-tp': 'Строки',
    'data-sg-cols': JSON.stringify([{id: 'Канал', type: 'string'}]),
    'data-sg-rows': JSON.stringify([{Канал: 'first'}, {Канал: 'second'}])
  };
  const gridHost = {offsetParent: null, addEventListener() {}, getAttribute(name) { return gridAttrs[name] || null; }};
  function eventSlot() {
    const listeners = [];
    return {subscribe(fn) { listeners.push(fn); }, notify(args) { for (const fn of listeners) fn({}, args); }};
  }
  class DataView {
    constructor() { this.onRowCountChanged = eventSlot(); this.onRowsChanged = eventSlot(); }
    setItems(items) { this.items = items; }
    getItems() { return this.items; }
    getItem(index) { return this.items[index]; }
    getRowById(id) { return this.items.findIndex(item => item.id === id); }
    addItem(item) { this.items.push(item); }
    deleteItem(id) { this.items.splice(this.getRowById(id), 1); }
  }
  class Grid {
    constructor() {
      for (const name of ['onSort', 'onCellChange', 'onValidationError', 'onKeyDown']) this[name] = eventSlot();
    }
    getEditorLock() { return {isActive() { return false; }}; }
    getActiveCell() { return {row: 0, cell: 0}; }
    getOptions() { return {}; }
    invalidate() {}
    setActiveCell() {}
    scrollRowIntoView() {}
    editActiveCell() {}
    render() {}
  }
  function element() {
    return {style: {}, children: [], remove() {}, appendChild(child) { this.children.push(child); return child; }};
  }
  const banner = element();
  const fileContent = {name: '_fc_Наименование', value: '', disabled: false, dataset: {}};
  const form = {
    id: 'main-form',
    getAttribute() { return null; },
    querySelectorAll(selector) { return fixture.file && selector === '[data-ob-file-content-for]' ? [fileContent] : []; },
    querySelector(selector) {
      if (fixture.file && selector === '[data-ob-file-content-for="Наименование"]') return fileContent;
      const match = selector.match(/^\[name="([^"]+)"\]$/);
      return match ? controls[match[1]] || null : null;
    },
    appendChild(el) { controls[el.name] = el; return el; }
  };
  for (const [name, value] of Object.entries({Наименование: fixture.input, _id: fixture.id, _version: '1'})) {
    controls[name] = {
      name, value, tagName: 'INPUT', type: 'text', disabled: false,
      closest(selector) { return selector === '#main-form' ? form : null; },
      getAttribute() { return null; }, hasAttribute() { return false; }
    };
  }
  function listen(map, type, fn) {
    if (!map.has(type)) map.set(type, []);
    map.get(type).push(fn);
  }
  const document = {
    readyState: 'loading', title: 'Обращение', activeElement: null,
    documentElement: {className: ''},
    head: element(), body: element(),
    addEventListener(type, fn) { listen(docListeners, type, fn); },
    querySelector(selector) {
      if (selector.startsWith('#main-form ')) return form.querySelector(selector.slice(11));
      return null;
    },
    querySelectorAll(selector) { return grid && selector === '.ob-grid[data-sg-tp]' ? [gridHost] : []; },
    contains() { return true; },
    createElement: element,
    getElementById(id) {
      if (id === 'main-form') return form;
      if (id === 'ob-fmevt-banner') return banner;
      if (id === 'ob-managed-config') return {textContent: JSON.stringify({url: '/form-event', docId: fixture.id})};
      return null;
    }
  };
  class FormData {
    constructor() { this.values = new Map(Object.values(controls).map(el => [el.name, el.value])); }
    set(name, value) { this.values.set(name, value); }
    forEach(fn) { this.values.forEach(fn); }
  }
  const requests = [];
  const replies = [];
  let signalRequest;
  const requestSent = new Promise(resolve => { signalRequest = resolve; });
  const location = {pathname: '/ui/catalog/Обращение/' + (fixture.id || 'new')};
  const context = {
    document, FormData, URLSearchParams, location,
    Slick: {Data: {DataView}, Grid, Editors: {Text: function() {}}},
    history: {replaceState(_state, _title, url) { location.pathname = url; }},
    CSS: {escape: value => value}, console,
    navigator: {userAgent: 'Node.js', platform: '', maxTouchPoints: 0},
    sessionStorage: {getItem() { return null; }, setItem() {}, removeItem() {}},
    setTimeout() {}, clearTimeout() {},
    addEventListener(type, fn) { listen(winListeners, type, fn); },
    async fetch(url, options) {
      requests.push({url, body: new URLSearchParams(options.body)});
      signalRequest();
      if (delay) await new Promise(resolve => { replies.push(resolve); });
      return {ok: true, async json() { return fixture.response; }};
    }
  };
  context.window = context;
  vm.createContext(context);
  if (loadUI) vm.runInContext(fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8'), context, {filename: 'ui.js'});
  vm.runInContext(source, context, {filename: 'managed.js'});
  if (grid) {
    document.readyState = 'complete';
    for (const fn of docListeners.get('DOMContentLoaded') || []) fn();
  }
  return {context, document, controls, requests, banner,
    requestSent,
    reply() { replies.shift()(); },
    input(type = 'input') { for (const fn of docListeners.get(type) || []) fn({target: controls.Наименование}); },
    unload() {
      const event = {prevented: false, preventDefault() { this.prevented = true; }};
      for (const fn of winListeners.get('beforeunload') || []) fn(event);
      return event.prevented;
    }
  };
}

for (const fixture of fixtures) {
  for (const initialDirty of [false, true]) {
    test(`${fixture.name}, initially ${initialDirty ? 'dirty' : 'clean'}`, async () => {
      const b = browser(fixture);
      if (initialDirty) b.input();
      assert.equal(b.context._obFormDirty, initialDirty);
      await b.context.obFire('КнопкаТест', 'Нажатие');
      assert.deepEqual(b.banner.children.map(el => el.children[0].textContent),
        [...(fixture.response.messages || []), ...(fixture.response.error ? [fixture.response.error] : [])],
        'the runtime must apply the response without a client-side error');
      assert.equal(b.requests.length, 1);
      assert.equal(b.requests[0].body.get('Наименование'), fixture.input);
      assert.equal(b.controls.Наименование.value, fixture.value);
      const dirty = fixture.noWrite ? initialDirty : fixture.dirty;
      assert.equal(b.context._obFormDirty, dirty, 'unsaved state after the handler');
      assert.equal(b.document.title, dirty ? '● Обращение' : 'Обращение');
      assert.equal(b.unload(), dirty, 'closing the form must warn about unsaved changes');
      assert.equal(b.controls._version.value, fixture.noWrite ? '1' : String(fixture.response.version));
      assert.equal(b.controls._id.value, fixture.response.savedId || fixture.id);
      if (fixture.response.savedId) assert.ok(b.context.location.pathname.endsWith('/' + fixture.response.savedId));
    });
  }
}

const fileSave = fixtures.find(fixture => fixture.name === 'file_save');
for (const initialDirty of [false, true]) {
  test(`delayed save without later edits, initially ${initialDirty ? 'dirty' : 'clean'}`, async () => {
    const b = browser(fileSave, {delay: true});
    if (initialDirty) b.input();
    const pending = b.context.obFire('КнопкаТест', 'Нажатие');
    await b.requestSent;
    b.reply();
    await pending;
    assert.equal(b.context._obFormDirty, false, 'edits already in FormData were saved');
    assert.equal(b.document.title, 'Обращение');
    assert.equal(b.unload(), false);
    assert.deepEqual(b.banner.children, [], 'no client-side error');
  });
  for (const event of ['input', 'change']) {
    test(`late file path ${event}, initially ${initialDirty ? 'dirty' : 'clean'}`, async () => {
      const b = browser(fileSave, {delay: true, loadUI: true});
      if (initialDirty) b.input();
      const pending = b.context.obFire('КнопкаТест', 'Нажатие');
      await b.requestSent;
      assert.equal(b.requests[0].body.get('Наименование'), '/old/path.csv');
      b.controls.Наименование.value = '/new/unsaved.csv';
      b.input(event);
      b.reply();
      await pending;
      assert.equal(b.controls.Наименование.value, '/new/unsaved.csv');
      assert.equal(b.context._obFormDirty, true, 'the response only saved the earlier request');
      assert.equal(b.document.title, '● Обращение');
      assert.equal(b.unload(), true);
      assert.equal(b.controls._version.value, String(fileSave.response.version));
      assert.deepEqual(b.banner.children, [], 'no client-side error');
    });
  }
}

for (const action of ['add', 'delete', 'copy', 'move', 'cell']) {
  test(`late grid ${action} keeps the form dirty without a DOM input event`, async () => {
    const b = browser(fileSave, {delay: true, grid: true});
    const pending = b.context.obFire('КнопкаТест', 'Нажатие');
    await b.requestSent;
    const g = b.context._obGrids.Строки;
    if (action === 'add') b.context.obGridAddRow('Строки');
    if (action === 'delete') b.context.obGridDelRow('Строки');
    if (action === 'copy') b.context.obGridCopyRow('Строки');
    if (action === 'move') b.context.obGridMoveRow('Строки', 1);
    if (action === 'cell') {
      g.dataView.getItem(0).Канал = 'unsaved';
      g.grid.onCellChange.notify({row: 0, cell: 0, item: g.dataView.getItem(0)});
    }
    assert.equal(b.context._obFormDirty, true, 'the real grid edit path ran');
    b.reply();
    await pending;
    assert.equal(b.context._obFormDirty, true, 'an earlier save cannot acknowledge the grid edit');
    assert.equal(b.document.title, '● Обращение');
    assert.equal(b.unload(), true);
    assert.deepEqual(b.banner.children, [], 'no client-side error');
  });
}

test('late DOM table deletion keeps the form dirty without a DOM input event', async () => {
  const b = browser(fileSave, {delay: true, loadUI: true});
  const row = {querySelector() { return null; }, remove() { body.rows.splice(0, 1); }};
  const body = {rows: [row], querySelectorAll() { return []; }};
  row.parentElement = body;
  const table = {
    tBodies: [body], _obCurrentRow: row,
    getAttribute(name) { return name === 'data-ob-dom-table' ? 'Строки' : null; },
    querySelector() { return null; }
  };
  const pending = b.context.obFire('КнопкаТест', 'Нажатие');
  await b.requestSent;
  b.context.obDOMDeleteRows(table);
  assert.equal(body.rows.length, 0, 'the real DOM row deletion ran');
  b.reply();
  await pending;
  assert.equal(b.context._obFormDirty, true);
  assert.equal(b.document.title, '● Обращение');
  assert.equal(b.unload(), true);
  assert.deepEqual(b.banner.children, [], 'no client-side error');
});
