// Feed attributes from the real HTTP form through the shipped row renderers.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
let body;
function node(tag) {
  const attrs = {};
  const el = {tagName: tag.toUpperCase(), children: [], style: {},
    getAttribute(k) { return attrs[k] ?? null; }, setAttribute(k, v) { attrs[k] = String(v); },
    appendChild(c) { this.children.push(c); c.parentElement = this; return c; },
    closest() { return null; }, querySelectorAll() { return []; }, addEventListener() {}};
  Object.defineProperty(el, 'innerHTML', {set() { this.children = []; }});
  Object.defineProperty(el, 'rows', {get() { return this.children; }});
  return el;
}
body = node('tbody');
for (const [k, v] of Object.entries(input.attrs)) body.setAttribute(k, v);
function all(el) { return el.children.flatMap(c => [c, ...all(c)]); }
const refOptions = {Строки: {Клиент: [{id: input.client, _label: 'Client'}]}};
const refMeta = {Строки: {Клиент: {entity: 'Клиент'}}};
const window = {_tpRefOpts: refOptions, _tpEnumLabels: {}, _tpEnumOrder: {}, obManagedSetTablePartJSON() {}};
const document = {createElement: node, getElementById() { return body; }};
const sandbox = {window, document, JSON, Array, Object, String, encodeURIComponent,
  obReady() {}, obTPRefOpts() { return refOptions; }, obTPRefMeta() { return refMeta; },
  obTPEnumLabels() { return {}; }, obTPEnumOrder() { return {}; },
  obManagedTableBodies() { return [body]; }, obManagedVirtualColumnNames() { return []; },
  obManagedHiddenColumnNames() { return []; }, obManagedTableReadOnly() { return input.readonly; },
  obFormRowClass() { return ''; }, obFormCellClass() { return ''; }};
vm.createContext(sandbox);
const ui = fs.readFileSync('static/ui.js', 'utf8');
vm.runInContext(ui.slice(ui.indexOf('function addTpRow('), ui.indexOf('function recalcTpRow(')), sandbox);
function check(phase, readonly) {
  const controls = all(body);
  const card = controls.filter(c => c.getAttribute('data-ob-ref-current') !== null || c.getAttribute('data-ob-ref-current-self') !== null);
  assert.equal(card.length, input.want ? 1 : 0, phase + ': card policy');
  const picker = controls.find(c => c.getAttribute('data-ob-ref-picker') !== null || c.getAttribute('data-ob-ref-picker-self') !== null);
  assert.ok(picker, phase + ': picker preserved');
  assert.equal(!!picker.disabled, readonly, phase + ': readonly picker');
  if (card.length) assert.ok(!card[0].disabled, phase + ': readonly navigation');
  assert.equal(controls.filter(c => c.tagName === 'SELECT').length, 1);
}
// New rows only exist for writable tables.
if (!input.readonly) { sandbox.addTpRow('Строки', ['Клиент'], [], 0, body); check('add', false); }
const managed = fs.readFileSync('static/managed.js', 'utf8');
vm.runInContext(managed.slice(managed.indexOf('  function applyTableParts(tps)'), managed.indexOf('  // applyFormTables(vts)')), sandbox);
for (let i = 0; i < 2; i++) { window.applyTableParts({Строки: [{Клиент: input.client}]}); check('redraw ' + i, input.readonly); }
