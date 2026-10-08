// Ответ form-event не стирает то, что пользователь набрал, пока шёл запрос:
// сервер вернул ровно отправленное, а в поле уже больше — поле не трогаем.
// Значение, которое сервер изменил, применяется как раньше.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');

const source = fs.readFileSync('static/managed.js', 'utf8');

function extract(name) {
  const start = source.indexOf('function ' + name);
  if (start < 0) throw new Error('в managed.js нет функции ' + name);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') {
      depth--;
      if (depth === 0) return source.slice(start, i + 1);
    }
  }
  throw new Error('не закрыта функция ' + name);
}

function input(value) {
  return {tagName: 'INPUT', type: 'text', value, classList: {contains() { return false; }}};
}

function applyValues(controls) {
  const form = {
    querySelector(selector) {
      if (selector.startsWith('[data-ob-file-content-for=')) return null;
      const match = selector.match(/^\[name="([^"]+)"\]$/);
      return match ? (controls[match[1]] || null) : null;
    },
    // applyValues раздаёт значение всем копиям реквизита (#1759).
    querySelectorAll(selector) {
      const match = selector.match(/^\[name="([^"]+)"\]$/);
      return match && controls[match[1]] ? [controls[match[1]]] : [];
    },
  };
  const document = {getElementById(id) { return id === 'main-form' ? form : null; }};
  return new Function('document', 'window',
    extract('managedRefParts') + '\n' + extract('ensureRefOption') + '\n' + extract('applyValues') +
      '\nreturn applyValues;')(document, {});
}

test('набранное за время запроса не стирается эхом отправленного', () => {
  const phone = input('(937)637-32-71');
  const sent = new URLSearchParams({Телефон: '(937)637-32-7'});
  applyValues({Телефон: phone})({Телефон: '(937)637-32-7'}, null, sent);
  assert.equal(phone.value, '(937)637-32-71');
});

test('значение, изменённое сервером, применяется', () => {
  const field = input('пользователь');
  const sent = new URLSearchParams({Поле: 'было'});
  applyValues({Поле: field})({Поле: 'сервер'}, null, sent);
  assert.equal(field.value, 'сервер');
});

test('без изменений пользователя ответ применяется, в том числе маска', () => {
  const phone = input('••••••');
  const sent = new URLSearchParams({Телефон: '••••••'});
  applyValues({Телефон: phone})({Телефон: '••••••'}, null, sent);
  assert.equal(phone.value, '••••••');
  const other = input('');
  applyValues({Другое: other})({Другое: 'x'}, null, null);
  assert.equal(other.value, 'x');
});
