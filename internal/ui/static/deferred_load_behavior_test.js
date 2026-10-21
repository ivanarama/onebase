'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('var obDeferredStarted = false;');
const end = source.indexOf('obManagedReady(function () {', start);
assert.ok(start >= 0 && end > start, 'managed.js: obStartDeferredLoads не найдена');

test('ПослеОткрытия: индикатор на время загрузки, событие с _deferred, один запуск', async () => {
  const classes = new Set();
  const group = {
    getAttribute: (k) => (k === 'data-ob-after-open' ? 'ГруппаИстория' : null),
    classList: { add: (c) => classes.add(c), remove: (c) => classes.delete(c) },
  };
  const calls = [];
  let finish;
  const ctx = {
    document: { querySelectorAll: () => [group] },
    Promise,
  };
  ctx.window = {
    obFire: (el, ev, extra) => { calls.push([el, ev, extra]); return new Promise((r) => { finish = r; }); },
  };
  vm.runInNewContext(source.slice(start, end) + '\nthis.obStartDeferredLoads = obStartDeferredLoads;', ctx);
  ctx.obStartDeferredLoads();
  ctx.obStartDeferredLoads();
  assert.equal(calls.length, 1, 'второй вызов не запускает загрузку повторно');
  assert.deepEqual(calls[0].slice(0, 2), ['ГруппаИстория', 'ПослеОткрытия']);
  assert.equal(calls[0][2]._deferred, '1');
  assert.ok(classes.has('ob-loading'), 'индикатор на время загрузки');
  finish(true);
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(!classes.has('ob-loading'), 'индикатор снят после ответа');
});
