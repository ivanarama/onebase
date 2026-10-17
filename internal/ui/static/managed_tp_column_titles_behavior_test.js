// Заголовки колонок из реквизита формы (title_from) после события формы.
//
// Берём срез managed.js с обработчиком columnTitles — тем же приёмом, что
// соседние тесты; вместо SlickGrid — запись вызовов updateColumnHeader.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');
const start = source.indexOf('function obEscapeColumnTitle');
const end = source.indexOf('window.obManagedApplyColumnTitles = obManagedApplyColumnTitles;', start);
assert.ok(start >= 0 && end > start, 'срез обработчика заголовков не найден');

globalThis.window = {};
vm.runInThisContext(source.slice(start, end) + 'window.obManagedApplyColumnTitles = obManagedApplyColumnTitles;',
  {filename: 'managed-column-titles.js'});

function gridState(tp, ids) {
  const calls = [];
  return {
    calls,
    div: {getAttribute(name) { return name === 'data-sg-tp' ? tp : null; }},
    columns: ids.map((id) => ({id, name: id})),
    grid: {updateColumnHeader(id, title) { calls.push([id, title]); }},
  };
}

test('заголовок колонки меняется и в описании, и в нарисованной шапке', () => {
  const days = gridState('Дни', ['День1', 'День2', 'Мастер']);
  window._obGrids = {Дни: days};
  window._obGridViews = [days];
  window.obManagedApplyColumnTitles({Дни: {День1: '1 пн', День2: '2 вт'}});
  assert.deepEqual(days.calls, [['День1', '1 пн'], ['День2', '2 вт']]);
  assert.equal(days.columns[0].name, '1 пн');
  assert.equal(days.columns[2].name, 'Мастер', 'колонку без title_from не трогаем');
});

test('текст реквизита экранируется: шапка SlickGrid рисует подпись как HTML', () => {
  const days = gridState('Дни', ['День1']);
  window._obGrids = {Дни: days};
  window._obGridViews = [days];
  window.obManagedApplyColumnTitles({Дни: {День1: '<img src=x onerror=1>&'}});
  assert.deepEqual(days.calls, [['День1', '&lt;img src=x onerror=1&gt;&amp;']]);
});

test('все экземпляры сетки табличной части, а чужая табличная часть — нет', () => {
  const writable = gridState('Дни', ['День1']);
  const summary = gridState('Дни', ['День1']);
  const other = gridState('Отклонения', ['День1']);
  window._obGrids = {Дни: writable, Отклонения: other};
  window._obGridViews = [summary];
  window.obManagedApplyColumnTitles({Дни: {День1: '1 пн'}});
  assert.deepEqual(writable.calls, [['День1', '1 пн']]);
  assert.deepEqual(summary.calls, [['День1', '1 пн']]);
  assert.deepEqual(other.calls, []);
});

test('тот же заголовок шапку не перерисовывает, пустой ответ ничего не ломает', () => {
  const days = gridState('Дни', ['День1']);
  days.columns[0].name = '1 пн';
  window._obGrids = {Дни: days};
  window._obGridViews = [days];
  window.obManagedApplyColumnTitles({Дни: {День1: '1 пн'}});
  window.obManagedApplyColumnTitles(undefined);
  window.obManagedApplyColumnTitles({Нет: {День1: 'x'}});
  assert.deepEqual(days.calls, []);
});
