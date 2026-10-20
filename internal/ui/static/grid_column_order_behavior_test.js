const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');

// Порядок колонок таблицы формы за пользователем: разбор сохранённого
// порядка, применение к колонкам формы и перенос колонки перетаскиванием.
const source = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('function obGridParseOrder');
const end = source.indexOf('\n(function(){', start);
if (start < 0 || end < 0) throw new Error('grid column order helpers not found');
const helpers = new Function(source.slice(start, end) +
  '\nreturn {obGridParseOrder, obGridApplyOrder, obGridMoveColumn, obGridColumnIds};')();

const cols = () => [{id: 'Номер'}, {id: 'Адрес'}, {id: 'Мастер'}];

test('сохранённый порядок применяется на месте массива', () => {
  const columns = cols();
  const same = columns;
  assert.equal(helpers.obGridApplyOrder(columns, ['Мастер', 'Номер', 'Адрес']), true);
  assert.equal(columns, same, 'массив тот же: обработчики берут columns[args.cell]');
  assert.deepEqual(helpers.obGridColumnIds(columns), ['Мастер', 'Номер', 'Адрес']);
});

test('новые колонки формы — после переставленных, исчезнувшие — пропускаются', () => {
  const columns = cols();
  assert.equal(helpers.obGridApplyOrder(columns, ['Мастер', 'Удалённая']), true);
  assert.deepEqual(helpers.obGridColumnIds(columns), ['Мастер', 'Номер', 'Адрес']);
});

test('порядок как в форме — без изменений', () => {
  const columns = cols();
  assert.equal(helpers.obGridApplyOrder(columns, ['Номер', 'Адрес', 'Мастер']), false);
  assert.equal(helpers.obGridApplyOrder(columns, []), false);
});

test('разбор data-sg-order: мусор не ломает таблицу', () => {
  assert.deepEqual(helpers.obGridParseOrder('["Адрес","",5,"Номер"]'), ['Адрес', 'Номер']);
  assert.deepEqual(helpers.obGridParseOrder('{"a":1}'), []);
  assert.deepEqual(helpers.obGridParseOrder('не json'), []);
  assert.deepEqual(helpers.obGridParseOrder(null), []);
});

test('перетаскивание переносит колонку на место целевой', () => {
  const columns = cols();
  assert.equal(helpers.obGridMoveColumn(columns, 2, 0), true);
  assert.deepEqual(helpers.obGridColumnIds(columns), ['Мастер', 'Номер', 'Адрес']);
  assert.equal(helpers.obGridMoveColumn(columns, 0, 2), true);
  assert.deepEqual(helpers.obGridColumnIds(columns), ['Номер', 'Адрес', 'Мастер']);
  assert.equal(helpers.obGridMoveColumn(columns, 1, 1), false);
  assert.equal(helpers.obGridMoveColumn(columns, 0, 9), false);
});

// Штатный инициализатор подключает события DOM-host; тест не вызывает
// enableColumnReorder напрямую и проверяет оба пользовательских пути.
function mountedGrid(ids, savedOrder, fetchOrder) {
  const listeners = {}, resetListeners = {}, saves = [];
  const reset = {style: {}, addEventListener(type, fn) { resetListeners[type] = fn; }};
  const headers = ids.map(() => ({setAttribute() {}, closest(s) { return s === '.slick-header-column' ? this : null; }}));
  const attrs = {
    'data-sg-tp': 'Lines', 'data-sg-el': 'Lines', 'data-sg-layout': 'processor/Test/Form/Lines',
    'data-sg-cols': JSON.stringify(ids.map(id => ({id, name: id, type: 'number'}))),
    'data-sg-rows': '[{"Price":5}]', 'data-sg-order': JSON.stringify(savedOrder || [])
  };
  const div = {
    offsetParent: {}, parentNode: {querySelector() { return reset; }},
    getAttribute(name) { return attrs[name] || null; },
    addEventListener(type, fn) { listeners[type] = fn; },
    querySelectorAll() { return headers; }, contains(h) { return headers.includes(h); }
  };
  const slot = () => ({subscribe() {}});
  function DataView() { this.onRowsChanged = slot(); this.onRowCountChanged = slot(); }
  DataView.prototype.setItems = function(items) { this.items = items; };
  DataView.prototype.getItems = function() { return this.items; };
  let grid;
  function Grid(host, data, columns) {
    grid = this;
    Object.assign(this, {columns, data, pending: null, valid: true, commitCalls: 0, commitColumns: [], setCalls: 0});
    for (const name of ['onSort', 'onCellChange', 'onValidationError', 'onKeyDown']) this[name] = slot();
  }
  Grid.prototype.getOptions = () => ({showFooterRow: false});
  Grid.prototype.resizeCanvas = Grid.prototype.autosizeColumns = function() {};
  Grid.prototype.getEditorLock = function() {
    return {commitCurrentEdit: () => {
      this.commitCalls++;
      this.commitColumns.push(this.columns.slice());
      if (!this.valid) return false;
      if (this.pending !== null) this.data.items[0].Price = this.pending;
      this.pending = null;
      return true;
    }};
  };
  // SlickGrid.setColumns закрывает редактор, теряя незавершённый ввод.
  Grid.prototype.setColumns = function(columns) { this.setCalls++; this.pending = null; this.columns = columns; };
  const window = {addEventListener() {}, fetch(url, options) {
    saves.push(options.body);
    return fetchOrder ? fetchOrder(url, options) : Promise.resolve();
  }};
  const document = {
    readyState: 'complete', querySelectorAll(s) { return s === '.ob-grid[data-sg-tp]' ? [div] : []; },
    addEventListener() {}, getElementById() { return null; }
  };
  const start = source.indexOf('// SlickGrid initializer for managed-form table parts');
  const end = source.indexOf('\n// Авто-вызов ПриОткрытииФормы', start);
  assert.ok(start >= 0 && end > start);
  new Function('window', 'document', 'Slick', 'formGridItemMetadata', 'copyFormGridStyleKeys',
    source.slice(start, end))(window, document, {Grid, Data: {DataView}, Editors: {Text() {}}}, () => {}, () => {});
  return {grid, saves, reset,
    drop(from, to) {
      listeners.dragstart({target: headers[from], dataTransfer: {setData() {}}});
      listeners.drop({target: headers[to], preventDefault() {}});
    },
    resetOrder() { resetListeners.click(); }
  };
}

