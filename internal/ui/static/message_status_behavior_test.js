'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

// Сообщить(Текст, СтатусСообщения.Важное): сервер везёт статус невидимой
// меткой (internal/msgstatus), тост формы снимает её и показывает Важное и
// ОченьВажное как ошибку — красным и до закрытия.

const managed = fs.readFileSync('static/managed.js', 'utf8');
const start = managed.indexOf('// BEGIN onebase-message-status');
const end = managed.indexOf('// END onebase-message-status');
assert.ok(start >= 0 && end > start, 'блок onebase-message-status не найден');
const src = managed.slice(start, end);

function load() {
  const flashed = [];
  const ctx = { flashed, flash(text, kind) { flashed.push([text, kind]); } };
  vm.createContext(ctx);
  vm.runInContext(src + '\nthis.obMessageParts = obMessageParts; this.obFlashMessage = obFlashMessage;', ctx);
  return ctx;
}

// Та же кодировка, что msgstatus.Encode: U+2063 × номер статуса + U+2064.
const mark = (n, text) => '\u2063'.repeat(n) + '\u2064' + text;

test('статус разбирается, Важное и ОченьВажное — ошибка', () => {
  const ctx = load();
  ['обычное', mark(1, 'инфо'), mark(2, 'внимание'), mark(3, 'важное'), mark(4, 'очень')].forEach(ctx.obFlashMessage);
  assert.deepEqual(ctx.flashed.map((x) => [x[0], x[1]]), [
    ['обычное', 'ok'], ['инфо', 'ok'], ['внимание', 'ok'], ['важное', 'err'], ['очень', 'err'],
  ]);
});

test('текст без завершителя метки не трогается', () => {
  const ctx = load();
  const raw = '\u2063\u2063текст';
  assert.deepEqual({ ...ctx.obMessageParts(raw) }, { text: raw, error: false });
  assert.deepEqual({ ...ctx.obMessageParts(null) }, { text: '', error: false });
});
