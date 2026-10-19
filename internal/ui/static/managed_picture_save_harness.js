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
    this.checked = this.attrs.has('checked');
    this.children = (spec.children || []).map(child => new Element(child, this));
  }
  getAttribute(name) { return this.attrs.has(name) ? this.attrs.get(name) : null; }
  setAttribute(name, value) { this.attrs.set(name, String(value)); }
  matches(selector) {
    const m = /^([a-z]+)?(?:\[([\w-]+)(?:="([^"]*)")?\])?$/.exec(selector);
    assert.ok(m, 'unsupported selector: ' + selector);
    return (!m[1] || this.tagName === m[1].toUpperCase()) &&
      (!m[2] || (this.attrs.has(m[2]) && (m[3] === undefined || this.attrs.get(m[2]) === m[3])));
  }
  descendants() { return this.children.flatMap(child => [child, ...child.descendants()]); }
  querySelectorAll(selector) { return this.descendants().filter(el => el.matches(selector)); }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
}
const page = new Element(fixture.page);
const form = page.querySelector('[id="main-form"]');
assert.ok(form);
const context = {
  window: {}, document: {getElementById: id => id === 'main-form' ? form : null},
  managedRefParts: () => null, ensureRefOption: () => {},
};
vm.createContext(context);
vm.runInContext(slice('// BEGIN onebase-ro-apply-values', '// END onebase-ro-apply-values') + '\n' +
  slice('// BEGIN onebase-picture-bind', '// END onebase-picture-bind') + '\nthis.applyValues = applyValues;', context);
context.applyValues(fixture.response.values);
const field = 'Наименование';
const updated = fixture.response.values[field];
for (const input of form.querySelectorAll('input[data-ob-picture-for]')) {
  assert.equal(input.value, updated);
  const link = form.querySelector('[data-ob-picture-link="' + input.getAttribute('data-ob-picture-for') + '"]');
  assert.equal(link.getAttribute('href'), '/ui/attachments/' + updated + '/download');
}
// A partial later response must preserve both the UUID and its submitting name.
context.applyValues({Unrelated: 'unchanged'});
const values = {};
for (const control of form.querySelectorAll('[name]')) {
  if (!['INPUT', 'SELECT', 'TEXTAREA'].includes(control.tagName) || control.disabled) continue;
  if (['button', 'submit', 'reset', 'file'].includes(control.type)) continue;
  if (['checkbox', 'radio'].includes(control.type) && !control.checked) continue;
  let disabled = false;
  for (let p = control.parentElement; p; p = p.parentElement) {
    if (p.tagName === 'FIELDSET' && p.disabled) disabled = true;
  }
  if (disabled) continue;
  const name = control.getAttribute('name');
  (values[name] = values[name] || []).push(String(control.value));
}
process.stdout.write(JSON.stringify(values));
