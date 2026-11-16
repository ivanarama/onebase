'use strict';

// obApplyConcealed: раскрытое поле включается, нераскрытое — отключено и пусто.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('function obApplyConcealed(');
const end = source.indexOf('window.obApplyConcealed = obApplyConcealed;', start);
assert.ok(start >= 0 && end > start, 'managed.js: obApplyConcealed не найдена');

test('раскрытое включается, нераскрытое отключается и стирается', () => {
  const field = (name, value) => ({ value, disabled: true, getAttribute: (k) => (k === 'name' ? name : null) });
  const opis = field('Описание', 'полный текст');
  const other = field('Адрес', 'ул. Тайная');
  other.disabled = false;
  const ctx = { document: { querySelectorAll: () => [opis, other] } };
  vm.runInNewContext(source.slice(start, end) + '\nthis.obApplyConcealed = obApplyConcealed;', ctx);
  ctx.obApplyConcealed(['описание'], ctx.document, {'Описание': opis.value});
  assert.equal(opis.disabled, false);
  assert.equal(opis.value, 'полный текст');
  assert.equal(other.disabled, true);
  assert.equal(other.value, '');
});

test('раскрытие ограничено своим экземпляром и сохраняет readonly', () => {
 const own={value:'text',disabled:true,readOnly:true,getAttribute:()=> 'Описание'};
 const alien={value:'hidden',disabled:true};
 const form={querySelectorAll:()=>[own]};
 const ctx={document:{querySelectorAll:()=>{throw Error('global DOM access');}}};
 vm.runInNewContext(source.slice(start,end)+'\nthis.apply=obApplyConcealed;',ctx);
 ctx.apply(['описание'],form, {'Описание': own.value});
 assert.equal(own.readOnly,true);
 assert.equal(alien.disabled,true);
 ctx.apply([],form);
 assert.equal(own.value,'');
});

test('маска и её подсказка сохраняются при раскрытии, редактировании и отзыве', () => {
  // Run both real functions and the delegated input listener from managed.js.
  const maskStart = source.indexOf('function obApplyInputMask(');
  const maskEnd = source.indexOf("document.addEventListener('focusin'", maskStart);
  assert.ok(maskStart >= 0 && maskEnd > maskStart);
  const listeners = {};
  const field = (name, readOnly = false) => ({
    value: '', disabled: true, readOnly, selectionStart: 0,
    attrs: {name, 'data-ob-input-mask': '00-00', placeholder: '__-__'},
    getAttribute(k) { return this.attrs[k] || null; },
    hasAttribute(k) { return Object.hasOwn(this.attrs, k); },
    setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; }
  });
  const editable = field('Секрет');
  const readonly = field('ТолькоЧтение', true);
  const alien = field('Секрет');
  const ordinary = field('Обычный');
  ordinary.disabled = false;
  const form = {querySelectorAll: () => [editable, readonly]};
  const ctx = {document: {
    addEventListener(name, handler) { listeners[name] = handler; },
    querySelectorAll() { throw Error('global conceal access'); }
  }};
  vm.runInNewContext(source.slice(start, end) + source.slice(maskStart, maskEnd) +
    '\nthis.apply = obApplyConcealed;', ctx);
  assert.equal(typeof listeners.input, 'function');
  ctx.apply([], form);
  listeners.input({target: editable});
  assert.equal(editable.value, '');
  assert.equal(editable.disabled, true);
  assert.equal(editable.getAttribute('placeholder'), '__-__');

  // The event response supplies the value before applying its revealed list.
  editable.value = readonly.value = '12-34';
  ctx.apply(['секрет', 'толькочтение'], form, {'Секрет': editable.value, 'ТолькоЧтение': readonly.value});
  assert.equal(editable.disabled, false);
  assert.equal(readonly.disabled, false);
  assert.equal(readonly.readOnly, true);
  assert.equal(alien.disabled, true);
  editable.value = '5678';
  editable.selectionStart = 4;
  listeners.input({target: editable});
  assert.equal(editable.value, '56-78');
  assert.equal(editable.selectionStart, 5);
  assert.equal(editable.getAttribute('placeholder'), '__-__');

  ordinary.value = '9012';
  ordinary.selectionStart = 4;
  listeners.input({target: ordinary});
  assert.equal(ordinary.value, '90-12');
  ctx.apply([], form); // Revoke/expiry follows the same server response path.
  assert.equal(editable.disabled, true);
  assert.equal(editable.value, '');
  assert.equal(readonly.value, '');
  assert.equal(readonly.readOnly, true);
  assert.equal(editable.getAttribute('data-ob-input-mask'), '00-00');
  assert.equal(editable.getAttribute('placeholder'), '__-__');
  assert.equal(ordinary.disabled, false);
  assert.equal(ordinary.value, '90-12');
});
