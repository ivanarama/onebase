// hint_from (подсказка из реквизита формы): после события формы title
// элемента берётся из нового значения реквизита в ответе, пустое значение
// возвращает статический hint, горячая клавиша кнопки остаётся в скобках.
// Срезом берём только obManagedApplyHints — помощник чистый, полный DOM не нужен.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');
const start = source.indexOf('function obManagedApplyHints');
const end = source.indexOf('window.obManagedApplyHints', start);
assert.ok(start >= 0 && end > start, 'нет obManagedApplyHints в managed.js');

function element(attrs) {
  return {
    attrs: Object.assign({}, attrs),
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attrs, name) ? this.attrs[name] : null; },
    setAttribute(name, value) { this.attrs[name] = String(value); },
    removeAttribute(name) { delete this.attrs[name]; },
  };
}

function run(elements, values) {
  const sandbox = {
    window: {},
    document: { querySelectorAll(sel) { assert.equal(sel, '[data-ob-hint-from]'); return elements; } },
  };
  vm.createContext(sandbox);
  vm.runInContext(source.slice(start, end), sandbox, { filename: 'managed-hint-from.js' });
  sandbox.obManagedApplyHints(values);
}

test('title берётся из нового значения реквизита', () => {
  const btn = element({ 'data-ob-hint-from': 'ПримечаниеНаправления', 'data-ob-hint': 'Примечание направления', title: 'старое' });
  run([btn], { ПримечаниеНаправления: 'Только после 18:00' });
  assert.equal(btn.getAttribute('title'), 'Только после 18:00');
});

test('пустое значение возвращает статический hint, горячая клавиша — в скобках', () => {
  const btn = element({ 'data-ob-hint-from': 'Прим', 'data-ob-hint': 'Примечание', 'data-ob-hint-key': 'F2', title: 'Было (F2)' });
  run([btn], { Прим: '' });
  assert.equal(btn.getAttribute('title'), 'Примечание (F2)');
  run([btn], { Прим: 'Новое' });
  assert.equal(btn.getAttribute('title'), 'Новое (F2)');
});

test('без hint и значения title снимается; чужой реквизит не трогает элемент', () => {
  const lbl = element({ 'data-ob-hint-from': 'Прим', title: 'было' });
  run([lbl], { Другое: 'x' });
  assert.equal(lbl.getAttribute('title'), 'было');
  run([lbl], { Прим: null });
  assert.equal(lbl.getAttribute('title'), null);
});
