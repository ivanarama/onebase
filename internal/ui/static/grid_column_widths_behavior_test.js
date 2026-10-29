const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');

// Ширины колонок за пользователем (#2357) — полный путь настоящего
// инициализатора SlickGrid из managed.js: применение сохранённых ширин,
// изменение мышью → POST /ui/_grid-widths, «Стандартные колонки».
const source = fs.readFileSync('static/managed.js', 'utf8');

function mountedGrid(cols, savedWidths) {
  const resetListeners = {}, saves = [];
  const reset = {style: {}, addEventListener(type, fn) { resetListeners[type] = fn; }};
  const attrs = {
    'data-sg-tp': 'Lines', 'data-sg-el': 'Lines', 'data-sg-layout': 'document/Test/Form/Lines',
    'data-sg-cols': JSON.stringify(cols.map(c => Object.assign({name: c.id, type: 'string'}, c))),
    'data-sg-rows': '[]', 'data-sg-widths': typeof savedWidths === 'string' ? savedWidths : (savedWidths ? JSON.stringify(savedWidths) : '')
  };
  const div = {
    offsetParent: {}, parentNode: {querySelector() { return reset; }},
    getAttribute(name) { return attrs[name] || null; },
    addEventListener() {}, querySelectorAll() { return []; }, contains() { return false; }
  };
  const slot = () => { const s = {fns: [], subscribe(fn) { s.fns.push(fn); }}; return s; };
  function DataView() { this.onRowsChanged = slot(); this.onRowCountChanged = slot(); }
  DataView.prototype.setItems = function(items) { this.items = items; };
  DataView.prototype.getItems = function() { return this.items; };
  let grid;
  function Grid(host, data, columns) {
    grid = this;
    Object.assign(this, {columns, data, setCalls: 0});
    for (const name of ['onSort', 'onCellChange', 'onValidationError', 'onKeyDown', 'onColumnsResized']) this[name] = slot();
  }
  Grid.prototype.getOptions = () => ({showFooterRow: false});
  Grid.prototype.resizeCanvas = Grid.prototype.autosizeColumns = function() {};
  Grid.prototype.getColumns = function() { return this.columns; };
  Grid.prototype.getEditorLock = () => ({commitCurrentEdit: () => true});
  Grid.prototype.setColumns = function(columns) { this.setCalls++; this.columns = columns; };
  const window = {addEventListener() {}, fetch(url, options) {
    saves.push({url, body: new URLSearchParams(options.body)});
    return Promise.resolve();
  }};
  const document = {
    readyState: 'complete', querySelectorAll(s) { return s === '.ob-grid[data-sg-tp]' ? [div] : []; },
    addEventListener() {}, getElementById() { return null; }
  };
  const start = source.indexOf('// SlickGrid initializer for managed-form table parts');
  const end = source.indexOf('\n// Авто-вызов ПриОткрытииФормы', start);
  assert.ok(start >= 0 && end > start);
  const helpersStart = source.indexOf('var OB_GRID_WIDTH_MIN');
  const helpersEnd = source.indexOf('// Grids are stored in window._obGrids', helpersStart);
  assert.ok(helpersStart >= 0 && helpersEnd > helpersStart);
  new Function('window', 'document', 'Slick', 'formGridItemMetadata', 'copyFormGridStyleKeys',
    source.slice(helpersStart, helpersEnd) + source.slice(start, end))(window, document, {Grid, Data: {DataView}, Editors: {Text() {}}}, () => {}, () => {});
  const col = id => grid.columns.find(c => c.id === id);
  return {
    grid, saves, reset, col,
    resize(id, px) { col(id).width = px; grid.onColumnsResized.fns.forEach(fn => fn({}, {grid})); },
    resetColumns() { resetListeners.click(); },
    lastWidths() { return JSON.parse(saves[saves.length - 1].body.get('widths') || 'null'); },
  };
}

test('ширина шире предела показывается той, что сохранится, и переживает повторное открытие', async () => {
  const g = mountedGrid([{id: 'Товар'}, {id: 'Цена'}]);
  assert.equal(g.col('Товар').maxWidth, 2000, 'SlickGrid не даёт растянуть шире предела');
  g.resize('Товар', 2618);
  await Promise.resolve();
  assert.equal(g.col('Товар').width, 2000, 'видимая ширина — та, что сохраняется');
  assert.equal(g.lastWidths()['Товар'], 2000);
  const reopened = mountedGrid([{id: 'Товар'}, {id: 'Цена'}], g.lastWidths());
  assert.equal(reopened.col('Товар').width, 2000, 'граница диапазона применяется при повторном открытии');
});

test('объявленная в форме ширина больше предела не обрезается молча при изменении другой колонки', async () => {
  const g = mountedGrid([{id: 'Товар', width: 2500}, {id: 'Цена'}]);
  assert.equal(g.col('Товар').width, 2000);
  g.resize('Цена', 150);
  await Promise.resolve();
  assert.equal(g.col('Товар').width, 2000);
  assert.equal(g.lastWidths()['Товар'], 2000);
});

test('колонки с именами Object.prototype: ширины сохраняются и сбрасываются числами', async () => {
  const g = mountedGrid([{id: 'constructor'}, {id: '__proto__'}, {id: 'toString', width: 90}]);
  g.resize('__proto__', 333);
  await Promise.resolve();
  const saved = g.lastWidths();
  assert.ok(Object.prototype.hasOwnProperty.call(saved, '__proto__'), 'ширина колонки __proto__ не потеряна');
  assert.equal(saved['__proto__'], 333);
  assert.equal(typeof saved['constructor'], 'number');
  g.resetColumns();
  await Promise.resolve();
  for (const id of ['constructor', '__proto__', 'toString']) {
    assert.equal(typeof g.col(id).width, 'number', id + ': ширина после сброса — число');
  }
  assert.equal(g.col('toString').width, 90, 'объявленная ширина вернулась');
  assert.equal(g.col('constructor').width, 120);
  assert.equal(g.col('constructor').obWidth, undefined);
  const reopened = mountedGrid([{id: 'constructor'}, {id: '__proto__'}], '{"__proto__":333}');
  assert.equal(reopened.col('__proto__').width, 333);
  assert.notEqual(typeof reopened.col('constructor').width, 'function');
});
