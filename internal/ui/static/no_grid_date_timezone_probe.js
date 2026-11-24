// Real HTTP attributes -> shipped addTpRow/applyTableParts -> POST values.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
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
const body = node('tbody');
for (const [k, v] of Object.entries(input.attrs)) body.setAttribute(k, v);
const document = {createElement: node, getElementById() { return body; }};
const window = {_tpRefOpts: {}, _tpEnumLabels: {}, _tpEnumOrder: {}, obManagedSetTablePartJSON() {}};
const sandbox = {window, document, JSON, Array, Object, String, Date, isNaN,
  obReady() {}, obTPRefOpts() { return {}; }, obTPRefMeta() { return {}; },
  obTPEnumLabels() { return {}; }, obTPEnumOrder() { return {}; },
  obManagedTableBodies() { return [body]; }, obManagedVirtualColumnNames() { return []; },
  obManagedHiddenColumnNames() { return []; }, obManagedTableReadOnly() { return input.readonly; },
  obFormRowClass() { return ''; }, obFormCellClass() { return ''; }};
vm.createContext(sandbox);
const ui = fs.readFileSync('static/ui.js', 'utf8');
const managed = fs.readFileSync('static/managed.js', 'utf8');
function load(source, start, end) {
  const a = source.indexOf(start), b = source.indexOf(end, a);
  assert.ok(a >= 0 && b > a, 'production function boundaries');
  vm.runInContext(source.slice(a, b), sandbox);
}
load(ui, 'function addTpRow(', 'function recalcTpRow(');
load(managed, '  function obManagedSplitDate(', '  // obManagedFormatDate');
load(managed, '  function applyTableParts(tps)', '  // applyFormTables(vts)');
function descendants(el) { return el.children.flatMap(c => [c, ...descendants(c)]); }
function dateControl(index) {
  const controls = descendants(body).filter(c => c.name === `tp.Lines.${index}.At`);
  assert.equal(controls.length, 1, 'actual row date control');
  return controls[0];
}
let current = input.initial;
if (input.managed) {
  assert.match(input.attrs['data-tp-fields'], /At\|date/);
  for (let i = 0; i < 2; i++) {
    window.applyTableParts({Lines: [{At: input.value}]});
    const el = dateControl(0);
    assert.equal(el.type, input.readonly ? 'text' : 'datetime-local', 'redraw input type');
    assert.equal(el.value, input.readonly ? input.display : input.want, 'redraw local date');
    assert.equal(!!el.disabled, input.readonly, 'readonly redraw');
    if (!input.readonly) assert.equal(el.step, '1', 'redraw seconds');
    current = el.value;
  }
}
let added = '';
if (!input.readonly) {
  let fields, dates;
  if (input.managed) {
    const meta = input.attrs['data-tp-fields'].split(',').map(s => s.split('|'));
    fields = meta.map(([name]) => name); dates = meta.filter(([, type]) => type === 'date').map(([name]) => name);
  } else {
    fields = input.add['data-tp-fields'].split(','); dates = input.add['data-tp-date-fields'].split(',');
  }
  sandbox.addTpRow('Lines', fields, [], 1, body, [], [], dates);
  const el = dateControl(1);
  assert.equal(el.type, 'datetime-local', 'added row date type');
  assert.equal(el.step, '1', 'added row seconds');
  assert.ok(!el.value, 'new date is empty');
  el.value = input.want; // User enters the displayed local value in the new row.
  added = el.value;
}
// Go independently checks the persisted instant after POSTing these values.
process.stdout.write(JSON.stringify({Current: current, Added: added}));
