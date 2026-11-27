const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

test('changing period visibility preserves both independent pairs and never submits', () => {
  const listeners = {};
  const flag = { type: 'checkbox', checked: false };
  const inputs = Object.fromEntries(Object.entries({ From: '2026-01-02', To: '2026-01-03', FromTime: '2026-09-02T11:22:33', ToTime: '2026-09-02T12:34:56' }).map(([name, value]) => [name, { name, value, disabled: false }]));
  const values = () => Object.fromEntries(Object.entries(inputs).map(([name, input]) => [name, input.value]));
  const original = values();
  let submits = 0;
  const form = { elements: { namedItem: name => name === 'ByHours' ? flag : inputs[name] }, submit: () => { submits++; } };
  const containers = [false, false, true, true].map((equals, index) => ({
    hidden: false,
    closest: () => form,
    getAttribute: () => JSON.stringify({ param: 'ByHours', equals }),
    querySelectorAll: () => [Object.values(inputs)[index]]
  }));
  form.querySelectorAll = () => containers;
  flag.form = form;
  const document = { readyState: 'complete', querySelectorAll: () => containers, addEventListener: (name, fn) => { listeners[name] = fn; } };
  vm.runInNewContext(fs.readFileSync(__dirname + '/report_params.js', 'utf8'), { window: {}, document });
  assert.deepEqual(containers.map(c => c.hidden), [false, false, true, true]);
  flag.checked = true;
  listeners.change({ target: flag });
  assert.deepEqual(containers.map(c => c.hidden), [true, true, false, false]);
  flag.checked = false;
  listeners.change({ target: flag });
  assert.deepEqual(containers.map(c => c.hidden), [false, false, true, true]);
  assert.deepEqual(values(), original);
  assert.equal(submits, 0);
  assert.ok(containers.every(c => !Object.hasOwn(c, 'disabled')));
  assert.ok(Object.values(inputs).every(c => c.disabled === false));
});
