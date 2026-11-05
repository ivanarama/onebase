'use strict';

// The Go test supplies the complete public GET DOM and unchanged form-event
// response. Run the real client functions, then serialize successful controls.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const fixture = JSON.parse(fs.readFileSync(0, 'utf8'));
const source = fs.readFileSync('static/managed.js', 'utf8');
function slice(begin, end) {
  const start = source.indexOf(begin);
  const finish = source.indexOf(end, start);
  assert.ok(start >= 0 && finish > start);
  return source.slice(start, finish + end.length);
}
class Element {
  constructor(spec, parent = null) {
    this.tagName = spec.tag.toUpperCase();
    this.attrs = new Map(Object.entries(spec.attrs || {}));
    this.parentElement = parent;
    this.value = this.attrs.get('value') || '';
    this.type = this.attrs.get('type') || 'text';
    this.disabled = this.attrs.has('disabled');
    this.style = {display: /display:\s*([^;]+)/.exec(this.attrs.get('style') || '')?.[1] || ''};
    this.checked = this.attrs.has('checked');
    this.children = (spec.children || []).map(child => new Element(child, this));
  }
  getAttribute(name) { return this.attrs.has(name) ? this.attrs.get(name) : null; }
  setAttribute(name, value) { this.attrs.set(name, String(value)); }
  removeAttribute(name) { this.attrs.delete(name); }
  get dataset() {
    const values = {};
    for (const [key, value] of this.attrs) {
      if (key.startsWith('data-')) values[key.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase())] = value;
    }
    return values;
  }
  closest(selector) {
    for (let el = this; el; el = el.parentElement) if (el.matches(selector)) return el;
    return null;
  }
  matches(selector) {
    const excluded = /:not\((\[[^)]*\])\)$/.exec(selector);
    if (excluded) return this.matches(selector.slice(0, excluded.index)) && !this.matches(excluded[1]);
    const m = /^([a-z]+)?(?:\[([\w-]+)(?:="([^"]*)")?\])?$/.exec(selector);
    assert.ok(m, 'unsupported selector: ' + selector);
    return (!m[1] || this.tagName === m[1].toUpperCase()) &&
      (!m[2] || (m[2] === 'disabled' ? this.disabled :
        (this.attrs.has(m[2]) && (m[3] === undefined || this.attrs.get(m[2]) === m[3]))));
  }
  descendants() { return this.children.flatMap(child => [child, ...child.descendants()]); }
  querySelectorAll(selector) { return this.descendants().filter(el => selector.split(',').some(part => el.matches(part.trim()))); }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
}
const page = new Element(fixture.page);
const form = page.querySelector('[id="main-form"]');
assert.ok(form);
const context = {
  window: {}, document: {getElementById: id => id === 'main-form' ? form : null, querySelector: selector => page.querySelector(selector)},
  managedRefParts: () => null, ensureRefOption: () => {},
  // applyElementStates отдаёт путь без якоря странице набора СтраницыФормы;
  // в этой форме закладок нет, и боевая функция вышла бы сразу — как здесь.
  obManagedSetPageHidden: path => assert.equal(page.querySelector('[data-ob-tab-path="' + path + '"]'), null),
};
vm.createContext(context);
vm.runInContext(slice('// BEGIN onebase-ro-apply-values', '// END onebase-ro-apply-values') + '\n' +
  slice('// BEGIN onebase-picture-bind', '// END onebase-picture-bind') + '\n' +
  slice('// BEGIN onebase-ro-apply-states', '// END onebase-ro-apply-states') + '\nthis.applyValues = applyValues;', context);
const field = 'Наименование';
const editor = form.querySelector('input[name="' + field + '"]');
if (fixture.checkEditor) {
  assert.ok(editor && editor.type !== 'hidden', 'GET must retain the editor in the DOM');
  assert.equal(!!editor.closest('fieldset[disabled]'), fixture.initiallyDisabled);
}
function serialize() {
  const values = {};
  for (const control of form.querySelectorAll('[name]')) {
    if (!['INPUT', 'SELECT', 'TEXTAREA'].includes(control.tagName) || control.disabled) continue;
    if (['button', 'submit', 'reset', 'file'].includes(control.type)) continue;
    if (['checkbox', 'radio'].includes(control.type) && !control.checked) continue;
    if (control.closest('fieldset[disabled]')) continue;
    const name = control.getAttribute('name');
    (values[name] = values[name] || []).push(String(control.value));
  }
  return values;
}
for (const [step, response] of fixture.responses.entries()) {
  // Same order as the production HTTP event response path.
  context.window.applyElementStates(response.elementStates);
  context.applyValues(response.values);
  const updated = response.values[field];
  for (const input of form.querySelectorAll('input[data-ob-picture-for]')) {
    assert.equal(input.value, updated);
    const link = form.querySelector('[data-ob-picture-link="' + input.getAttribute('data-ob-picture-for') + '"]');
    assert.equal(link.getAttribute('href'), '/ui/attachments/' + updated + '/download');
  }
  if (fixture.checkEditor) {
    assert.equal(!!editor.closest('fieldset[disabled]'), step !== 1, 'HTTP hidden_when state must toggle');
  }
  assert.deepEqual(serialize()[field], [updated], 'exactly one successful control must carry the UUID');
  // A partial later response must preserve both the UUID and its submitting name.
  context.applyValues({Unrelated: 'unchanged'});
  assert.deepEqual(serialize()[field], [updated]);
}
process.stdout.write(JSON.stringify(serialize()));
