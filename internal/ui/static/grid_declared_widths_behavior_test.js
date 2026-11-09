// Объявленные ширины колонок (`width` у kind: Колонка) у таблицы, которая в
// видимую область не помещается: сохраняются, таблица прокручивается.
// autosizeColumns подгонял сумму ширин под контейнер, и сотня колонок табеля
// ужималась до минимума — подписи превращались в «1…».
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');
// Срез от obGridKeepDeclaredWidths до obGridColumnIds берёт и obGridAutosize.
const start = source.indexOf('function obGridKeepDeclaredWidths');
const end = source.indexOf('function obGridColumnIds', start);
assert.ok(start >= 0 && end > start, 'срез obGridKeepDeclaredWidths не найден');
vm.runInThisContext(source.slice(start, end), {filename: 'managed-declared-widths.js'});

function grid(columns) {
  return {
    columns, setCalls: 0,
    getColumns() { return this.columns; },
    setColumns(cols) { this.columns = cols; this.setCalls++; },
  };
}

test('широкая таблица с объявленными ширинами их сохраняет', () => {
  // 31 день по 52 px — заведомо шире 1200 px; autosize ужал бы их до 30.
  const cols = [];
  for (let d = 1; d <= 31; d++) cols.push({id: 'День' + d, width: 30, obWidth: 52});
  cols.push({id: 'Без ширины', width: 30});
  const g = grid(cols);
  assert.equal(obGridKeepDeclaredWidths(g, 1200), true, 'autosize должен быть пропущен');
  assert.equal(g.columns[0].width, 52);
  assert.equal(g.columns[31].width, 120, 'колонка без width — ширина по умолчанию');
  assert.equal(g.setCalls, 1);
  // Повтор (показ вкладки, ресайз окна) ничего не перерисовывает зря.
  assert.equal(obGridKeepDeclaredWidths(g, 1200), true);
  assert.equal(g.setCalls, 1);
});

test('помещающаяся таблица растягивается, но только за счёт колонок без width', () => {
  const g = grid([{id: 'Дата', width: 300, obWidth: 130, resizable: true}, {id: 'Текст', width: 120, resizable: true}]);
  assert.equal(obGridKeepDeclaredWidths(g, 1200), false);
  assert.equal(g.setCalls, 0);
  let seen = null;
  g.autosizeColumns = function() { seen = this.columns.map((c) => [c.id, c.width, c.resizable]); };
  obGridAutosize(g);
  // Во время подгонки «Дата» — своей ширины и вне подгонки; «Текст» растягивается.
  assert.deepEqual(seen, [['Дата', 130, false], ['Текст', 120, true]]);
  assert.equal(g.columns[0].resizable, true, 'перетаскивание колонке возвращается');
});

test('таблица без объявленных ширин подгоняется как раньше', () => {
  const g = grid([{id: 'А', width: 120, resizable: true}]);
  let calls = 0;
  g.autosizeColumns = function() { calls++; assert.equal(this.columns[0].resizable, true); };
  obGridAutosize(g);
  assert.equal(calls, 1);
});

test('таблица без объявленных ширин не меняет поведения, даже широкая', () => {
  const cols = [];
  for (let i = 0; i < 20; i++) cols.push({id: 'К' + i, width: 60});
  const g = grid(cols);
  assert.equal(obGridKeepDeclaredWidths(g, 800), false);
  assert.equal(g.setCalls, 0);
});

test('скрытый контейнер (ширина 0) решение не принимает', () => {
  const g = grid([{id: 'День1', width: 30, obWidth: 52}]);
  assert.equal(obGridKeepDeclaredWidths(g, 0), false);
});
