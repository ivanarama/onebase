// Поведение клиента при выбранном составе колонок табличной части (план 154).
//
// Загружаем не весь managed.js, а два верхнеуровневых помощника — тем же
// приёмом среза, которым соседний тест берёт addTpRow из ui.js. Помощники
// чистые (нужны только getAttribute и cells), поэтому полный стенд DOM здесь
// был бы дороже проверяемого.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');

function slice(startMarker, endMarker) {
  const start = source.indexOf(startMarker);
  if (start < 0) throw new Error('не найдено начало среза: ' + startMarker);
  const end = source.indexOf(endMarker, start);
  if (end < 0) throw new Error('не найден конец среза: ' + endMarker);
  return source.slice(start, end);
}

vm.runInThisContext(
  slice('function obManagedHiddenColumnNames', 'function obManagedSetTablePartJSON'),
  {filename: 'managed-hidden-column-names.js'});
vm.runInThisContext(
  slice('function obManagedHideRowColumns', 'function obManagedAddTpRow'),
  {filename: 'managed-hide-row-columns.js'});

function tbodyWith(attr) {
  return {getAttribute(name) { return name === 'data-tp-hidden-cols' ? attr : null; }};
}

function rowWithCells(count) {
  const cells = [];
  for (let i = 0; i < count; i++) cells.push({style: {}});
  return {cells};
}

test('скрытые колонки читаются из атрибута', () => {
  assert.deepEqual(obManagedHiddenColumnNames(tbodyWith('["Сумма","СуммаНДС"]')), ['Сумма', 'СуммаНДС']);
});

test('пустой или сломанный атрибут не роняет перерисовку', () => {
  assert.deepEqual(obManagedHiddenColumnNames(tbodyWith('')), []);
  assert.deepEqual(obManagedHiddenColumnNames(tbodyWith('не json')), []);
  assert.deepEqual(obManagedHiddenColumnNames(tbodyWith('{"Сумма":true}')), []);
  assert.deepEqual(obManagedHiddenColumnNames(tbodyWith('["",  " "]')), []);
  assert.deepEqual(obManagedHiddenColumnNames(null), []);
});

test('прячется ячейка выбранной колонки, а не соседняя', () => {
  const row = rowWithCells(3);
  obManagedHideRowColumns(row, ['Количество', 'Цена', 'Сумма'], ['Сумма'], 0);
  assert.equal(row.cells[0].style.display, undefined);
  assert.equal(row.cells[1].style.display, undefined);
  assert.equal(row.cells[2].style.display, 'none');
});

// У табличной части с командами первой идёт служебная ячейка с галочкой
// выделения: без сдвига спряталась бы не та колонка.
test('служебная ячейка выделения учитывается сдвигом', () => {
  const row = rowWithCells(4);
  obManagedHideRowColumns(row, ['Количество', 'Цена', 'Сумма'], ['Количество'], 1);
  assert.equal(row.cells[0].style.display, undefined, 'спрятана галочка выделения');
  assert.equal(row.cells[1].style.display, 'none');
});

test('без скрытых колонок строка не трогается', () => {
  const row = rowWithCells(2);
  obManagedHideRowColumns(row, ['Количество', 'Цена'], [], 0);
  assert.equal(row.cells[0].style.display, undefined);
  assert.equal(row.cells[1].style.display, undefined);
});
