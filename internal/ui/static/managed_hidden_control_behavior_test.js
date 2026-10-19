'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');

const source = fs.readFileSync('static/managed.js', 'utf8');
function region(name) {
  const begin = '// BEGIN ' + name;
  const end = '// END ' + name;
  const start = source.indexOf(begin);
  const stop = source.indexOf(end, start);
  assert.ok(start >= 0 && stop > start, 'нет участка ' + name);
  return source.slice(start, stop + end.length);
}
function fn(name) {
  const start = source.indexOf('function ' + name + '(');
  assert.ok(start >= 0, 'нет функции ' + name);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    if (source[i] === '}' && --depth === 0) return source.slice(start, i + 1);
  }
  throw new Error('не закрыта функция ' + name);
}

// Копия реквизита «Улица»: fieldset элемента формы с input внутри. Скрытая по
// hidden_when копия остаётся в DOM, но fieldset отключён — input не отправляется.
function copy(initiallyHidden, value) {
  const fieldset = {
    style: {display: initiallyHidden ? 'none' : ''},
    disabled: initiallyHidden,
    getAttribute(name) { return name === 'data-ob-control-fieldset' ? '1' : null; },
    querySelectorAll() { return []; },
  };
  const input = {
    tagName: 'INPUT', type: 'text', name: 'Улица', value, fieldset,
    classList: {contains() { return false; }},
    closest(selector) { return selector === 'fieldset[disabled]' && fieldset.disabled ? fieldset : null; },
  };
  return {fieldset, input};
}

test('hidden_when: ответ события доходит до всех копий реквизита, отправляется новое значение', () => {
  // Сначала видна первая копия, вторая скрыта; обработчик меняет их местами и
  // пишет новое значение. Показанная копия стоит в DOM второй — до неё ответ
  // раньше не доходил (#1759).
  const copies = {First: copy(false, 'old value'), Second: copy(true, 'old value')};
  const inputs = Object.values(copies).map((c) => c.input);
  const form = {
    querySelector() { return null; },
    querySelectorAll(selector) { return selector === '[name="Улица"]' ? inputs : []; },
  };
  const document = {
    getElementById(id) { return id === 'main-form' ? form : null; },
    querySelector(selector) {
      const match = /^\[data-ob-el="([^"]+)"\]$/.exec(selector);
      return match && copies[match[1]] ? copies[match[1]].fieldset : null;
    },
  };
  const window = {CSS: null};
  const client = new Function('window', 'document', 'CSS',
    fn('managedRefParts') + '\n' + fn('ensureRefOption') + '\n' +
      region('onebase-ro-apply-states') + '\n' + region('onebase-ro-apply-values') +
      '\nreturn {applyElementStates, applyValues};')(window, document, null);
  const submitted = () => inputs.filter((input) => !input.fieldset.disabled).map((input) => input.value);

  assert.deepEqual(submitted(), ['old value']);
  // Порядок клиента (обработка ответа в managed.js): состояния, затем значения.
  client.applyElementStates({hidden: {First: true, Second: false}});
  client.applyValues({Улица: 'new value'});
  assert.equal(copies.First.fieldset.style.display, 'none');
  assert.equal(copies.Second.fieldset.style.display, '');
  assert.equal(copies.First.fieldset.disabled, true);
  assert.equal(copies.Second.fieldset.disabled, false);
  assert.deepEqual(inputs.map((input) => input.value), ['new value', 'new value']);
  assert.deepEqual(submitted(), ['new value']);
});
