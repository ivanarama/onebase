// Consume the context and source controls from a public GET form. The grid
// uses its production carrier; no_grid reads the live named form controls.
const fs = require('node:fs');
const vm = require('node:vm');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const ui = fs.readFileSync('static/ui.js', 'utf8');
const column = input.column || 'Глобальная';
const form = {elements: {namedItem(name) { return input.controls.find(c => c.name === name) || null; }}};
function select() {
  const attrs = {};
  return {name: 'tp.Строки.0.' + column, form, value: input.selected,
    getAttribute(k) { return attrs[k] ?? null; }, setAttribute(k, v) { attrs[k] = String(v); }};
}
const window = {};
const sandbox = {window, document: {createElement: select, getElementsByName() { return []; }},
  JSON, Array, Object, String, encodeURIComponent, obRefFilterParam() { return ''; }};
vm.createContext(sandbox);
vm.runInContext(ui.slice(ui.indexOf('function obRefChoiceSnapshot(sel)'), ui.indexOf('function obChoiceSelectIsLive(sel)')), sandbox);
let carrier;
if (input.grid) {
  carrier = window.obTPChoiceCarrier(input.context, input.row || {[column]: input.selected}, 0, form, column, 'Неисправность', '');
} else {
  carrier = select(); carrier.setAttribute('data-ref-choice-context', input.context); carrier.setAttribute('data-ref-row-id', '0');
}
process.stdout.write(window.obRefChoiceSnapshot(carrier).query);