for (const action of ['drop', 'reset']) {
  for (const valid of [true, false]) {
    test(`${action}: активная правка ${valid ? 'сохраняется' : 'блокирует операцию при ошибке'}`, () => {
      const state = mountedGrid(['Qty', 'Price'], action === 'reset' ? ['Price', 'Qty'] : []);
      const before = state.grid.columns.slice(), same = state.grid.columns;
      state.grid.pending = 99;
      state.grid.valid = valid;
      if (action === 'drop') state.drop(1, 0); else state.resetOrder();
      assert.equal(state.grid.commitCalls, 1);
      assert.deepEqual(state.grid.commitColumns[0], before, 'редактор сохраняется до изменения массива колонок');
      assert.equal(state.grid.columns, same);
      if (valid) {
        assert.equal(state.grid.data.items[0].Price, 99);
        assert.equal(state.grid.pending, null);
        assert.deepEqual(state.grid.columns, before.slice().reverse());
        assert.equal(state.grid.setCalls, 1);
        assert.equal(state.saves.length, 1);
      } else {
        assert.equal(state.grid.data.items[0].Price, 5);
        assert.equal(state.grid.pending, 99);
        assert.deepEqual(state.grid.columns, before);
        assert.equal(state.grid.setCalls, 0);
        assert.deepEqual(state.saves, []);
        assert.deepEqual(state.reset.style, {});
        state.grid.valid = true;
        if (action === 'drop') state.drop(1, 0); else state.resetOrder();
        assert.equal(state.grid.data.items[0].Price, 99);
        assert.equal(state.saves.length, 1);
      }
    });
  }
}

test('инициализация и сброс сохраняют колонки с именами Object.prototype', () => {
  const ids = ['Qty', 'constructor', 'toString', 'hasOwnProperty', '__proto__'];
  const state = mountedGrid(ids, ['toString', 'constructor', 'toString', '__proto__']);
  assert.deepEqual(state.grid.columns.map(c => c.id), ['toString', 'constructor', '__proto__', 'Qty', 'hasOwnProperty']);
  state.resetOrder();
  assert.deepEqual(state.grid.columns.map(c => c.id), ids);
  assert.equal(state.saves.length, 1);
});

// The first HTTP operation stays in flight while the user changes the layout.
// Completing it simulates the server write, rather than only recording fetches.
function delayedSaves() {
  const requests = [];
  let saved = [];
  return {
    requests,
    saved: () => saved,
    fetch(url, options) {
      assert.equal(url, '/ui/_grid-order');
      return new Promise((resolve, reject) => {
        requests.push({
          complete(fail = false) {
            if (fail) { reject(new Error('offline')); return; }
            const order = new URLSearchParams(options.body).get('order');
            saved = order ? JSON.parse(order) : [];
            resolve({ok: true});
          }
        });
      });
    }
  };
}
const flushSaves = () => new Promise(resolve => setImmediate(resolve));

for (const resetLast of [false, true]) {
  for (const failFirst of [false, true]) {
    test(`overlapping saves: last ${resetLast ? 'reset' : 'move'}, first ${failFirst ? 'fails' : 'succeeds'}`, async () => {
      const server = delayedSaves();
      const ids = ['Qty', 'Price', 'Total'];
      const state = mountedGrid(ids, [], server.fetch);
      state.drop(0, 2); // Price, Total, Qty — held at the server.
      state.drop(1, 0); // Total, Price, Qty — superseded before sending.
      if (resetLast) state.resetOrder();
      else state.drop(2, 0); // Qty, Total, Price — latest desired order.
      const expected = state.grid.columns.map(c => c.id);
      assert.equal(server.requests.length, 1, 'one in-flight save per table');
      server.requests[0].complete(failFirst);
      await flushSaves();
      assert.equal(server.requests.length, 2, 'only the latest pending action is sent');
      server.requests[1].complete();
      await flushSaves();
      const reopened = mountedGrid(ids, server.saved());
      assert.deepEqual(reopened.grid.columns.map(c => c.id), expected);
      assert.equal(server.requests.length, 2);
      // A drained queue must also accept later actions.
      state.drop(0, 1);
      assert.equal(server.requests.length, 3);
      server.requests[2].complete();
      await flushSaves();
    });
  }
}
