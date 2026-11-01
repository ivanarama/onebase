// Replay an actual public form's table metadata and event response through the
// shipped no-grid renderer and choice snapshot. Go sends the resulting queries
// to the public HTTP endpoint on both database dialects.
const fs = require('node:fs');
const vm = require('node:vm');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const ui = fs.readFileSync('static/ui.js', 'utf8');
const managed = fs.readFileSync('static/managed.js', 'utf8');
function node(tag) {
  const attrs = {};
  const el = {tagName: tag.toUpperCase(), style: {}, children: [], value: '',
    setAttribute(k, v) { attrs[k] = String(v); }, getAttribute(k) { return attrs[k] ?? null; },
    appendChild(child) { this.children.push(child); child.parentElement = this; return child; },
    closest(selector) { return selector === 'form' ? form : null; },
    addEventListener() {}, querySelectorAll() { return []; }};
  Object.defineProperty(el, 'innerHTML', {set() { el.children = []; }});
  return el;
}
function descendants(el) { return el.children.flatMap(c => [c, ...descendants(c)]); }
const body = node('tbody');
for (const [k, v] of Object.entries(input.attrs)) body.setAttribute(k, v);
const globalControls = input.controls.map(c => Object.assign(node('input'), c));
const form = {elements: {namedItem(name) { return [...globalControls, ...descendants(body)].find(c => c.name === name) || null; }},
  querySelector(selector) { return this.elements.namedItem(selector.slice(7, -2)); }};
const window = {_tpRefOpts: input.refOptions, _tpEnumLabels: {}, _tpEnumOrder: {},
  obManagedSetTablePartJSON() {}};
const document = {createElement: node, getElementsByName(name) { const c = form.elements.namedItem(name); return c ? [c] : []; }};
const sandbox = {window, document, JSON, Array, Object, String, encodeURIComponent,
  obReady() {}, obTPRefMeta() { return input.refMeta; },
  obManagedTableBodies() { return [body]; }, obManagedVirtualColumnNames() { return []; },
  obManagedHiddenColumnNames() { return []; }, obManagedTableReadOnly() { return false; },
  obFormRowClass() { return ''; }, obFormCellClass() { return ''; }};
vm.createContext(sandbox);
vm.runInContext(ui.slice(ui.indexOf('function obRefFilterValues(sel)'), ui.indexOf('// obRefreshDependentSelects')), sandbox);
vm.runInContext(ui.slice(ui.indexOf('function obRefChoiceSnapshot(sel)'), ui.indexOf('function obChoiceSelectIsLive(sel)')), sandbox);
vm.runInContext(managed.slice(managed.indexOf('  function applyTableParts(tps)'), managed.indexOf('  // applyFormTables(vts)')), sandbox);
window.applyTableParts(input.tableparts);
// A second server reply must preserve metadata too, after old rows are gone.
window.applyTableParts(input.tableparts);
// Model all select values, including the leading row-local source control.
for (const c of descendants(body).filter(c => c.tagName === 'SELECT')) {
  c.form = form;
  const selected = c.children.find(o => o.selected);
  c.value = selected ? selected.value : '';
}
const queries = descendants(body).filter(c => c.getAttribute('data-ref-choice-context')).map(c => {
  return {name: c.name, query: window.obRefChoiceSnapshot(c).query, filter: c.getAttribute('data-ref-filter')};
});
process.stdout.write(JSON.stringify(queries));
