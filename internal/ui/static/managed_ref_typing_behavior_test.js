'use strict';

// Ввод по строке у ссылочного поля (input_by_string): поведение настоящих
// функций из managed.js. Функции вырезаются из файла, а не копируются в тест —
// копия разошлась бы с оригиналом, и тест утверждал бы про код, которого нет.

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

const helpers = [
  extract('obRefTypingMin'),
  extract('obRefTypingReady'),
  extract('obRefTypingLabel'),
  extract('obRefTypingDecide'),
  extract('obRefTypingMount'),
  extract('obRefTypingUnresolved'),
].join('\n');

function load(window) {
  return new Function('window', 'document', 'Event', 'setTimeout', 'clearTimeout', 'fetch',
    helpers + '\nreturn {obRefTypingMin, obRefTypingReady, obRefTypingLabel, obRefTypingDecide, obRefTypingMount, obRefTypingUnresolved};',
  )(window, window.document, window.Event, window.setTimeout, window.clearTimeout, window.fetch);
}

// ── Крошечный DOM: ровно те операции, которые делает монтирование поля ──────
function element(tag, attrs) {
  const el = {
    tagName: String(tag).toUpperCase(),
    style: {},
    children: [],
    listeners: {},
    textContent: '',
    // Как у настоящего <select>: options — живые варианты списка.
    options: [],
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(el._attrs, name) ? el._attrs[name] : null; },
    setAttribute(name, value) { el._attrs[name] = String(value); },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(el._attrs, name); },
    addEventListener(type, fn) { (el.listeners[type] = el.listeners[type] || []).push(fn); },
    dispatchEvent(event) {
      (el.listeners[event.type] || []).forEach((fn) => fn(event));
      return true;
    },
    appendChild(child) {
      el.children.push(child);
      child.parentNode = el;
      if (el.tagName === 'SELECT' && child.tagName === 'OPTION') el.options.push(child);
      return child;
    },
    scrollIntoView() {},
    querySelector(selector) { return find(el, selector); },
    querySelectorAll(selector) { return findAll(el, selector); },
  };
  el._attrs = Object.assign({}, attrs || {});
  return el;
}

function matches(el, selector) {
  if (selector.startsWith('.')) return (el._attrs.class || '').split(/\s+/).includes(selector.slice(1));
  const attr = /^\[([^\]=]+)(?:="([^"]*)")?\]$/.exec(selector);
  if (attr) return el.hasAttribute(attr[1]) && (attr[2] === undefined || el.getAttribute(attr[1]) === attr[2]);
  return false;
}

function find(el, selector) {
  for (const child of el.children) {
    if (matches(child, selector)) return child;
    const nested = find(child, selector);
    if (nested) return nested;
  }
  return null;
}

function findAll(el, selector) {
  const out = [];
  for (const child of el.children) {
    if (matches(child, selector)) out.push(child);
    out.push(...findAll(child, selector));
  }
  return out;
}

function option(value, label) {
  const opt = element('option', {});
  opt.value = value;
  opt.textContent = label;
  return opt;
}

// Поле: скрытый носитель значения + видимый текст + список подсказки —
// ровно та разметка, которую рисует templates_managed.go (select и .ob-ref-combo
// лежат рядом в .managed-control-row).
function field(options) {
  const row = element('div', {class: 'managed-control-row'});
  const select = element('select', {
    id: 'ref-Склад', name: 'Склад', 'data-ref-entity': 'Склад', 'data-ob-ref-typing': '1',
  });
  (options || []).forEach((o) => select.appendChild(option(o.id, o.label)));

  const combo = element('div', {class: 'ob-ref-combo'});
  const input = element('input', {'data-ob-ref-text': 'ref-Склад'});
  input.value = '';
  const list = element('div', {class: 'ob-ref-suggest'});
  list.style.display = 'none';
  combo.appendChild(input);
  combo.appendChild(list);

  row.appendChild(select);
  row.appendChild(combo);

  return {select, input, list, combo, row};
}

function browser(rows, requests) {
  const created = [];
  const doc = {
    activeElement: null,
    createElement(tag) {
      const el = element(tag, {});
      created.push(el);
      return el;
    },
  };
  const win = {
    document: doc,
    Event: class { constructor(type) { this.type = type; } },
    setTimeout(fn) { fn(); return 1; },
    clearTimeout() {},
    fetch(url) {
      if (requests) requests.push(url);
      return Promise.resolve({ok: true, json: () => Promise.resolve({items: rows(url)})});
    },
  };
  return win;
}

async function settle() {
  for (let i = 0; i < 6; i++) await Promise.resolve();
}

test('порог ввода — три символа, и считается символ, а не байт', () => {
  const api = load(browser(() => []));
  assert.equal(api.obRefTypingMin(), 3);
  assert.equal(api.obRefTypingReady('Ив', 3), false);
  assert.equal(api.obRefTypingReady('Ива', 3), true);
  assert.equal(api.obRefTypingReady('  Ива  ', 3), true);
  assert.equal(api.obRefTypingReady('ab', 3), false);
});

