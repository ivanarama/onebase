'use strict';
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fixture = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));

function page() {
  const elements = fixture.elements.map(spec => ({
    attrs: {...spec.attrs}, value: spec.attrs.value || '', hidden: 'hidden' in spec.attrs,
    handlers: {}, classList: {add() {}, remove() {}},
    getAttribute(name) { return this.attrs[name] ?? null; },
    setAttribute(name, value) { this.attrs[name] = value; },
    addEventListener(type, callback) { (this.handlers[type] ||= []).push(callback); },
    scrollIntoView() {}, focus() {},
    closest(selector) { assert.equal(selector, 'li[data-login]'); return this; },
    fire(type, properties = {}) {
      const event = {target: this, preventDefault() { this.defaultPrevented = true; }, ...properties};
      for (const handler of this.handlers[type] || []) handler(event);
      return event;
    }
  }));
  const byID = Object.fromEntries(elements.filter(e => e.attrs.id).map(e => [e.attrs.id, e]));
  const input = byID['ob-login'], list = byID['ob-users'], password = byID['ob-password'];
  const rows = elements.filter(e => 'data-login' in e.attrs);
  assert.equal(rows.length, 3, 'users come from LoginPage');
  list.querySelectorAll = selector => { assert.equal(selector, 'li[data-login]'); return rows; };
  const timers = [];
  vm.runInNewContext(fixture.script, {
    document: {getElementById: id => byID[id] || null}, setTimeout: callback => timers.push(callback)
  });
  return {input, list, password, toggle: byID['ob-users-toggle'], rows,
    flush() { while (timers.length) timers.shift()(); },
    visible() { return list.hidden ? [] : rows.filter(r => !r.hidden).map(r => r.attrs['data-login']); },
    autofill() {
      input.value = 'admin'; password.value = 'saved-admin-password';
      input.fire('beforeinput', {inputType: 'insertReplacementText', data: null});
      input.fire('input', {inputType: 'insertReplacementText', data: null});
    }
  };
}

for (const method of ['cut', 'paste', 'drop', 'insertText', 'deleteContentBackward', 'historyUndo', 'composition', 'beforeinput']) {
  const p = page(); p.autofill();
  const types = {cut: 'deleteByCut', paste: 'insertFromPaste', drop: 'insertFromDrop', composition: 'insertCompositionText', beforeinput: 'insertFromPaste'};
  const inputType = types[method] || method;
  if (['cut', 'paste', 'drop'].includes(method)) p.input.fire(method);
  if (method === 'composition') p.input.fire('compositionstart');
  p.input.fire('beforeinput', {inputType, data: 'olga', isComposing: method === 'composition'});
  p.input.value = method === 'cut' ? '' : 'olga';
  p.input.fire('input', method === 'beforeinput' ? {} : {inputType, data: 'olga', isComposing: method === 'composition'});
  assert.equal(p.password.value, '', method + ' without keydown clears the previous password');
  assert.ok(p.visible().includes(method === 'cut' ? 'admin' : 'olga'), method + ' opens matching users');
  if (method === 'composition') p.input.fire('compositionend');
}
for (const key of ['ArrowDown', 'ArrowUp', 'toggle']) {
  const p = page(); p.autofill();
  assert.equal(p.password.value, 'saved-admin-password', 'autofill preserves the password');
  assert.equal(p.list.hidden, true, 'autofill does not open a filtered list');
  if (key === 'toggle') p.toggle.fire('mousedown'); else p.input.fire('keydown', {key});
  assert.equal(p.visible().length, 3, key + ' first opens every user after autofill');
  assert.equal(p.input.getAttribute('aria-expanded'), 'true');
}
{
  const p = page(); p.autofill();
  p.input.fire('input');
  assert.equal(p.password.value, 'saved-admin-password', 'unclassified autofill preserves the password');
  p.input.fire('keydown', {key: 'x'}); // A key with no edit must not taint later autofill.
  p.input.value = 'ivan'; p.input.fire('input', {inputType: 'insertText', data: null});
  assert.equal(p.password.value, 'saved-admin-password');
  assert.equal(p.list.hidden, true);
  p.input.fire('paste'); p.flush(); // A cancelled paste must not taint later autofill either.
  p.input.value = 'olga'; p.input.fire('input');
  assert.equal(p.password.value, 'saved-admin-password');
}
{
  const p = page(); p.autofill();
  p.input.fire('beforeinput', {inputType: 'insertText', data: 'o'});
  p.input.value = 'olga'; p.input.fire('input', {inputType: 'insertText', data: 'o'});
  assert.deepEqual(p.visible(), ['olga'], 'manual typing filters');
  p.password.value = 'olga-password';
  p.input.fire('input', {inputType: 'insertText', data: ''});
  assert.equal(p.password.value, 'olga-password', 'same login preserves password');
  p.input.fire('keydown', {key: 'Escape'});
  assert.equal(p.list.hidden, true);
  p.toggle.fire('mousedown');
  const row = p.rows.find(r => r.attrs['data-login'] === 'ivan');
  p.list.fire('mousedown', {target: row});
  assert.equal(p.input.value, 'ivan'); assert.equal(p.password.value, ''); assert.equal(p.list.hidden, true);
}
for (const event of ['cut', 'beforeinput', 'compositionstart']) {
  const p = page();
  // Some password managers set values without an input event.
  p.input.value = 'admin'; p.password.value = 'saved-admin-password';
  p.input.fire(event, event === 'beforeinput' ? {inputType: 'deleteContentBackward'} : {});
  p.input.value = ''; p.input.fire('input');
  assert.equal(p.password.value, '', event + ' snapshots silent autofill before editing');
}
console.log('LoginPage DOM event regressions passed');
