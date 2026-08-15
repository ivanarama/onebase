const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');

const source = fs.readFileSync('static/managed.js', 'utf8');
const helperStart = source.indexOf('function obManagedIsReservedVirtualColumnName');
const helperEndMarker = 'window.obManagedSetTablePartJSON = obManagedSetTablePartJSON;';
const helperEndAt = source.indexOf(helperEndMarker, helperStart);
const start = source.indexOf('// SlickGrid initializer for managed-form table parts');
const end = source.indexOf('\n// Авто-вызов ПриОткрытииФормы', start);
if (helperStart < 0 || helperEndAt < 0) throw new Error('managed table JSON helper not found');
if (start < 0 || end < 0) throw new Error('managed SlickGrid runtime slice not found');
const helperEnd = helperEndAt + helperEndMarker.length;
const runtime = source.slice(helperStart, helperEnd) + '\n' + source.slice(start, end);

function eventSlot() {
  return {handlers: [], subscribe(fn) { this.handlers.push(fn); }};
}

function host(readOnly, overrides) {
  const attrs = {
    'data-sg-tp': 'Lines',
    'data-sg-el': readOnly ? 'ReadonlyLines' : 'WritableLines',
    'data-sg-cols': '[]',
    'data-sg-ref': 'null',
    'data-sg-enum': 'null',
    'data-sg-rows': '[]'
  };
  Object.assign(attrs, overrides || {});
  if (readOnly) attrs['data-sg-ro'] = '1';
  else attrs['data-sg-rowadd'] = '1';
  return {
    readOnly,
    offsetParent: {},
    listeners: [],
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    addEventListener(type) { this.listeners.push(type); }
  };
}

function run(order, overrides) {
  const readonly = host(true, overrides);
  const writable = host(false, overrides);
  const hosts = order === 'readonly-first' ? [readonly, writable] : [writable, readonly];
  const created = [];
  const fired = [];
  const hidden = {disabled: false, value: ''};
  const requestedFields = [];

  function DataView() {
    this.items = [];
    this.setItemsCalls = 0;
    this.onRowCountChanged = eventSlot();
    this.onRowsChanged = eventSlot();
  }
  DataView.prototype.setItems = function(items) { this.items = items.slice(); this.setItemsCalls++; };
  DataView.prototype.getItems = function() { return this.items; };

  function Grid(div, dataView, columns, options) {
    this.div = div;
    this.dataView = dataView;
    this.columns = columns;
    this.options = options;
    this.onSort = eventSlot();
    this.onCellChange = eventSlot();
    this.onValidationError = eventSlot();
    this.onKeyDown = eventSlot();
    this.resizeCalls = 0;
    created.push(this);
  }
  Grid.prototype.getOptions = function() { return this.options; };
  Grid.prototype.getFooterRowColumn = function() { return null; };
  Grid.prototype.resizeCanvas = function() { this.resizeCalls++; };
  Grid.prototype.autosizeColumns = function() {};
  Grid.prototype.updateRowCount = function() {};
  Grid.prototype.render = function() {};
  Grid.prototype.invalidate = function() {};
  Grid.prototype.getActiveCell = function() { return null; };
  Grid.prototype.getEditorLock = function() { return {isActive() { return false; }}; };

  global.window = {
    _obGrids: {},
    addEventListener() {},
    obFire(element, eventName) { fired.push({element, eventName}); }
  };
  global.document = {
    readyState: 'complete',
    activeElement: null,
    querySelectorAll(selector) { return selector === '.ob-grid[data-sg-tp]' ? hosts : []; },
    getElementsByName(name) {
      requestedFields.push(name);
      return name === 'tp_json.Lines' ? [hidden] : [];
    },
    getElementById() { return null; },
    addEventListener() {},
    contains() { return true; }
  };
  global.Slick = {Data: {DataView}, Grid, Editors: {Text: function TextEditor() {}}};
  global.formGridItemMetadata = function() {};
  global.copyFormGridStyleKeys = function() {};

  eval(runtime);
  return {readonly, writable, created, fired, hidden, requestedFields, window: global.window};
}

test('virtual SlickGrid formatter escapes HTML', () => {
  const state = run('writable-first', {
    'data-sg-cols': JSON.stringify([{
      id: 'Virtual', name: 'Virtual', type: 'string', virtual: true
    }])
  });
  const formatter = state.created[0].columns[0].formatter;
  const attack = `<img src=x onerror=alert(1)>&"'`;
  assert.equal(
    formatter(0, 0, attack),
    "<span style='color:#64748b'>&lt;img src=x onerror=alert(1)&gt;&amp;&quot;&#39;</span>"
  );
});

test('reserved virtual column cannot overwrite stable row order', () => {
  const state = run('writable-first', {
    'data-sg-cols': JSON.stringify([
      {id: 'Value', name: 'Value', type: 'string'},
      {id: '_ord', name: 'Order', type: 'string', virtual: true}
    ]),
    'data-sg-rows': JSON.stringify([
      {Value: 'first', _ord: 100},
      {Value: 'second', _ord: -1}
    ])
  });
  assert.deepEqual(state.created[0].columns.map(column => column.id), ['Value']);
  state.window.obGridSync();
  assert.deepEqual(JSON.parse(state.hidden.value), [{Value: 'first'}, {Value: 'second'}]);
});

for (const order of ['readonly-first', 'writable-first']) {
  test(`duplicate SlickGrid keeps writable authority (${order})`, () => {
    const state = run(order);
    assert.equal(state.created.length, 2, 'both visual placements must initialize');
    assert.equal(state.window._obGridViews.length, 2, 'both grids must remain available for repaint/resize');
    assert.equal(state.window._obGrids.Lines.div, state.writable, 'canonical grid must be writable');
    assert.equal(state.window._obGrids.Lines.readOnly, false);
    assert.deepEqual(state.readonly.listeners, [], 'readonly summary must not claim active mutable-grid state');
    assert.deepEqual(state.writable.listeners, ['mousedown', 'focusin']);

    state.window.obGridSync();
    assert.deepEqual(state.requestedFields, ['tp_json.Lines'], 'only canonical writable grid is serialized');
    assert.equal(state.hidden.value, '[]', 'real shared helper must update the hidden JSON payload');

    state.window.applyTableParts({Lines: []});
    assert.equal(state.window._obGridViews[0].dataView.setItemsCalls, 2, 'first display did not receive event repaint');
    assert.equal(state.window._obGridViews[1].dataView.setItemsCalls, 2, 'second display did not receive event repaint');

    state.window.obFireRowEvent('Lines', 'data-sg-rowadd', 'ПриДобавленииСтроки');
    assert.deepEqual(state.fired, [{element: 'WritableLines', eventName: 'ПриДобавленииСтроки'}]);
  });
}
