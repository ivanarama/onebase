'use strict';

// deadline_warn: поле-срок подсвечивается по часам браузера. Проверяем
// настоящие функции managed.js, вырезанные из файла.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('function obDeadlineParse(');
const end = source.indexOf('function obDeadlineTick(', start);
assert.ok(start >= 0 && end > start, 'managed.js: функции поля-срока не найдены');
const ctx = {};
vm.runInNewContext(source.slice(start, end) + '\nthis.obDeadlineState = obDeadlineState;', ctx);

test('срок «ЧЧ:ММ» сегодня: меньше N минут — soon, прошёл — over', () => {
  const now = new Date(2026, 9, 21, 17, 0, 0);
  assert.equal(ctx.obDeadlineState('18:00', 30, now), '');
  assert.equal(ctx.obDeadlineState('17:30', 30, now), 'soon');
  assert.equal(ctx.obDeadlineState('17:00', 30, now), 'over');
  assert.equal(ctx.obDeadlineState('9:15', 30, now), 'over');
});

test('дата со временем в ISO и в русском формате', () => {
  const now = new Date(2026, 9, 21, 17, 0, 0);
  assert.equal(ctx.obDeadlineState('2026-10-21T17:10', 30, now), 'soon');
  assert.equal(ctx.obDeadlineState('2026-10-22 09:00:00', 30, now), '');
  assert.equal(ctx.obDeadlineState('21.10.2026 16:00', 30, now), 'over');
});

test('пустое или нечитаемое значение подсветки не даёт', () => {
  const now = new Date(2026, 9, 21, 17, 0, 0);
  assert.equal(ctx.obDeadlineState('', 30, now), '');
  assert.equal(ctx.obDeadlineState('после обеда', 30, now), '');
});
