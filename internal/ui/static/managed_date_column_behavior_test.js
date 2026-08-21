// Колонка даты в ТЧ (#1077): формат показа и редактор вместо свободного текста.
// Проверяем НАСТОЯЩИЕ функции из managed.js, а не их пересказ — копия разошлась
// бы с оригиналом и продолжала бы утверждать про код, которого нет.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');

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

function element(tag) {
  const el = {
    tagName: String(tag).toUpperCase(),
    style: {cssText: ''},
    children: [],
    value: '',
    type: '',
    className: '',
    parentElement: null,
    appendChild(child) { this.children.push(child); child.parentElement = this; return child; },
    removeChild(child) { this.children = this.children.filter((c) => c !== child); },
    remove() { if (this.parentElement) this.parentElement.removeChild(this); },
    addEventListener() {},
    removeEventListener() {},
    focus() { el.focused = true; },
  };
  return el;
}

global.document = { createElement: element };

// obManagedFormatDate зовёт obManagedEscapeHTML, поэтому в песочницу нужны обе.
const sandbox = new Function(
  extract('obManagedEscapeHTML') + '\n' +
  extract('obManagedSplitDate') + '\n' +
  extract('obManagedFormatDate') + '\n' +
  extract('ObDateEditor') + '\n' +
  'return {obManagedFormatDate, obManagedSplitDate, ObDateEditor};'
)();

const {obManagedFormatDate, obManagedSplitDate, ObDateEditor} = sandbox;

test('дата показывается днём, а не сырой меткой', () => {
  assert.equal(obManagedFormatDate('1985-03-14T00:00'), '<span>14.03.1985</span>');
});

test('ненулевое время показывается рядом с днём', () => {
  assert.equal(obManagedFormatDate('1985-03-14T13:45'), '<span>14.03.1985 13:45</span>');
});

test('пустое значение даёт пустую ячейку, а не «—»', () => {
  // Пустая ячейка даты — рабочее состояние ввода; маркер читался бы как значение.
  assert.equal(obManagedFormatDate(''), '');
  assert.equal(obManagedFormatDate(null), '');
});

test('старые метки с зоной разбираются текстом, без пересчёта по зоне браузера', () => {
  // Значения, сохранённые до #1077, могут прийти в любом из этих видов. Ключевое
  // здесь — что календарный день берётся ИЗ СТРОКИ: new Date() пересчитал бы её
  // по зоне браузера и вернул тот самый съезд на сутки.
  assert.equal(obManagedFormatDate('1985-03-14T00:00:00+03:00'), '<span>14.03.1985</span>');
  assert.equal(obManagedFormatDate('1985-03-14'), '<span>14.03.1985</span>');
  assert.equal(obManagedFormatDate('1985-03-13T21:00:00Z'), '<span>13.03.1985 21:00</span>');
});

test('не-дата показывается красным, а не как обычный текст', () => {
  const out = obManagedFormatDate('не дата');
  assert.match(out, /color:#dc2626/);
  assert.match(out, /не дата/);
});

test('редактор — datetime-local, а не свободный текст', () => {
  const container = element('div');
  const editor = new ObDateEditor({
    container,
    column: {field: 'Дат'},
    item: {'Дат': '1985-03-14T13:45'},
  });
  const input = container.children[0];
  assert.equal(input.tagName, 'INPUT');
  assert.equal(input.type, 'datetime-local');
  assert.equal(input.value, '1985-03-14T13:45');
  assert.equal(editor.isValueChanged(), false);
});

test('редактор поднимает дату без времени как полночь', () => {
  const container = element('div');
  new ObDateEditor({container, column: {field: 'Дат'}, item: {'Дат': '1985-03-14'}});
  assert.equal(container.children[0].value, '1985-03-14T00:00');
});

test('очищенная ячейка отдаётся пустой строкой', () => {
  // Сервер понимает пустую строку как «значения нет» и пишет NULL (#1074).
  const container = element('div');
  const editor = new ObDateEditor({container, column: {field: 'Дат'}, item: {'Дат': '1985-03-14T00:00'}});
  container.children[0].value = '';
  assert.equal(editor.serializeValue(), '');
  assert.equal(editor.isValueChanged(), true);
  const item = {};
  editor.applyValue(item, editor.serializeValue());
  assert.equal(item['Дат'], '');
});

test('вставленный мусор не проходит валидацию ячейки', () => {
  const container = element('div');
  const editor = new ObDateEditor({container, column: {field: 'Дат'}, item: {'Дат': ''}});
  container.children[0].value = 'не дата';
  assert.equal(editor.validate().valid, false);
  container.children[0].value = '1985-03-14T00:00';
  assert.equal(editor.validate().valid, true);
});

test('пустая ячейка проходит валидацию — это очистка, а не ошибка', () => {
  const container = element('div');
  const editor = new ObDateEditor({container, column: {field: 'Дат'}, item: {'Дат': '1985-03-14T00:00'}});
  container.children[0].value = '';
  assert.equal(editor.validate().valid, true);
});

test('obManagedSplitDate не трогает значение, которое датой не является', () => {
  assert.equal(obManagedSplitDate('не дата'), null);
  assert.equal(obManagedSplitDate(''), null);
  assert.equal(obManagedSplitDate(null), null);
});
