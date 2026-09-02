// Event round-trip for a managed table part must update the UUID-to-label map
// before SlickGrid renders the returned rows. Exercise the real helpers and
// formatter from managed.js; a structural source check alone would miss the
// closure identity that caused the regression.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');

const source = fs.readFileSync('static/managed.js', 'utf8');

function slice(startMarker, endMarker) {
  const start = source.indexOf(startMarker);
  if (start < 0) throw new Error('не найдено начало среза: ' + startMarker);
  const end = source.indexOf(endMarker, start);
  if (end < 0) throw new Error('не найден конец среза: ' + endMarker);
  return source.slice(start, end);
}

global.window = {};
new Function(slice('function obManagedSyncRefOptionMap', '// Отправляет текущие form-values'))();

const buildColumns = new Function(
  'ObRefEditor', 'Slick', 'obManagedEscapeHTML',
  slice('  function buildColumns(', '  function commitGridEdit(') + '\nreturn buildColumns;'
)(function ObRefEditor() {}, {Editors: {Checkbox: function Checkbox() {}}}, String);

test('event options update the live SlickGrid formatter and editor array', () => {
  const refOptions = {Товар: [{id: 'old', _label: 'Старый'}]};
  const editorOptions = refOptions.Товар;
  const columns = buildColumns([{id: 'Товар', name: 'Товар', ref: 'Товары'}], refOptions, {}, {});
  const gridState = {tpName: 'Строки', refOpts: refOptions};
  window._tpRefOpts = {Строки: refOptions};
  window._obGridViews = [gridState];
  window._obGrids = {Строки: gridState};

  window.obManagedApplyTablePartRefOptions({
    Строки: {Товар: [{id: 'new-id', _label: 'Новая подпись'}]},
  });

  assert.strictEqual(refOptions.Товар, editorOptions, 'заменён массив, удерживаемый редактором');
  assert.deepEqual(editorOptions, [{id: 'new-id', _label: 'Новая подпись'}]);
  assert.match(columns[0].formatter(0, 0, 'new-id'), /Новая подпись/);
});

test('event options clear a field without breaking its retained array', () => {
  const retained = [{id: 'secret', _label: 'Скрытая подпись'}];
  window._tpRefOpts = {Строки: {Товар: retained}};
  window._obGridViews = [];
  window._obGrids = {};

  window.obManagedApplyTablePartRefOptions({Строки: {Товар: []}});

  assert.strictEqual(window._tpRefOpts.Строки.Товар, retained);
  assert.deepEqual(retained, []);
});

test('event path applies reference options before table-part rows', () => {
  const options = source.indexOf('window.obManagedApplyTablePartRefOptions(data.tpRefOptions);');
  const rows = source.indexOf('window.applyTableParts(data.tableparts);', options);
  assert.ok(options >= 0 && rows > options, 'tpRefOptions применяются после строк или не применяются вовсе');
});
