// Replay actual GET metadata and form-event data through both shipped table
// renderers. Return the ordinary options and filtered picker snapshot to Go.
const fs = require('node:fs');
const vm = require('node:vm');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const ui = fs.readFileSync('static/ui.js', 'utf8');
const managed = fs.readFileSync('static/managed.js', 'utf8');
function node(tag = 'div') {
  const attrs = {};
  const el = {tagName: tag.toUpperCase(), style: {}, children: [], value: '', offsetParent: {},
    setAttribute(k, v) { attrs[k] = String(v); }, getAttribute(k) { return attrs[k] ?? null; },
    appendChild(child) { this.children.push(child); child.parentElement = this; return child; },
    closest(selector) { return selector === 'form' ? form : null; },
    addEventListener() {}, querySelectorAll() { return []; }};
  Object.defineProperty(el, 'innerHTML', {set() { el.children = []; }});
  return el;
}
function descendants(el) { return el.children.flatMap(c => [c, ...descendants(c)]); }
const hosts = input.placements.map(p => {
  const el = node(input.grid ? 'div' : 'tbody');
  for (const [k, v] of Object.entries(p.attrs)) el.setAttribute(k, v);
  return el;
});
const controls = input.controls.map(c => Object.assign(node('input'), c));
const form = {elements: {namedItem(name) { return [...controls, ...hosts.flatMap(descendants)].find(c => c.name === name) || null; }}};
const window = {_tpRefOpts: input.initialRefOptions, _tpEnumLabels: {}, _tpEnumOrder: {},
  _obGrids: {}, addEventListener() {}, obManagedSetTablePartJSON() {}};
const document = {readyState: 'complete', activeElement: null, createElement: node,
  getElementsByName(name) { const c = form.elements.namedItem(name); return c ? [c] : []; },
  querySelectorAll(selector) { return selector === '.ob-grid[data-sg-tp]' ? hosts : []; },
  getElementById() { return null; }, addEventListener() {}, contains() { return true; }};
function slot() { return {subscribe() {}}; }
function DataView() { this.items = []; this.onRowCountChanged = slot(); this.onRowsChanged = slot(); }
DataView.prototype.setItems = function(items) { this.items = items; };
DataView.prototype.getItems = function() { return this.items; };
function Grid(div, dataView, columns, options) {
  this.options = options;
  for (const name of ['onSort', 'onCellChange', 'onValidationError', 'onKeyDown']) this[name] = slot();
}
Grid.prototype.getOptions = function() { return this.options; };
Grid.prototype.getFooterRowColumn = function() { return null; };
for (const name of ['resizeCanvas', 'autosizeColumns', 'updateRowCount', 'render', 'invalidate']) Grid.prototype[name] = function() {};
Grid.prototype.getActiveCell = function() { return null; };
Grid.prototype.getEditorLock = function() { return {isActive() { return false; }}; };
const sandbox = {window, document, JSON, Array, Object, String, WeakMap, Promise, encodeURIComponent,
  Slick: {Data: {DataView}, Grid, Editors: {Text: function() {}}},
  obReady() {}, obTPRefMeta() { return input.refMeta; },
  obManagedTableBodies() { return hosts; }, obManagedVirtualColumnNames() { return []; },
  obManagedHiddenColumnNames() { return []; }, obManagedTableReadOnly() { return false; },
  obFormRowClass() { return ''; }, obFormCellClass() { return ''; },
  formGridItemMetadata() {}, copyFormGridStyleKeys() {}, obRefFilterParam() { return ''; }};
vm.createContext(sandbox);
function run(source, start, end) {
  const a = source.indexOf(start), b = source.indexOf(end, a);
  if (a < 0 || b < 0) throw new Error('missing production runtime slice: ' + start);
  vm.runInContext(source.slice(a, b), sandbox);
}
run(ui, 'function obRefFilterValues(sel)', '// obRefreshDependentSelects');
run(ui, 'function obRefChoiceSnapshot(sel)', 'function obChoiceSelectIsLive(sel)');
run(managed, 'function obManagedSyncRefOptionMap', '// Отправляет текущие form-values');
if (input.grid) {
  run(managed, '// SlickGrid initializer for managed-form table parts', '\n// Авто-вызов ПриОткрытииФормы');
} else {
  run(managed, '  function applyTableParts(tps)', '  // applyFormTables(vts)');
}
window.obManagedApplyTablePartRefOptions(input.refOptions);
window.applyTableParts(input.tableparts);
window.applyTableParts(input.tableparts);
const snapshots = hosts.map((host, index) => {
  let ids, query = '';
  if (input.grid) {
    const state = host._obGridState;
    if (!state || state.dataView.getItems().length !== 1) throw new Error('grid did not redraw');
    const column = state.columns.find(c => c.field === 'Глобальная');
    const row = state.dataView.getItems()[0];
    if (row.Глобальная !== input.tableparts.Строки[0].Глобальная) throw new Error('grid retained old event row');
    if (column.choiceContext) {
      // After redraw the old per-row seeds must not become a global fallback.
      const seeds = column.choiceSeeds && column.choiceSeeds.get(row);
      ids = seeds ? seeds.items.map(o => o.id) : [];
      const carrier = window.obTPChoiceCarrier(column.choiceContext, row, 0, form, column.field, column.refEntity, column.refFilter);
      query = window.obRefChoiceSnapshot(carrier).query;
    } else {
      ids = state.refOpts.Глобальная.map(o => o.id);
    }
  } else {
    const select = descendants(host).find(c => c.name === 'tp.Строки.0.Глобальная');
    if (!select) throw new Error('selector did not redraw');
    select.form = form;
    select.value = select.children.find(o => o.selected)?.value || '';
    if (select.value !== input.tableparts.Строки[0].Глобальная) throw new Error('selector retained old event value');
    ids = select.children.map(o => o.value).filter(Boolean);
    if (select.getAttribute('data-ref-choice-context')) query = window.obRefChoiceSnapshot(select).query;
  }
  return {name: input.placements[index].name, ids, query};
});
process.stdout.write(JSON.stringify(snapshots));
