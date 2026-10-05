'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '..', 'static', 'configurator.js'), 'utf8');
const start = source.indexOf('function cfgToggleMultiline(');
const end = source.indexOf('var _cfgNewTpIdx', start);
assert.ok(start >= 0 && end > start);
const context = {T: value => value};
vm.createContext(context);
vm.runInContext(source.slice(start, end), context);

test('changing a field type clears the flag and hides its control', () => {
  const checkbox = {checked: true, disabled: false};
  const label = {hidden: false, querySelector: () => checkbox};
  const select = {value: 'number', closest: () => ({querySelector: () => label})};
  context.cfgToggleMultiline(select);
  assert.equal(checkbox.checked, false);
  assert.equal(checkbox.disabled, true);
  assert.equal(label.hidden, true);
  select.value = 'string';
  context.cfgToggleMultiline(select);
  assert.equal(checkbox.disabled, false);
  assert.equal(checkbox.checked, false);
  assert.equal(label.hidden, false);
});

test('unsupported field contexts remain editable without a multiline control', () => {
  context.cfgToggleMultiline({value: 'number', closest: () => ({querySelector: () => null})});
});

test('add-field action exposes multiline only in enabled tables', () => {
  for (const allowed of [true, false]) {
    let added;
    const table = {
      getAttribute: name => name === 'data-cfg-multiline-fields' && allowed ? '1' : null,
      appendChild: row => { added = row; },
    };
    context.document = {
      getElementById: () => table,
      createElement: () => ({innerHTML: '', querySelector: () => ({focus() {}})}),
    };
    // Delete-column padding is unrelated to the controls produced by this action.
    context.cfgAppendDeleteCell = () => {};
    context.cfgAddField('fields', 'new_field', 'Заметки', 'entity');
    assert.equal(/name="new_field\.\d+\.multiline_present"/.test(added.innerHTML), allowed);
    assert.equal(/type="checkbox" name="new_field\.\d+\.multiline"/.test(added.innerHTML), allowed);
    assert.match(added.innerHTML, /onchange="[^"]*cfgToggleMultiline\(this\)/);
  }
});
