'use strict';

// Снятие «грязного» признака формы после записи из обработчика — поведение
// боевого кода из managed.js.
//
// Разметочный тест тут бесполезен: и до правки в ответе были и savedId, и
// version, и обе ветки что-то делали. Ошибка была в ТОМ, ЧЕГО НЕ ДЕЛАЛОСЬ, —
// у существующей записи флаг не снимался вовсе. Поэтому исполняем ту же
// функцию, что и браузер.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');
const begin = source.indexOf('// BEGIN onebase-form-written-handler');
const end = source.indexOf('// END onebase-form-written-handler');
assert.ok(begin >= 0 && end > begin, 'form written handler markers must exist');
const context = {};
vm.runInNewContext(source.slice(begin, end) + 'this.written = obFormWrittenByHandler;', context);
const written = context.written;

function state() {
  return { dirty: true, title: '● Приём звонка', baseTitle: 'Приём звонка' };
}

test('запись существующего документа снимает звёздочку', () => {
  const s = state();
  assert.equal(written({ version: 7 }, s), true);
  assert.equal(s.dirty, false);
  assert.equal(s.title, 'Приём звонка');
});

test('первая запись новой формы снимает звёздочку', () => {
  const s = state();
  assert.equal(written({ savedId: 'a24dee66-700d-4c34-ba4f-c56a5a1bbd3b' }, s), true);
  assert.equal(s.dirty, false);
  assert.equal(s.title, 'Приём звонка');
});

test('событие без записи оставляет несохранённое несохранённым', () => {
  const s = state();
  assert.equal(written({ values: { Филиал: 'Москва' } }, s), false);
  assert.equal(s.dirty, true);
  assert.equal(s.title, '● Приём звонка');
});

test('обработчик записал объект и упал — записанное не считается потерянным', () => {
  const s = state();
  assert.equal(written({ savedId: 'x', error: 'Заявку оформить нельзя' }, s), true);
  assert.equal(s.dirty, false);
});

test('чистый заголовок не портится второй записью подряд', () => {
  const s = { dirty: false, title: 'Приём звонка', baseTitle: 'Приём звонка' };
  assert.equal(written({ version: 9 }, s), true);
  assert.equal(s.title, 'Приём звонка');
});

test('пустой ответ ничего не трогает', () => {
  const s = state();
  assert.equal(written(null, s), false);
  assert.equal(s.dirty, true);
});
