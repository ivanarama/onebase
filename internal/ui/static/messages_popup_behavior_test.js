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

test('новые сообщения хранилища показываются, уже показанные формой и старые — нет', () => {
  const { ctx } = sandbox(10);
  const flashed = [{ text: 'Заявка записана', at: t0 + 5000 }];
  const r = ctx.obPickPopupMessages(
    [msg(-10, 'старое'), msg(1, 'Заявка записана'), msg(2, 'Фоновое задание выполнено')], t0, flashed, t0 + 6000);
  assert.deepEqual(Array.from(r.show, (m) => m.text), ['Фоновое задание выполнено']);
  assert.equal(r.seenAt, t0 + 2000);
  assert.equal(r.flashed.length, 0, 'плашка формы гасит ровно одно совпавшее сообщение');
  // Повторный опрос того же хранилища ничего не показывает.
  assert.equal(ctx.obPickPopupMessages([msg(1, 'Заявка записана'), msg(2, 'Фоновое задание выполнено')], r.seenAt, r.flashed, t0 + 9000).show.length, 0);
});

test('отметка плашки формы старше минуты забывается', () => {
  const { ctx } = sandbox(10);
  const r = ctx.obPickPopupMessages([msg(70, 'Готово')], t0, [{ text: 'Готово', at: t0 }], t0 + 71000);
  assert.deepEqual(Array.from(r.show, (m) => m.text), ['Готово']);
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
