const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('// BEGIN onebase-form-idle');
const end = source.indexOf('// END onebase-form-idle');
if (start < 0 || end < 0) throw new Error('form idle block is absent from managed.js');
const ctx = vm.createContext({ Promise });
vm.runInContext(source.slice(start, end) + '\nthis.make = obManagedIdleManager;', ctx);

// Поддельные часы: таймеры срабатывают по advance(ms).
function clock() {
  let now = 0, seq = 0;
  const timers = new Map();
  return {
    now: () => now,
    setTimeout(fn, ms) { const id = ++seq; timers.set(id, { at: now + ms, fn }); return id; },
    clearTimeout(id) { timers.delete(id); },
    async advance(ms) {
      const until = now + ms;
      for (;;) {
        let next = null;
        for (const [id, t] of timers) if (t.at <= until && (!next || t.at < next[1].at)) next = [id, t];
        if (!next) break;
        timers.delete(next[0]);
        now = next[1].at;
        next[1].fn();
        await new Promise(setImmediate);
      }
      now = until;
    },
  };
}

function env(c, opts = {}) {
  const fired = [];
  const state = { busy: false, modal: false, hidden: false, pending: null };
  const e = {
    fire(proc, token) {
      fired.push({ proc, token, at: c.now() });
      if (opts.slow) { let done; state.pending = new Promise(r => { done = r; }); state.done = done; return state.pending; }
      return Promise.resolve();
    },
    busy: () => state.busy, modal: () => state.modal, hidden: () => state.hidden,
    now: c.now, setTimeout: c.setTimeout, clearTimeout: c.clearTimeout,
  };
  return { e, fired, state };
}

test('тик раз в интервал с подписью; отключение снимает таймер', async () => {
  const c = clock(), { e, fired } = env(c), m = ctx.make(e);
  m.apply([{ proc: 'Опрос', interval: 2, token: 'sig' }]);
  await c.advance(1999); assert.equal(fired.length, 0);
  await c.advance(1); assert.equal(fired.length, 1); assert.deepEqual([fired[0].proc, fired[0].token], ['Опрос', 'sig']);
  await c.advance(2000); assert.equal(fired.length, 2);
  m.apply([{ proc: 'опрос', off: true }]);
  await c.advance(10000); assert.equal(fired.length, 2);
});

test('следующий тик — только после ответа предыдущего', async () => {
  const c = clock(), { e, fired, state } = env(c, { slow: true }), m = ctx.make(e);
  m.apply([{ proc: 'Опрос', interval: 1, token: 't' }]);
  await c.advance(1000); assert.equal(fired.length, 1);
  await c.advance(5000); assert.equal(fired.length, 1, 'ответа ещё нет — новых тиков нет');
  state.done(); await new Promise(setImmediate);
  await c.advance(1000); assert.equal(fired.length, 2);
});

test('занятая форма или открытое окно откладывают тик, не теряя его', async () => {
  const c = clock(), { e, fired, state } = env(c), m = ctx.make(e);
  m.apply([{ proc: 'Опрос', interval: 2, token: 't' }]);
  state.busy = true;
  await c.advance(2000); assert.equal(fired.length, 0);
  state.busy = false; state.modal = true;
  await c.advance(1000); assert.equal(fired.length, 0);
  state.modal = false;
  await c.advance(1000); assert.equal(fired.length, 1);
});

test('однократный срабатывает один раз; интервал меньше секунды — секунда', async () => {
  const c = clock(), { e, fired } = env(c), m = ctx.make(e);
  m.apply([{ proc: 'ОдинРаз', interval: 0.2, once: true, token: 't' }]);
  await c.advance(999); assert.equal(fired.length, 0);
  await c.advance(1); assert.equal(fired.length, 1);
  await c.advance(10000); assert.equal(fired.length, 1);
});

test('скрытая вкладка — реже; вернулись — просроченный тик сразу', async () => {
  const c = clock(), { e, fired, state } = env(c), m = ctx.make(e);
  state.hidden = true;
  m.apply([{ proc: 'Опрос', interval: 2, token: 't' }]);
  await c.advance(9999); assert.equal(fired.length, 0, 'скрытая: не чаще раза в 10 с');
  await c.advance(1); assert.equal(fired.length, 1);
  await c.advance(3000); assert.equal(fired.length, 1);
  state.hidden = false; m.visibility();
  await c.advance(0); assert.equal(fired.length, 2, 'вернулись на вкладку — тик сразу');
});

test('повторное подключение той же процедуры заменяет таймер', async () => {
  const c = clock(), { e, fired } = env(c), m = ctx.make(e);
  m.apply([{ proc: 'Опрос', interval: 5, token: 'a' }]);
  await c.advance(3000);
  m.apply([{ proc: 'Опрос', interval: 2, token: 'b' }]);
  await c.advance(2000); assert.equal(fired.length, 1); assert.equal(fired[0].token, 'b');
  assert.equal(JSON.stringify(m.active()), '["Опрос"]');
});
