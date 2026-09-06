// Объектная ссылка из form-event должна проходить настоящий applyValues:
// подпись видна пользователю, а select отправляет UUID, не "[object Object]".
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

function control(tagName, type) {
  const el = {
    tagName,
    type: type || '',
    options: [],
    classList: {contains() { return false; }},
    appendChild(child) { this.options.push(child); return child; },
    value: '',
  };
  return el;
}

function runtime(controls) {
  const form = {
    querySelector(selector) {
      if (selector.startsWith('[data-ob-file-content-for=')) return null;
      const match = selector.match(/^\[name="([^"]+)"\]$/);
      return match ? (controls[match[1]] || null) : null;
    },
  };
  const document = {
    getElementById(id) { return id === 'main-form' ? form : null; },
    createElement(tag) { return {tagName: String(tag).toUpperCase(), value: '', textContent: ''}; },
  };
  return new Function(
    'document',
    'window',
    extract('managedRefParts') + '\n' +
      extract('ensureRefOption') + '\n' +
      extract('applyValues') + '\n' +
      'return {managedRefParts, applyValues};',
  )(document, {});
}

function responseFixture() {
  const encoded = process.env.ONEBASE_FORM_EVENT_RESPONSE_B64;
  if (encoded) return JSON.parse(Buffer.from(encoded, 'base64').toString('utf8'));
  const id = '11111111-1111-1111-1111-111111111111';
  return {
    values: {Склад: {UUID: id, Name: 'Склад-060', Type: 'Склад', Kind: 'catalog'}},
    refOptions: {Склад: [{id, _label: 'Склад-060'}]},
  };
}

test('form-event object reference shows its safe label and submits UUID', () => {
  const response = responseFixture();
  const select = control('SELECT', 'select-one');
  select.options.push({value: '', textContent: '— выбрать —'});
  runtime({Склад: select}).applyValues(response.values, response.refOptions);

  const expectedID = String(response.values.Склад.UUID);
  assert.equal(select.value, expectedID);
  const selected = select.options.find((option) => String(option.value) === expectedID);
  assert.ok(selected, 'applyValues did not add the selected reference option');
  assert.equal(selected.textContent, response.refOptions.Склад[0]._label);
  assert.notEqual(select.value, '[object Object]');
  assert.equal(select.options.some((option) => option.textContent === '[object Object]'), false);
});

test('client reference form {id,_label} follows the same path', () => {
  const select = control('SELECT', 'select-one');
  const ref = {id: 'client-id', _label: 'Клиентская подпись'};
  runtime({Контрагент: select}).applyValues({Контрагент: ref}, null);
  assert.equal(select.value, ref.id);
  assert.equal(select.options[0].textContent, ref._label);
});

test('text shows Name while hidden storage keeps UUID', () => {
  const ref = {UUID: 'ref-id', Name: 'Представление'};
  const text = control('INPUT', 'text');
  const hidden = control('INPUT', 'hidden');
  runtime({Текст: text, Хранилище: hidden}).applyValues({Текст: ref, Хранилище: ref}, null);
  assert.equal(text.value, ref.Name);
  assert.equal(hidden.value, ref.UUID);
});

test('arbitrary JSON object is not mistaken for a reference', () => {
  const api = runtime({});
  assert.equal(api.managedRefParts({id: 'not-a-reference', Name: 'payload'}), null);
  assert.equal(api.managedRefParts({UUID: 'not-a-reference', payload: true}), null);
  assert.equal(api.managedRefParts({UUID: {nested: true}, Name: 'payload'}), null);
});
