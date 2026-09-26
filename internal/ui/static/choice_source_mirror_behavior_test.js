// Источник отбора, у которого есть скрытое зеркало значения.
//
// У поля с readonly_when платформа рисует ДВА элемента с одним именем: сам
// контрол и зеркало data-ob-ro-mirror (план 181C/#1672 — отключённый select
// браузер не отправляет). form.elements.namedItem тогда отдаёт RadioNodeList,
// и его .value пуст, раз это не радиокнопки. Снимок фильтра уезжал с пустым
// источником, и зависимый подбор показывал пустой список: поле после выбора
// направления разблокировано, а выбирать не из чего.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/ui.js', 'utf8');
const start = source.indexOf('function obRefChoiceSnapshot(sel)');
const end = source.indexOf('function openRefPicker(selOrId)', start);
if (start < 0 || end < 0) throw new Error('choice_filter block is absent from ui.js');
const block = source.slice(start, end);

// Список одноимённых элементов, как его отдаёт form.elements.namedItem:
// без tagName, с length — тот самый RadioNodeList.
function namedList(items) {
  const list = {length: items.length, value: ''};
  items.forEach((item, i) => { list[i] = item; });
  return list;
}

function runtime(items) {
  const attrs = {
    'data-ref-choice-context': JSON.stringify({
      form_entity: 'А_Звонок',
      form: 'ФормаОбъекта',
      element: 'ПолеЗаявленнаяНеисправность',
      sources: {'Объект.Направление.ГруппаНеисправностей': 'Направление'},
    }),
    'data-ref-entity': 'А_Неисправности',
  };
  const select = {
    value: '',
    form: {elements: {namedItem: (name) => (name === 'Направление' ? namedList(items) : null)}},
    getAttribute: (name) => (Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null),
  };
  const document = {
    documentElement: {contains: () => true},
    querySelectorAll: () => [],
    getElementsByName: () => [],
    getElementById: () => null,
    addEventListener: () => {},
  };
  const sandbox = {
    window: {}, document, Promise, JSON, Array, Object, String, Error,
    encodeURIComponent, obReady() {},
  };
  vm.createContext(sandbox);
  vm.runInContext(block, sandbox, {filename: 'ui.js#choice-source'});
  return sandbox.obRefChoiceSnapshot(select);
}

function sources(snapshot) {
  const m = /&sources=([^&]*)/.exec(snapshot.query);
  return JSON.parse(decodeURIComponent(m[1]));
}

test('разблокированное поле: источник берётся из контрола, а не из зеркала', () => {
  // Поле открыто: select везёт значение, зеркало выключено и пусто.
  const snapshot = runtime([
    {tagName: 'SELECT', value: 'напр-хд', disabled: false},
    {tagName: 'INPUT', value: '', disabled: true},
  ]);
  assert.equal(sources(snapshot)['Объект.Направление.ГруппаНеисправностей'], 'напр-хд');
});

test('запертое поле: источник берётся из зеркала', () => {
  // Поле под запретом: select отключён, значение везёт зеркало.
  const snapshot = runtime([
    {tagName: 'SELECT', value: 'напр-хд', disabled: true},
    {tagName: 'INPUT', value: 'напр-хд', disabled: false},
  ]);
  assert.equal(sources(snapshot)['Объект.Направление.ГруппаНеисправностей'], 'напр-хд');
});

test('источник не заполнен — отбор пуст, а не сорван', () => {
  const snapshot = runtime([
    {tagName: 'SELECT', value: '', disabled: false},
    {tagName: 'INPUT', value: '', disabled: true},
  ]);
  assert.equal(sources(snapshot)['Объект.Направление.ГруппаНеисправностей'], '');
});
