// Редактор ссылки в ячейке ТЧ: форма подбора должна предлагать «+ Создать»
// ровно тогда, когда колонка это разрешает (allow_inline_create у поля ТЧ).
// Проверяем настоящий ObRefEditor из managed.js, а не его пересказ.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');

const source = fs.readFileSync('static/managed.js', 'utf8');
// refId живёт рядом с редактором в той же области видимости инициализатора —
// берём срез от него, иначе редактор не соберётся.
const start = source.indexOf('  function refId(');
const end = source.indexOf('  function ObNumberEditor(', start);
if (start < 0 || end < 0) throw new Error('managed ref editor slice not found');
const editorSource = source.slice(start, end);

function element(tag) {
  const attrs = {};
  const el = {
    tagName: String(tag).toUpperCase(),
    style: {},
    children: [],
    listeners: {},
    value: '',
    parentElement: null,
    textContent: '',
    setAttribute(name, value) { attrs[name] = String(value); },
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    appendChild(child) { this.children.push(child); child.parentElement = this; return child; },
    removeChild(child) { this.children = this.children.filter((c) => c !== child); },
    remove() { if (this.parentElement) this.parentElement.removeChild(this); },
    addEventListener(type, fn) { (this.listeners[type] || (this.listeners[type] = [])).push(fn); },
    removeEventListener() {},
    dispatch(type, event) { (this.listeners[type] || []).forEach((fn) => fn(event || {preventDefault() {}, stopPropagation() {}})); },
    focus() {},
    select() {},
    querySelectorAll() { return []; },
    getBoundingClientRect() { return {left: 0, top: 0, bottom: 0, width: 0, height: 0}; },
  };
  return el;
}

function runEditor(allowCreate) {
  const picked = [];
  global.document = {
    body: element('body'),
    createElement: element,
    addEventListener() {},
    removeEventListener() {},
  };
  global.window = {
    openRefPicker(sel) { picked.push(sel); },
    addEventListener() {},
    removeEventListener() {},
  };
  global.clearTimeout = () => {};
  global.setTimeout = () => 0;

  const container = element('div');
  const column = {field: 'Клиент', refEntity: 'Клиент'};
  if (allowCreate) column.allowCreate = true;
  const args = {
    column,
    item: {Клиент: ''},
    container,
    commitChanges() {},
    cancelChanges() {},
    grid: {getOptions() { return {}; }},
  };
  const factory = new Function('args', 'refField', 'refOptsList', editorSource + '\nreturn new ObRefEditor(refField, refOptsList, args);');
  factory(args, 'Клиент', [{id: 'id-1', _label: 'ООО Ромашка'}]);

  // «…» в ячейке открывает общую форму подбора.
  const wrapper = container.children[0];
  const dropBtn = wrapper.children[1];
  dropBtn.dispatch('click');
  assert.equal(picked.length, 1, 'редактор не открыл форму подбора');
  return picked[0];
}

test('колонка с allow_inline_create даёт подбору «+ Создать»', () => {
  const sel = runEditor(true);
  assert.equal(sel.getAttribute('data-ref-entity'), 'Клиент');
  assert.equal(sel.getAttribute('data-ref-allow-create'), '1');
});

test('без разрешения колонки создание из подбора недоступно', () => {
  const sel = runEditor(false);
  assert.equal(sel.getAttribute('data-ref-entity'), 'Клиент');
  assert.equal(sel.getAttribute('data-ref-allow-create'), null);
});
