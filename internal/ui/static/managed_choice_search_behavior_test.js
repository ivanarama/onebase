const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const source = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('  function refId(');
const end = source.indexOf('  function ObEnumEditor(', start);
assert.ok(start >= 0 && end > start);
function element(tag) {
  const attrs = {};
  return {
    tagName: tag.toUpperCase(), style: {}, children: [], listeners: {}, value: '', textContent: '',
    setAttribute(k, v) { attrs[k] = String(v); }, getAttribute(k) { return attrs[k] ?? null; },
    appendChild(child) { this.children.push(child); child.parentElement = this; return child; },
    remove() { if (this.parentElement) this.parentElement.children = this.parentElement.children.filter(c => c !== this); this.parentElement = null; },
    set innerHTML(value) { assert.equal(value, ''); this.children = []; },
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
    dispatch(type, data = {}) { for (const fn of this.listeners[type] || []) fn({target: this, preventDefault() {}, stopPropagation() {}, ...data}); },
    focus() { this.dispatch('focus'); }, select() {}, closest() { return {}; },
    querySelectorAll() { return this.children.filter(c => c.getAttribute('data-idx') !== null); },
    getBoundingClientRect() { return {left: 0, bottom: 0, width: 200}; },
  };
}
function runtime(context = false) {
  const document = {body: element('body'), createElement: element, addEventListener() {}, removeEventListener() {}};
  const requests = [], timers = new Map(); let timerID = 0;
  const fetch = url => new Promise(resolve => requests.push({url, resolve}));
  const window = {fetch};
  const opts = [{id: 'local', _label: 'д.15'}, {id: 'excluded', _label: 'д.150'}];
  const column = {field: 'Дом', refEntity: 'Дом'};
  if (context) {
    column.choiceContext = 'context'; column.choiceSeeds = {get() { return {fingerprint: 'same', items: opts}; }};
    window.obTPChoiceCarrier = () => ({});
    window.obRefChoiceSnapshot = () => ({query: '&form=address', fingerprint: 'same'});
  }
  const args = {column, item: {Дом: ''}, row: 0, container: element('div'), commitChanges() {}};
  const factory = new Function('document', 'window', 'fetch', 'setTimeout', 'clearTimeout', 'args', 'opts',
    source.slice(start, end) + '\nreturn new ObRefEditor("Дом", opts, args);');
  const editor = factory(document, window, fetch,
    (fn, ms) => { const id = ++timerID; timers.set(id, {fn, ms}); return id; }, id => timers.delete(id), args, opts);
  const input = args.container.children[0].children[0];
  function type(text) { input.value = text; input.dispatch('input'); }
  function send() { for (const [id, timer] of [...timers]) if (timer.ms === 180) { timers.delete(id); timer.fn(); } }
  async function respond(index, data, status = 200) {
    requests[index].resolve({ok: status === 200, status, json: async () => data});
    await new Promise(resolve => setImmediate(resolve));
  }
  const labels = () => document.body.children.flatMap(list => list.children.filter(c => c.getAttribute('data-id') !== null).map(c => c.textContent));
  const message = () => document.body.children.flatMap(list => list.children.map(c => c.textContent)).join('\n');
  return {editor, input, type, send, respond, labels, message};
}
for (const context of [false, true]) {
  test(`matched controls membership and order (context=${context})`, async () => {
    const r = runtime(context); r.type('15'); r.send();
    await r.respond(0, {matched: true, items: [{id: 'exact', _label: 'д.15, корп.1'}, {id: 'local', _label: 'д.15'}]});
    assert.deepEqual(r.labels(), ['д.15, корп.1', 'д.15']);
    r.input.dispatch('keydown', {key: 'Enter'}); assert.equal(r.editor.getValue(), 'exact');
  });
  test(`empty matched blocks preload and seeds (context=${context})`, async () => {
    const r = runtime(context); r.type('15'); r.send(); await r.respond(0, {matched: true, items: []});
    assert.deepEqual(r.labels(), []); r.input.dispatch('keydown', {key: 'Enter'});
    assert.equal(r.editor.getValue(), ''); assert.equal(r.editor.validate().valid, false);
  });
}
test('422 shows error and blocks Enter/Tab until current success', async () => {
  const r = runtime(); r.type('15'); r.send(); await r.respond(0, {error: 'Search unavailable', items: []}, 422);
  assert.deepEqual(r.labels(), []); assert.match(r.message(), /Search unavailable/);
  r.input.dispatch('keydown', {key: 'Enter'}); assert.equal(r.editor.getValue(), '');
  assert.equal(r.editor.validate().valid, false); assert.equal(r.editor.serializeValue(), '');
  r.type('15'); r.send(); await r.respond(1, {matched: true, items: [{id: 'recovered', _label: 'д.15, корп.1'}]});
  r.input.dispatch('keydown', {key: 'Enter'}); assert.equal(r.editor.getValue(), 'recovered');
});
test('late success and 422 cannot replace newer input before debounce', async () => {
  for (const status of [200, 422]) {
    const r = runtime(); r.type('15'); r.send(); r.type('150');
    await r.respond(0, status === 200 ? {matched: true, items: [{id: 'stale', _label: 'old'}]} : {error: 'stale error'}, status);
    assert.deepEqual(r.labels(), ['д.150']); assert.doesNotMatch(r.message(), /stale|old/);
  }
});
test('ordinary substring retains preload compatibility', async () => {
  const r = runtime(); r.type('15'); r.send(); await r.respond(0, {items: [{id: 'more', _label: 'д.151'}]});
  assert.deepEqual(r.labels(), ['д.15', 'д.150', 'д.151']);
});

test('whitespace preserves authoritative empty matched on Tab validation', async () => {
  const r = runtime(); r.type(' 15 '); r.send(); await r.respond(0, {matched: true, items: []});
  assert.deepEqual(r.labels(), []); assert.equal(r.editor.validate().valid, false); assert.equal(r.editor.serializeValue(), '');
});
