'use strict';

// tab_skip_readonly / autofocus: настоящие функции managed.js на маленьком
// поддельном DOM.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('var obAutofocusDone = null;');
const end = source.indexOf('window.obFormKeyboard = obFormKeyboard;', start);
assert.ok(start >= 0 && end > start, 'managed.js: блок клавиатуры формы не найден');

function control(name, opts) {
  const attrs = new Map(Object.entries(opts.attrs || {}));
  const el = {
    name, type: opts.type || 'text', value: opts.value || '', readOnly: !!opts.readOnly, disabled: !!opts.disabled,
    offsetParent: opts.hidden ? null : {}, focused: false,
    getAttribute: (k) => (attrs.has(k) ? attrs.get(k) : null),
    setAttribute: (k, v) => attrs.set(k, String(v)),
    hasAttribute: (k) => attrs.has(k),
    removeAttribute: (k) => attrs.delete(k),
    closest: (sel) => (opts.inTable && /tp-table|data-ob-table-readonly/.test(sel) ? {} : null),
    focus() { ctx.document.activeElement = el; el.focused = true; },
  };
  return el;
}

let ctx;
function run(controls, formAttrs) {
  const form = {
    getAttribute: (k) => (k in formAttrs ? formAttrs[k] : null),
    querySelectorAll: () => controls,
    querySelector: () => null,
  };
  ctx = { document: { body: {}, activeElement: null, getElementById: (id) => (id === 'main-form' ? form : null) } };
  ctx.document.activeElement = ctx.document.body;
  vm.runInNewContext(source.slice(start, end) + '\nthis.obFormKeyboard = obFormKeyboard;', ctx);
  ctx.obFormKeyboard(false);
}

test('tab_skip_readonly: нередактируемое уходит из Tab, редактируемое остаётся', () => {
  const ro = control('Номер', { readOnly: true });
  const dis = control('Автор', { disabled: true });
  const cell = control('vt.История.0.Дата', { inTable: true });
  const ok = control('ФИО', {});
  run([ro, dis, cell, ok], { 'data-ob-tab-skip-readonly': '1' });
  assert.equal(ro.getAttribute('tabindex'), '-1');
  assert.equal(dis.getAttribute('tabindex'), '-1');
  assert.equal(cell.getAttribute('tabindex'), '-1');
  assert.equal(ok.getAttribute('tabindex'), null);
});

test('autofocus first_empty: первое пустое доступное поле, пропуская заполненные, скрытые и нередактируемые', () => {
  const ro = control('Номер', { readOnly: true });
  const filled = control('Филиал', { value: 'МСК' });
  const hidden = control('Скрытое', { hidden: true });
  const empty = control('ФИО', {});
  const next = control('Город', {});
  run([ro, filled, hidden, empty, next], { 'data-ob-autofocus': 'first_empty' });
  assert.equal(empty.focused, true);
  assert.equal(next.focused, false);
});

test('tab_skip_readonly: возвращает исходный tabindex после снятия readonly', () => {
  const explicit = control('Explicit', { readOnly: true, attrs: { tabindex: '2' } });
  const implicit = control('Implicit', { readOnly: true });
  const empty = control('Empty', { readOnly: true, attrs: { tabindex: '' } });
  run([explicit, implicit, empty], { 'data-ob-tab-skip-readonly': '1' });
  explicit.readOnly = false;
  implicit.readOnly = false;
  empty.readOnly = false;
  ctx.obFormKeyboard(true);
  assert.equal(explicit.getAttribute('tabindex'), '2');
  assert.equal(implicit.getAttribute('tabindex'), null);
  assert.equal(empty.getAttribute('tabindex'), '');
  assert.equal(explicit.hasAttribute('data-ob-tab-orig'), false);
  assert.equal(implicit.hasAttribute('data-ob-tab-orig'), false);
});

test('table navigation buttons leave Tab but stay enabled; commands and autofocus keep their contract', () => {
  const card = control('Card', { type: 'button', inTable: true });
  const command = control('Command', { type: 'button' });
  const submit = control('Save', { type: 'submit' });
  const first = control('First', {});
  run([card, command, submit, first], {
    'data-ob-tab-skip-readonly': '1', 'data-ob-autofocus': 'first_empty',
  });
  assert.equal(card.getAttribute('tabindex'), '-1');
  assert.equal(card.disabled, false);
  assert.equal(command.getAttribute('tabindex'), null);
  assert.equal(submit.getAttribute('tabindex'), null);
  assert.equal(first.focused, true);
  ctx.obFormKeyboard(true);
  assert.equal(ctx.document.activeElement, first);
  assert.equal(card.getAttribute('data-ob-tab-orig'), 'null');
});
