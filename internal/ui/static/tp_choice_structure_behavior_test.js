const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');
const ui = fs.readFileSync('static/ui.js', 'utf8');
function runtime() {
  let form, table;
  function node(tag) {
    const attrs = {};
    const el = {tagName: tag.toUpperCase(), children: [], style: {}, value: '', options: [], isConnected: true,
      getAttribute(k) { return attrs[k] ?? null; }, setAttribute(k, v) { attrs[k] = String(v); },
      hasAttribute(k) { return k in attrs; }, removeAttribute(k) { delete attrs[k]; },
      appendChild(child) { child.remove(); this.children.push(child); child.parentElement = this; return child; },
      insertBefore(child, next) { child.remove(); const i = next ? this.children.indexOf(next) : this.children.length; this.children.splice(i, 0, child); child.parentElement = this; },
      remove() { if (this.parentElement) { this.parentElement.children.splice(this.parentElement.children.indexOf(this), 1); this.parentElement = null; } },
      querySelectorAll(selector) { return selector === '[name]' ? this.children : []; },
      querySelector() { return null; }, focus() {}, addEventListener() {}, dispatchEvent() {},
      closest(selector) { if (selector === 'form') return form; if (selector === 'tr') return this.tagName === 'TR' ? this : this.parentElement;
        return selector === 'table[data-ob-dom-table]' ? table : null; }, classList: {toggle() {}}};
    Object.defineProperty(el, 'name', {get() { return attrs.name || ''; }, set(v) { attrs.name = v; }});
    Object.defineProperty(el, 'nextSibling', {get() { const p = this.parentElement; return p ? p.children[p.children.indexOf(this) + 1] || null : null; }});
    return el;
  }
  const body = node('tbody');
  Object.defineProperty(body, 'rows', {get() { return body.children; }});
  table = node('table'); table.tBodies = [body]; table.setAttribute('data-ob-dom-table', 'Строки'); table.setAttribute('data-ob-readonly', '0'); table.contains = () => false;
  form = {elements: {namedItem(name) { return body.rows.flatMap(r => r.children).find(c => c.name === name) || null; }}};
  const context = JSON.stringify({form_entity: 'Заявка', form: 'Объекта', element: 'Строки.local', table_part: 'Строки', sources: {'Строки.Направление': 'Направление'}});
  for (const [i, value] of ['A', 'B'].entries()) {
    const row = node('tr'); body.appendChild(row);
    const source = node('input'); source.name = `tp.Строки.${i}.Направление`; source.value = value; row.appendChild(source);
    const select = node('select'); select.name = `tp.Строки.${i}.Локальная`; select.value = value + '-selected'; select.form = form;
    select.setAttribute('data-ref-row-id', i); select.setAttribute('data-ref-choice-context', context); select.setAttribute('data-ref-entity', 'Цель'); row.appendChild(select);
  }
  const pending = [], applied = [], choices = [];
  const window = {fetch(url) { const job = {url}; job.promise = new Promise(resolve => { job.resolve = resolve; }); pending.push(job); return job.promise; }};
  const document = {activeElement: null, querySelectorAll() { return choices; }, contains() { return true; }, getElementById() { return null; }, getElementsByName(name) { const c = form.elements.namedItem(name); return c ? [c] : []; }};
  const sandbox = {window, document, JSON, Array, Object, String, Promise, Error, encodeURIComponent,
    obReady() {}, obElementVisible() { return true; }, obIsTypingTarget() { return false; }};
  vm.createContext(sandbox);
  vm.runInContext(ui.slice(ui.indexOf('function obDOMTableFromTarget'), ui.indexOf('function obInitDOMTables')), sandbox);
  vm.runInContext(ui.slice(ui.indexOf('function obRefChoiceSnapshot(sel)'), ui.indexOf('function obAttachTPChoiceContexts')), sandbox);
  sandbox.obRefFilterParam = () => '';
  sandbox.obChoiceApplyResponse = (sel, data) => applied.push(data.marker);
  function shortcut(row, key, ctrlKey = false) {
    document.activeElement = row;
    assert.equal(sandbox.obHandleDOMTableShortcut({target: row, key, ctrlKey, preventDefault() {}, stopPropagation() {}}), true);
  }
  function snapshot(row) { return new URLSearchParams(window.obRefChoiceSnapshot(row.children[1]).query); }
  return {body, pending, window, choices, applied, shortcut, snapshot};
}
test('Ctrl+Down, Ctrl+Up and Delete keep row-local picker context current without change events', () => {
  const env = runtime(), a = env.body.rows[0], b = env.body.rows[1];
  env.shortcut(a, 'ArrowDown', true);
  assert.equal(a.children[1].name, 'tp.Строки.1.Локальная');
  assert.equal(env.snapshot(a).get('row_id'), '1');
  assert.deepEqual(JSON.parse(env.snapshot(a).get('sources')), {'Строки.Направление': 'A'});
  env.shortcut(a, 'ArrowUp', true); assert.equal(env.snapshot(a).get('row_id'), '0');
  env.shortcut(a, 'Delete'); assert.equal(env.body.rows[0], b);
  assert.equal(env.snapshot(b).get('row_id'), '0');
  assert.deepEqual(JSON.parse(env.snapshot(b).get('sources')), {'Строки.Направление': 'B'});
});
for (const action of ['move', 'delete']) {
  test(`a pending response from before ${action} is discarded and retried with the current row`, async () => {
    const env = runtime(), a = env.body.rows[0], b = env.body.rows[1];
    env.choices.push(b.children[1]);
    const job = env.window.obRefreshChoiceFilters();
    env.shortcut(a, action === 'move' ? 'ArrowDown' : 'Delete', action === 'move');
    env.pending[0].resolve({ok: true, json: async () => ({marker: 'stale'})});
    await new Promise(setImmediate); assert.equal(env.pending.length, 2);
    const query = new URLSearchParams(env.pending[1].url.split('?')[1]);
    assert.equal(query.get('row_id'), '0');
    assert.deepEqual(JSON.parse(query.get('sources')), {'Строки.Направление': 'B'}); assert.deepEqual(env.applied, []);
    env.pending[1].resolve({ok: true, json: async () => ({marker: 'current'})}); await job;
    assert.deepEqual(env.applied, ['current']);
  });
}
