// hidden_when у колонки таблицы формы: показ колонки меняется после события
// формы, данные строки — нет.
//
// Берём срезы managed.js тем же приёмом, что соседние тесты: для SlickGrid —
// функции верхнего уровня с поддельной таблицей, для ValueTable — функцию
// переключения с минимальным DOM.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');
function slice(from, to) {
  const start = source.indexOf(from);
  const end = source.indexOf(to, start);
  assert.ok(start >= 0 && end > start, 'срез не найден: ' + from);
  return source.slice(start, end);
}

globalThis.window = {};
globalThis.document = {querySelectorAll() { return globalThis.__bodies || []; }};
vm.runInThisContext(
  slice('function obGridParseOrder', '// Grids are stored in window._obGrids') +
  slice('function obManagedParseFieldMeta', 'function obManagedAdjacentTableBody') +
  slice('function obManagedSetVTColumnHidden', '// Shared by the managed-form response renderer'),
  {filename: 'managed-column-hidden-when.js'});

function gridState(columns, active) {
  const calls = [];
  return {
    calls,
    columns,
    grid: {
      updateColumns() { calls.push('updateColumns'); },
      getActiveCell() { return active || null; },
      setActiveCell(row, cell) { calls.push(['setActiveCell', row, cell]); },
      getEditorLock() { return {isActive() { return false; }}; },
      getColumns() { return columns; },
      setColumns() { calls.push('setColumns'); },
    },
  };
}

test('колонка с hidden_when прячется и показывается по пути размещения', () => {
  const street = {id: 'Улица'};
  const repr = {id: 'Представление', obElPath: '1.1', hidden: true};
  const state = gridState([street, repr]);

  assert.deepEqual(obGridSetColumnHidden(state, '1.1', false), {matched: true, changed: true});
  assert.equal(repr.hidden, false);
  assert.deepEqual(state.calls, ['updateColumns']);

  // То же состояние повторно — шапку не перерисовываем.
  assert.deepEqual(obGridSetColumnHidden(state, '1.1', false), {matched: true, changed: false});
  assert.deepEqual(state.calls, ['updateColumns']);

  assert.deepEqual(obGridSetColumnHidden(state, '1.1', true), {matched: true, changed: true});
  assert.equal(repr.hidden, true);

  // Чужой путь — не колонка этой таблицы.
  assert.deepEqual(obGridSetColumnHidden(state, '2', true), {matched: false, changed: false});
  assert.equal(street.hidden, undefined);
});

test('активная ячейка из скрытой колонки переезжает в первую видимую той же строки', () => {
  const state = gridState([
    {id: 'Скрытая', obElPath: '1.0'},
    {id: 'Улица'},
    {id: 'Представление'},
  ], {row: 3, cell: 0});
  obGridSetColumnHidden(state, '1.0', true);
  assert.deepEqual(state.calls, ['updateColumns', ['setActiveCell', 3, 1]]);
});

test('перетаскивание шапки считает только видимые колонки', () => {
  const cols = [{id: 'А'}, {id: 'Б', hidden: true}, {id: 'В'}, {id: 'Г'}];
  assert.equal(obGridVisibleToIndex(cols, 0), 0);
  assert.equal(obGridVisibleToIndex(cols, 1), 2, 'вторая видимая — «В», а не скрытая «Б»');
  assert.equal(obGridVisibleToIndex(cols, 2), 3);
  assert.equal(obGridVisibleToIndex(cols, 3), -1);
});

test('объявленные ширины считаются по видимым колонкам', () => {
  // Видимые 300 + 300 помещаются в 700 — растягивает обычная автоширина;
  // скрытая колонка в 500 px сумму не раздувает.
  const cols = [{obWidth: 300, width: 300}, {obWidth: 300, width: 300}, {obWidth: 500, width: 500, hidden: true}];
  const state = gridState(cols);
  assert.equal(obGridKeepDeclaredWidths(state.grid, 700), false);
});

function vtTable(flags, cond, rows) {
  const th = {style: {display: 'none'}};
  const tbody = {
    attrs: {'data-vt-fields': 'Улица|string,Представление|string,Ид|string', 'data-vt-flags': flags, 'data-vt-cond': cond},
    getAttribute(name) { return this.attrs[name] || null; },
    setAttribute(name, value) { this.attrs[name] = value; },
    rows: rows.map(() => ({cells: [0, 1, 2].map(() => ({colSpan: 1, style: {display: ''}}))})),
    closest() { return {querySelector(sel) { return sel === 'th[data-ob-vt-col="1.1"]' ? th : null; }}; },
  };
  return {th, tbody};
}

test('колонка ValueTable: шапка, ячейки и флаг перестройки строк', () => {
  const {th, tbody} = vtTable('Улица:r,Представление:rh,Ид:rh', 'Представление=1.1', [{}, {}]);
  globalThis.__bodies = [tbody];

  assert.equal(obManagedSetVTColumnHidden('1.1', false), true);
  assert.equal(th.style.display, '');
  assert.equal(tbody.attrs['data-vt-flags'], 'Улица:r,Представление:r,Ид:rh');
  tbody.rows.forEach((tr) => assert.equal(tr.cells[1].style.display, ''));
  assert.equal(tbody.rows[0].cells[2].style.display, '', 'служебную колонку Ид не трогаем');

  assert.equal(obManagedSetVTColumnHidden('1.1', true), true);
  assert.equal(th.style.display, 'none');
  assert.equal(tbody.attrs['data-vt-flags'], 'Улица:r,Представление:rh,Ид:rh');
  tbody.rows.forEach((tr) => assert.equal(tr.cells[1].style.display, 'none'));

  assert.equal(obManagedSetVTColumnHidden('3', true), false, 'чужой путь');
});

test('строка-заглушка пустой ValueTable не ломается', () => {
  const {tbody} = vtTable('Улица:r,Представление:rh,Ид:rh', 'Представление=1.1', []);
  tbody.rows = [{cells: [{colSpan: 2, style: {display: ''}}]}];
  globalThis.__bodies = [tbody];
  assert.equal(obManagedSetVTColumnHidden('1.1', false), true);
  assert.equal(tbody.rows[0].cells[0].style.display, '');
});
