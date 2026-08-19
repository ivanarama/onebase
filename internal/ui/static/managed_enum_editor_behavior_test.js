// Редактор ячейки-перечисления в ТЧ (#1010): список значений вместо свободного
// текста. Проверяем НАСТОЯЩИЙ ObEnumEditor из managed.js, а не его пересказ —
// копия разошлась бы с оригиналом и продолжала бы утверждать про код, которого
// нет.
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

const editorSource = extract('ObEnumEditor');

function element(tag) {
  const el = {
    tagName: String(tag).toUpperCase(),
    style: {},
    children: [],
    listeners: {},
    value: '',
    textContent: '',
    parentElement: null,
    appendChild(child) { this.children.push(child); child.parentElement = this; return child; },
    removeChild(child) { this.children = this.children.filter((c) => c !== child); },
    remove() { if (this.parentElement) this.parentElement.removeChild(this); },
    addEventListener(type, fn) { (this.listeners[type] || (this.listeners[type] = [])).push(fn); },
    removeEventListener(type, fn) {
      this.listeners[type] = (this.listeners[type] || []).filter((f) => f !== fn);
    },
    dispatch(type, event) { (this.listeners[type] || []).forEach((fn) => fn(event)); },
    focus() {},
  };
  Object.defineProperty(el, 'options', { get() { return el.children; } });
  return el;
}

function makeEditor(current, order) {
  global.document = { createElement: element };
  const container = element('div');
  const item = { Вид: current };
  const args = { column: { field: 'Вид' }, item, container, commitChanges() {}, cancelChanges() {} };
  const labels = { Телефон: 'Телефон', Почта: 'Почта', Мессенджер: 'Мессенджер' };
  const factory = new Function('args', 'labels', 'order',
    editorSource + '\nreturn new ObEnumEditor("Вид", labels, order, args);');
  const editor = factory(args, labels, order || ['Телефон', 'Почта', 'Мессенджер']);
  return { editor, item, select: container.children[0] };
}

test('в ячейке рисуется список значений в порядке объявления values:', () => {
  const { select } = makeEditor('');
  assert.equal(select.tagName, 'SELECT');
  assert.deepEqual(select.options.map((o) => o.value), ['', 'Телефон', 'Почта', 'Мессенджер']);
  // Первый пункт пустой: «не выбрано» — законное состояние, и иначе значение
  // не очистить.
  assert.equal(select.options[0].value, '');
});

test('текущее значение выбрано и уезжает в строку без изменений', () => {
  const { editor, item, select } = makeEditor('Почта');
  assert.equal(select.value, 'Почта');
  assert.equal(editor.isValueChanged(), false);
  select.value = 'Телефон';
  assert.equal(editor.isValueChanged(), true);
  editor.applyValue(item, editor.serializeValue());
  assert.equal(item['Вид'], 'Телефон');
});

test('значение вне перечисления показывается, а не подменяется первым вариантом', () => {
  const { editor, select } = makeEditor('Факс');
  const stale = select.options[select.options.length - 1];
  assert.equal(stale.value, 'Факс');
  assert.equal(stale.textContent, '⚠ Факс');
  assert.equal(select.value, 'Факс', 'редактор потерял записанное значение');
  // Повторный loadValue (SlickGrid зовёт его сам после init) не должен
  // плодить дубли пункта.
  editor.loadValue({ Вид: 'Факс' });
  assert.equal(select.options.filter((o) => o.value === 'Факс').length, 1);
});

test('validate() пропускает пустое и известное значение и отклоняет чужое', () => {
  const { editor, select } = makeEditor('Почта');
  select.value = '';
  assert.equal(editor.validate().valid, true);
  select.value = 'Мессенджер';
  assert.equal(editor.validate().valid, true);
  select.value = 'Факс';
  const res = editor.validate();
  assert.equal(res.valid, false);
  assert.match(res.msg, /Факс/);
});

test('стрелки не всплывают в грид — иначе список не раскрыть', () => {
  const { select } = makeEditor('');
  let stopped = 0;
  select.dispatch('keydown', { key: 'ArrowDown', stopPropagation() { stopped++; } });
  select.dispatch('keydown', { key: 'ArrowUp', stopPropagation() { stopped++; } });
  assert.equal(stopped, 2);
  // Enter трогать нельзя: им SlickGrid коммитит правку ячейки.
  let entered = 0;
  select.dispatch('keydown', { key: 'Enter', stopPropagation() { entered++; } });
  assert.equal(entered, 0);
});
