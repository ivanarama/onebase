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
function copy(anchor, value) {
  const fieldset = {
    style: {display: anchor.hidden ? 'none' : ''},
    disabled: anchor.disabled,
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

// Go-обвязка передаёт пути из публичной отрисовки и настоящие ответы события.
// Без неё остаётся самостоятельная smoke-проверка с двумя путями размещения.
const fixture = process.env.OB_HIDDEN_CONTROL_FIXTURE
  ? JSON.parse(fs.readFileSync(process.env.OB_HIDDEN_CONTROL_FIXTURE, 'utf8'))
  : {
    page: {
      anchors: [
        {path: '1', name: 'First', hidden: false, disabled: false},
        {path: '2', name: 'Second', hidden: true, disabled: true},
      ],
      controls: [
        {name: 'Улица', value: 'Старая', anchors: [0]},
        {name: 'Улица', value: 'Старая', anchors: [1]},
      ],
    },
    responses: [
      {elementStates: {hidden: {'1': true, '2': false}}, values: {Улица: 'Новая'}},
      {elementStates: {hidden: {'1': false, '2': true}}, values: {Улица: 'Обновлённая'}},
    ],
  };

test('hidden_when: ответ события доходит до всех копий реквизита, отправляется новое значение', () => {
  // Обработчик меняет видимую и скрытую копии местами и пишет новое значение.
  // Go запускает оба порядка в DOM: ответ обязан дойти и до второй копии (#1759).
  const controls = fixture.page.controls.filter((c) => c.name === 'Улица');
  assert.equal(controls.length, 2, 'в публичной разметке нужны обе копии');
  const copies = new Map(controls.map((c) => {
    const anchor = fixture.page.anchors[c.anchors[0]];
    assert.ok(anchor.path && anchor.path !== anchor.name, 'путь размещения отличается от имени');
    return [anchor.path, copy(anchor, c.value)];
  }));
  assert.equal(copies.size, 2, 'каждая копия имеет свой путь размещения');
  const inputs = [...copies.values()].map((c) => c.input);
  const form = {
    querySelector() { return null; },
    querySelectorAll(selector) { return selector === '[name="Улица"]' ? inputs : []; },
  };
  const document = {
    getElementById(id) { return id === 'main-form' ? form : null; },
    querySelector(selector) {
      const match = /^\[data-ob-el-path="([^"]+)"\]$/.exec(selector);
      assert.ok(match, 'состояния должны адресоваться путём размещения: ' + selector);
      return copies.get(match[1])?.fieldset || null;
    },
  };
  const window = {CSS: null};
  const client = new Function('window', 'document', 'CSS',
    fn('managedRefParts') + '\n' + fn('ensureRefOption') + '\n' +
      region('onebase-ro-apply-states') + '\n' + region('onebase-ro-apply-values') +
      '\nreturn {applyElementStates, applyValues};')(window, document, null);
  const submitted = () => inputs.filter((input) => !input.fieldset.disabled).map((input) => input.value);

  assert.deepEqual(submitted(), ['Старая']);
  const initialHidden = [...copies.values()].map((c) => c.fieldset.disabled);
  assert.equal(initialHidden.filter(Boolean).length, 1);
  assert.equal(fixture.responses.length, 2);
  fixture.responses.forEach((response, step) => {
    // Порядок клиента (обработка ответа в managed.js): состояния, затем значения.
    client.applyElementStates(response.elementStates);
    client.applyValues(response.values);
    [...copies.entries()].forEach(([path, c], index) => {
      const hidden = step === 0 ? !initialHidden[index] : initialHidden[index];
      assert.equal(response.elementStates.hidden[path], hidden, 'сервер переключил обе копии');
      assert.equal(c.fieldset.style.display, hidden ? 'none' : '');
      assert.equal(c.fieldset.disabled, hidden);
    });
    const value = step === 0 ? 'Новая' : 'Обновлённая';
    assert.equal(response.values.Улица, value);
    assert.deepEqual(inputs.map((input) => input.value), [value, value]);
    assert.deepEqual(submitted(), [value]);
  });
});
