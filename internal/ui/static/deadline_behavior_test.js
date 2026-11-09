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

// Execute the actual DOM startup, timer and input handlers with a fixed browser
// timezone. A parser-only assertion cannot prove that rendered controls update.
process.env.TZ = 'Europe/Belgrade';
function runtime(value) {
  let now = new Date('2026-10-21T15:00:00Z');
  const classes = new Set();
  const input = {
    value,
    getAttribute: name => name === 'data-ob-deadline' ? '30' : null,
    hasAttribute: name => name === 'data-ob-deadline',
    classList: {toggle(name, on) { if (on) classes.add(name); else classes.delete(name); }}
  };
  let timer, onInput;
  class BrowserDate extends Date {
    constructor(...args) { super(...(args.length ? args : [now.getTime()])); }
  }
  const browser = {
    Date: BrowserDate,
    window: {},
    document: {
      querySelectorAll(selector) { assert.equal(selector, 'input[data-ob-deadline]'); return [input]; },
      addEventListener(event, callback) { assert.equal(event, 'input'); onInput = callback; }
    },
    obManagedReady(callback) { callback(); },
    setInterval(callback, ms) { assert.equal(ms, 15000); timer = callback; }
  };
  vm.runInNewContext(source.slice(start), browser);
  return {
    state: () => [...classes].sort(),
    tick(iso) { now = new Date(iso); timer(); },
    enter(value) { input.value = value; onInput({target: input}); }
  };
}

test('DOM startup, timer and input update deadline classes', () => {
  const page = runtime('17:20');
  assert.deepEqual(page.state(), ['ob-deadline-soon']);
  page.tick('2026-10-21T15:21:00Z');
  assert.deepEqual(page.state(), ['ob-deadline-over']);
  page.enter('18:00');
  assert.deepEqual(page.state(), []);
  page.enter('');
  assert.deepEqual(page.state(), []);
});

test('DOM honors Z and positive/negative ISO offsets', () => {
  for (const value of ['2026-10-21T16:30:00Z', '2026-10-21T18:30:00+02:00', '2026-10-21T12:30:00-04:00']) {
    const page = runtime(value);
    assert.deepEqual(page.state(), [], value);
    page.tick('2026-10-21T16:15:00Z');
    assert.deepEqual(page.state(), ['ob-deadline-soon'], value);
    page.tick('2026-10-21T16:31:00Z');
    assert.deepEqual(page.state(), ['ob-deadline-over'], value);
  }
});

test('DOM clears highlighting for invalid dates, times and trailing garbage', () => {
  const page = runtime('16:00');
  assert.deepEqual(page.state(), ['ob-deadline-over']);
  for (const value of ['12:99', '24:00', '12:00:60', '2026-02-31 16:00',
    '2026-13-01T16:00', '2026-00-01T16:00', '2026-01-00T16:00',
    '2026-10-21T16:00garbage', '21.10.2026 16:00garbage',
    '2026-02-29T16:00Z', '2026-10-21T16:00+24:00', '2026-10-21T16:00+02:60',
    '31.04.2026 16:00', '21.10.2026 24:00']) {
    page.enter(value);
    assert.deepEqual(page.state(), [], value);
    page.enter('16:00');
    assert.deepEqual(page.state(), ['ob-deadline-over']);
  }
});

test('DOM accepts leap days, seconds and fractional ISO seconds', () => {
  const page = runtime('2028-02-29T16:00:00Z');
  page.tick('2028-02-29T15:45:00Z');
  assert.deepEqual(page.state(), ['ob-deadline-soon']);
  page.enter('2028-02-29T16:00:00.500Z');
  page.tick('2028-02-29T16:00:00Z');
  assert.deepEqual(page.state(), ['ob-deadline-soon']);
  page.tick('2028-02-29T16:00:01Z');
  assert.deepEqual(page.state(), ['ob-deadline-over']);
});
