// messages: popup — сообщения хранилища плашкой, без дублей с ответом события.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/ui.js', 'utf8');
const start = source.indexOf('// BEGIN onebase-messages-popup');
const end = source.indexOf('// END onebase-messages-popup');
assert.ok(start >= 0 && end > start, 'блок onebase-messages-popup не найден');

function node(tag) {
  const n = { tag, children: [], style: {}, textContent: '', id: '',
    appendChild(c) { this.children.push(c); c.parent = this; return c; },
    removeChild(c) { this.children = this.children.filter((x) => x !== c); c.parent = null; },
    remove() { if (this.parent) this.parent.removeChild(this); },
    get firstChild() { return this.children[0]; } };
  return n;
}
function sandbox(seconds) {
  const timers = [];
  const body = node('body');
  const ctx = {
    window: {},
    setTimeout: (fn, ms) => timers.push({ fn, ms }),
    document: {
      body,
      createElement: node,
      getElementById: (id) => body.children.find((c) => c.id === id) || null,
      querySelector: () => (seconds ? { getAttribute: (k) => (k === 'content' ? 'popup' : String(seconds)) } : null),
    },
  };
  vm.createContext(ctx);
  vm.runInContext(source.slice(start, end), ctx);
  return { ctx, timers, body };
}

const t0 = Date.parse('2026-11-05T10:00:00Z');
const msg = (sec, text) => ({ time: new Date(t0 + sec * 1000).toISOString(), text });

test('серверный курсор сохраняет сообщения одной миллисекунды', () => {
  const { ctx } = sandbox(10);
  const r = ctx.obPickPopupMessages([msg(-1, 'старое'), msg(0, 'одно'), msg(0, 'другое'), msg(1, 'новое')], t0);
  assert.deepEqual(Array.from(r.show, m => m.text), ['одно', 'другое', 'новое']);
  assert.equal(r.seenAt, t0 + 1000);
});

test('точная ID-дедупликация, независимые одинаковые тексты и локальные предупреждения', () => {
  const { ctx } = sandbox(10);
  assert.equal(ctx.obClaimMessage('first'), true);
  assert.equal(ctx.obClaimMessage('first'), false);
  assert.equal(ctx.obClaimMessage('second'), true);
  assert.equal(ctx.obClaimMessage(undefined), true);
  assert.equal(ctx.obClaimMessage(undefined), true);
  for (let i = 0; i < 500; i++) ctx.obClaimMessage('msg-' + i);
  assert.equal(ctx.window.__obMessageIDs.length, 400);
  assert.equal(ctx.obClaimMessage('msg-499'), false);
});

test('плашки: не больше пяти, обычная живёт message_seconds, ошибка — до крестика', () => {
  const { ctx, timers, body } = sandbox(10);
  for (let i = 1; i <= 6; i++) ctx.obToast('сообщение ' + i, 'info');
  ctx.obToast('ошибка', 'err');
  const banner = body.children.find((c) => c.id === 'ob-fmevt-banner');
  assert.equal(banner.children.length, 5);
  assert.equal(banner.children[0].children[0].textContent, 'сообщение 3', 'старые вытеснены');
  assert.equal(timers.length, 6, 'у ошибки таймера нет');
  assert.ok(timers.every((t) => t.ms === 10000));
});

test('без настройки — 5 секунд', () => {
  const { ctx, timers } = sandbox(0);
  ctx.obToast('x', 'info');
  assert.equal(timers[0].ms, 5000);
});

// Exercise the production polling initializer, not a replacement renderer.
// The public HTTP/DSL/browser matrix also checks both form/poll arrival orders.
for (const status of ['important', 'very_important']) {
  test(`polling preserves ${status} color and lifetime`, async () => {
    const { ctx, timers, body } = sandbox(10);
    ctx.document.readyState = 'complete';
    ctx.document.addEventListener = () => {};
    ctx.setInterval = () => {};
    ctx.fetch = async path => {
      assert.equal(path, '/ui/messages');
      return { json: async () => ({ serverTime: new Date(t0).toISOString(), messages: [
        { ...msg(0, 'same-message'), id: 'receipt', kind: 'info', status },
      ] }) };
    };
    const initEnd = source.indexOf("\nif ('serviceWorker' in navigator)", end);
    assert.ok(initEnd > end, 'production message initializer not found');
    vm.runInContext(source.slice(end, initEnd), ctx);
    await ctx.window.obReloadMessages();
    const banner = body.children.find(c => c.id === 'ob-fmevt-banner');
    assert.equal(banner.children.length, 1, 'one store receipt is shown once');
    assert.equal(banner.children[0].children[0].textContent, 'same-message');
    assert.match(banner.children[0].style.cssText, /background:#fee2e2/);
    assert.equal(timers.length, 0, 'important messages have no auto-dismiss timer');
    banner.children[0].children[1].onclick({ stopPropagation() {} });
    assert.equal(banner.children.length, 0, 'explicit close still works');
  });
}