test('решение по ответу: один — подставить, пусто — «не найдено», много — список', () => {
  const api = load(browser(() => []));
  assert.equal(api.obRefTypingDecide([]).kind, 'empty');
  assert.equal(api.obRefTypingDecide([{id: '1'}]).kind, 'single');
  assert.equal(api.obRefTypingDecide([{id: '1'}, {id: '2'}]).kind, 'list');
  assert.equal(api.obRefTypingLabel({_label: 'Склад'}), 'Склад');
  assert.equal(api.obRefTypingLabel({}), '');
});

test('единственное совпадение подставляется само и уходит ссылкой', async () => {
  const requests = [];
  const rows = () => [{id: 'uuid-1', _label: 'Основной склад'}];
  const win = browser(rows, requests);
  const f = field([{id: '', label: '— выбрать —'}]);
  load(win).obRefTypingMount(f.select);

  let fired = 0;
  f.select.addEventListener('change', () => { fired++; });
  win.document.activeElement = f.input;
  f.input.value = 'Основ';
  f.input.dispatchEvent(new win.Event('input'));
  await settle();

  assert.equal(f.input.value, 'Основной склад');
  assert.equal(f.select.value, 'uuid-1');
  assert.equal(fired, 1);
  assert.equal(f.select.style.display, 'none', 'скрытый список остаётся носителем значения');
  assert.equal(f.combo.style.display, 'block');
  assert.ok(requests[0].includes('by=name'), 'поиск ограничен наименованием: ' + requests[0]);
  assert.ok(requests[0].includes('q=' + encodeURIComponent('Основ')));
});

test('несколько совпадений — выпадающий список, выбор стрелками и Enter', async () => {
  const rows = () => [{id: 'uuid-1', _label: 'Склад А'}, {id: 'uuid-2', _label: 'Склад Б'}];
  const win = browser(rows);
  const f = field([]);
  load(win).obRefTypingMount(f.select);

  win.document.activeElement = f.input;
  f.input.value = 'Склад';
  f.input.dispatchEvent(new win.Event('input'));
  await settle();

  assert.equal(f.list.style.display, 'block');
  assert.equal(f.list.children.length, 2);
  assert.equal(f.list.children[0].textContent, 'Склад А');

  f.input.dispatchEvent({type: 'keydown', key: 'ArrowDown', preventDefault() {}});
  const handled = {type: 'keydown', key: 'Enter', preventDefault() { handled.prevented = true; }, prevented: false};
  f.input.dispatchEvent(handled);
  assert.equal(handled.prevented, true, 'Enter выбирает пункт, а не отправляет форму');
  assert.equal(f.select.value, 'uuid-2');
  assert.equal(f.input.value, 'Склад Б');
  assert.equal(f.list.style.display, 'none');
});

test('ничего не найдено — так и написано, значение не подменяется', async () => {
  const win = browser(() => []);
  const f = field([{id: 'uuid-old', label: 'Прежний'}]);
  load(win).obRefTypingMount(f.select);

  win.document.activeElement = f.input;
  f.input.value = 'Неттакого';
  f.input.dispatchEvent(new win.Event('input'));
  await settle();

  assert.equal(f.list.style.display, 'block');
  assert.match(f.list.children[0].textContent, /не найдено/);
});

test('короткий ввод не ищет и не шлёт запрос', async () => {
  const requests = [];
  const win = browser(() => [{id: 'uuid-1', _label: 'Склад'}], requests);
  const f = field([]);
  load(win).obRefTypingMount(f.select);

  win.document.activeElement = f.input;
  f.input.value = 'Ск';
  f.input.dispatchEvent(new win.Event('input'));
  await settle();

  assert.deepEqual(requests, []);
  assert.equal(f.list.style.display, 'none');
});

test('стёртый текст очищает ссылку', () => {
  const win = browser(() => []);
  const f = field([{id: 'uuid-1', label: 'Склад'}]);
  f.select.value = 'uuid-1';
  f.input.value = 'Склад';
  load(win).obRefTypingMount(f.select);

  f.input.value = '';
  f.input.dispatchEvent(new win.Event('input'));
  assert.equal(f.select.value, '');
});

// Набранный текст без выбранной ссылки нельзя отправить молча: иначе в базу
// ушла бы пустая ссылка при заполненном на вид поле.
test('незавершённый ввод останавливает отправку формы', () => {
  const win = browser(() => []);
  const f = field([{id: 'uuid-1', label: 'Склад'}]);
  const api = load(win);
  api.obRefTypingMount(f.select);

  assert.equal(api.obRefTypingUnresolved(f.select, f.input), null, 'пустое поле не блокирует форму');

  f.input.value = 'Склад Х';
  assert.equal(api.obRefTypingUnresolved(f.select, f.input), f.input, 'набранный текст без ссылки — блокирует');

  // Точное совпадение с загруженным вариантом подставляется само: пользователь
  // мог выбрать вариант мышью и потом переписать текст тем же значением.
  f.input.value = 'Склад';
  assert.equal(api.obRefTypingUnresolved(f.select, f.input), null);
  assert.equal(f.select.value, 'uuid-1', 'точное совпадение подписи восстанавливает ссылку');
});
