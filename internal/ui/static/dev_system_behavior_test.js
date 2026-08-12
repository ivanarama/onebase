'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const begin = source.indexOf('// BEGIN onebase-dev-system-handler');
const end = source.indexOf('// END onebase-dev-system-handler');
assert.ok(begin >= 0 && end > begin, 'dev system handler markers must exist');
const context = {};
vm.runInNewContext(source.slice(begin, end) + '\nthis.handle = obHandleDevSystem;', context);
const handle = context.handle;

test('ordinary DSL event cannot trigger reload', () => {
  const state = { generation: null };
  let reloads = 0;
  const handled = handle({ name: 'dev-reload', data: null }, true, state, () => { reloads++; });
  assert.equal(handled, false);
  assert.equal(reloads, 0);
});

test('production page ignores system envelopes', () => {
  const state = { generation: null };
  let reloads = 0;
  const handled = handle({ system: 'dev-reload' }, false, state, () => { reloads++; });
  assert.equal(handled, false);
  assert.equal(reloads, 0);
});

test('dev reload and changed generation reload exactly when required', () => {
  const state = { generation: null };
  let reloads = 0;
  const reload = () => { reloads++; };
  assert.equal(handle({ system: 'dev-generation', data: 'a' }, true, state, reload), true);
  assert.equal(reloads, 0);
  assert.equal(handle({ system: 'dev-generation', data: 'a' }, true, state, reload), true);
  assert.equal(reloads, 0);
  assert.equal(handle({ system: 'dev-generation', data: 'b' }, true, state, reload), true);
  assert.equal(reloads, 1);
  assert.equal(handle({ system: 'dev-reload' }, true, state, reload), true);
  assert.equal(reloads, 2);
});
