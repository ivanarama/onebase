// Строка поиска диалога подбора при Конфиг.ПоискНаСервере обязана спрашивать
// СЕРВЕР, а не фильтровать уже приехавшие строки. Регрессия здесь не видна
// глазами: диалог выглядит одинаково, просто перестаёт находить то, чего нет в
// привезённой выдаче (предел ВЫБРАТЬ ПЕРВЫЕ N) или что показано маской ПДн.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');

const source = fs.readFileSync('static/ui.js', 'utf8');

function block(from, start) {
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') {
      depth--;
      if (depth === 0) return source.slice(from, i + 1);
    }
  }
  throw new Error('не закрыт блок с позиции ' + start);
}

function extract(name) {
  const start = source.indexOf('function ' + name);
  if (start < 0) throw new Error('в ui.js нет функции ' + name);
  return block(start, start);
}

// Состояние диалога живёт модульной переменной, а не в замыкании: подмена её
// заглушкой скрыла бы ровно те дефекты, ради которых тест написан.
function extractVar(name) {
  const start = source.indexOf('var ' + name + ' = {');
  if (start < 0) throw new Error('в ui.js нет переменной ' + name);
  return block(start, start) + ';';
}

function extractWindowFn(name) {
  const start = source.indexOf('window.' + name + ' = function');
  if (start < 0) throw new Error('в ui.js нет window.' + name);
  return 'var ' + name + ' = ' + block(source.indexOf('function', start), start) + ';';
}

// Узел ровно того объёма, который трогает openItemPicker: дерево, атрибуты,
// подписки и строки tbody. Настоящего DOM в тестах нет намеренно — проверяем
// поведение функции, а не браузер.
// Селектор ровно того вида, какой встречается в openItemPicker: `.класс`,
// `тег[data-col="X"]`, `.класс[data-col="X"]`, `тег`.
function matches(el, sel) {
  const m = /^(?:([a-z]+))?(?:\.([\w-]+))?(?:\[data-col="([^"]*)"\])?$/.exec(sel);
  if (!m) throw new Error('тест не умеет селектор ' + sel);
  const [, tag, cls, col] = m;
  if (tag && el.tagName !== tag.toUpperCase()) return false;
  if (cls && !String(el.className || '').split(/\s+/).includes(cls)) return false;
  if (col !== undefined && el.getAttribute('data-col') !== col) return false;
  return true;
}

function walk(el, visit) {
  el.children.forEach((child) => { visit(child); walk(child, visit); });
}

function node(tag) {
  return {
    tagName: String(tag || '').toUpperCase(),
    children: [],
    rows: [],
    parent: null,
    attrs: {},
    listeners: {},
    style: {cssText: '', display: ''},
    value: '',
    textContent: '',
    innerHTML: '',
    checked: false,
    type: '',
    placeholder: '',
    autocomplete: '',
    colSpan: 0,
    className: '',
    appendChild(child) {
      this.children.push(child);
      child.parent = this;
      if (this.tagName === 'TBODY' && child.tagName === 'TR') this.rows.push(child);
      return child;
    },
    setAttribute(name, value) { this.attrs[name] = String(value); },
    getAttribute(name) {
      return Object.prototype.hasOwnProperty.call(this.attrs, name) ? this.attrs[name] : null;
    },
    addEventListener(type, fn) { (this.listeners[type] = this.listeners[type] || []).push(fn); },
    dispatch(type, event) {
      (this.listeners[type] || []).forEach((fn) => fn.call(this, event || {}));
      // Обработчик input у tbody делегированный: событие всплывает.
      if (this.parent && type === 'input') this.parent.dispatch(type, event || {target: this});
    },
    querySelector(sel) {
      let found = null;
      walk(this, (el) => { if (!found && matches(el, sel)) found = el; });
      return found;
    },
    querySelectorAll(sel) {
      const out = [];
      walk(this, (el) => { if (matches(el, sel)) out.push(el); });
      return out;
    },
    closest(sel) {
      for (let cur = this; cur; cur = cur.parent) if (matches(cur, sel)) return cur;
      return null;
    },
    focus() { this.focused = true; },
    setSelectionRange(from, to) { this.caret = [from, to]; },
    remove() {
      this.removed = true;
      if (this.parent) {
        const at = this.parent.children.indexOf(this);
        if (at >= 0) this.parent.children.splice(at, 1);
        this.parent = null;
      }
    },
    get classList() {
      const self = this;
      return {contains(name) { return String(self.className || '').split(/\s+/).includes(name); }};
    },
  };
}

// Диалог собирается из элементов подряд, поэтому строку поиска находим по типу:
// это единственный input вне таблицы.
function findSearchInput(box) {
  return box.children.find((el) => el.tagName === 'INPUT' && el.type === 'text') || null;
}

// Одна среда на несколько открытий: ответ сервера приходит тем же pickerData,
// и клиент СОБИРАЕТ ДИАЛОГ ЗАНОВО. Состояние строки поиска обязано это пережить,
// иначе набранное пропадает после первой же буквы — проверить это можно только
// двумя вызовами в одном контексте.
function pickerContext() {
  const timers = [];
  const fired = [];
  const body = node('body');
  const document = {
    getElementById() { return null; },
    createElement(tag) { return node(tag); },
    body,
  };
  let modal = null;
  document.getElementById = (id) => (id === '_item-picker-modal' ? modal : null);
  const api = new Function(
    'document', 'window', 'obFire', 'setTimeout', 'clearTimeout',
    extractVar('obPickerSearch') + '\n' +
      extract('obPickerForget') + '\n' +
      extract('obPickerSendSearch') + '\n' +
      extract('obPickerSearchApplied') + '\n' +
      extract('obPickerFirePending') + '\n' +
      extractWindowFn('obPickerSearchEmpty') + '\n' +
      extract('openItemPicker') + '\n' +
      'return {openItemPicker: openItemPicker, searchEmpty: obPickerSearchEmpty,' +
      ' state: function () { return obPickerSearch; }};',
  )(
    document,
    {},
    function (element, event, params) { fired.push({element, event, params}); },
    function (fn) { timers.push(fn); return timers.length; },
    function () { timers.length = 0; },
  );
  function box() { return modal ? modal.children[0] : null; }
  function foot() {
    const children = box().children;
    return children[children.length - 1];
  }
  return {
    fired,
    state: api.state,
    searchEmpty: api.searchEmpty,
    modal() { return modal; },
    flush() { timers.splice(0).forEach((fn) => fn()); },
    rowsOf() { return box().children[2].children[0].children[1].rows; },
    cancel() { foot().children[0].dispatch('click'); },
    transfer() { foot().children[1].dispatch('click'); },
    escape() { if (modal && modal._obClose) modal._obClose(); },
    open(payload, elementName, eventContext) {
      body.children.length = 0;
      api.openItemPicker(payload, elementName, eventContext || null);
      modal = body.children[0] || null;
      if (!modal) return {search: null};
      return {search: findSearchInput(box())};
    },
  };
}

const columns = [{name: 'Номер', title: 'Заявка №', type: 'string'}];
const rows = [{id: 'u-1', data: {Номер: 'ЗАЯ-000001'}}];

test('при ПоискНаСервере набранное уходит событием Поиск', () => {
  const ctx = pickerContext();
  const picker = ctx.open(
    {columns, rows, config: {title: 'Подбор', serverSearch: true}},
    'КнопкаНайти',
    {_tp: 'Строки'},
  );
  assert.ok(picker.search, 'строка поиска не найдена');

  picker.search.value = 'гай';
  picker.search.dispatch('input');
  assert.deepEqual(ctx.fired, [], 'запрос ушёл без задержки — сервер дёрнут на каждую букву');

  ctx.flush();
  assert.equal(ctx.fired.length, 1);
  assert.equal(ctx.fired[0].element, 'КнопкаНайти');
  assert.equal(ctx.fired[0].event, 'Поиск');
  assert.equal(ctx.fired[0].params._pick_query, 'гай');
  assert.equal(ctx.fired[0].params._tp, 'Строки', 'контекст события потерян');
});

test('без флага строка поиска остаётся клиентским фильтром', () => {
  const ctx = pickerContext();
  const picker = ctx.open({columns, rows, config: {title: 'Подбор'}}, 'КнопкаНайти', null);
  picker.search.value = 'гай';
  picker.search.dispatch('input');
  ctx.flush();
  assert.deepEqual(ctx.fired, [], 'клиентский фильтр не должен ходить на сервер');
});

test('ответ сервера пересобирает окно: набранное на месте, каретка в конце', () => {
  const ctx = pickerContext();
  const config = {title: 'Подбор', serverSearch: true};
  const first = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  first.search.value = '111222';
  first.search.dispatch('input');
  ctx.flush();

  // Сервер ответил своим pickerData — клиент открывает диалог заново.
  const second = ctx.open({columns, rows: [], config}, 'КнопкаНайти', null);
  assert.equal(second.search.value, '111222', 'набранное пропало при пересборке окна');
  assert.deepEqual(second.search.caret, [6, 6], 'каретка не поставлена в конец строки');
});

test('закрытое окно не подставляет старый запрос в следующее открытие', () => {
  const ctx = pickerContext();
  const config = {title: 'Подбор', serverSearch: true};
  const picker = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  picker.search.value = 'гай';
  picker.search.dispatch('input');
  ctx.flush();

  const other = ctx.open({columns, rows, config}, 'ДругаяКнопка', null);
  assert.equal(other.search.value, '', 'запрос от чужой кнопки подставился в её диалог');
});

test('отмена гасит таймер: запрос после закрытия окна не уходит', () => {
  const ctx = pickerContext();
  const config = {title: 'Подбор', serverSearch: true};
  const picker = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  picker.search.value = 'гай';
  picker.search.dispatch('input');
  ctx.cancel();
  ctx.flush();
  assert.deepEqual(ctx.fired, [], 'таймер debounce пережил закрытие диалога');
  assert.equal(ctx.state().element, '', 'состояние диалога не очищено');
});

test('Esc закрывает диалог тем же путём, что «Отмена»', () => {
  const ctx = pickerContext();
  const config = {title: 'Подбор', serverSearch: true};
  const picker = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  picker.search.value = 'гай';
  picker.search.dispatch('input');
  ctx.escape();
  ctx.flush();
  assert.deepEqual(ctx.fired, [], 'после Esc запрос всё равно ушёл');
  assert.equal(ctx.state().element, '');
});

test('ответ, пришедший после закрытия, не открывает окно заново', () => {
  const ctx = pickerContext();
  const config = {title: 'Подбор', serverSearch: true};
  const picker = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  picker.search.value = 'гай';
  picker.search.dispatch('input');
  ctx.flush();
  assert.equal(ctx.fired.length, 1, 'запрос не ушёл');
  ctx.cancel();

  const late = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  assert.equal(late.search, null, 'запоздалый ответ заново открыл закрытое окно');
  assert.equal(ctx.modal(), null);
});

test('два поиска подряд не идут параллельно: второй ждёт ответа на первый', () => {
  const ctx = pickerContext();
  const config = {title: 'Подбор', serverSearch: true};
  const picker = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  picker.search.value = 'га';
  picker.search.dispatch('input');
  ctx.flush();
  assert.equal(ctx.fired.length, 1);
  assert.equal(ctx.fired[0].params._pick_query, 'га');

  // Пока первый запрос в пути, человек дописывает букву.
  picker.search.value = 'гай';
  picker.search.dispatch('input');
  ctx.flush();
  assert.equal(ctx.fired.length, 1, 'второй запрос ушёл параллельно первому');

  // Ответ на первый пришёл — только теперь уходит второй.
  const second = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  assert.ok(second.search, 'ответ не открыл диалог');
  assert.equal(ctx.fired.length, 2, 'отложенный запрос не ушёл после ответа');
  assert.equal(ctx.fired[1].params._pick_query, 'гай');
});

test('выбор переживает смену выдачи и уходит в «Перенести» целиком', () => {
  const ctx = pickerContext();
  const config = {title: 'Подбор', serverSearch: true};
  const first = ctx.open(
    {columns, rows: [{id: 'u-1', data: {Номер: 'ЗАЯ-000001'}}], config},
    'КнопкаНайти',
    null,
  );
  const firstRows = ctx.rowsOf();
  assert.equal(firstRows.length, 1);
  const firstCb = firstRows[0].querySelector('._ip-cb');
  firstCb.checked = true;
  firstCb.onchange();

  first.search.value = 'вторая';
  first.search.dispatch('input');
  ctx.flush();

  // Вторая выдача без первой строки: выбор по прежнему запросу обязан выжить.
  ctx.open({columns, rows: [{id: 'u-2', data: {Номер: 'ЗАЯ-000002'}}], config}, 'КнопкаНайти', null);
  const secondRows = ctx.rowsOf();
  assert.equal(secondRows.length, 1);
  const secondCb = secondRows[0].querySelector('._ip-cb');
  secondCb.checked = true;
  secondCb.onchange();

  ctx.transfer();
  const choice = ctx.fired.find((f) => f.event === 'Выбор');
  assert.ok(choice, 'событие Выбор не отправлено');
  const result = JSON.parse(choice.params._pick_result);
  assert.deepEqual(
    result.map((r) => r.id),
    ['u-1', 'u-2'],
    '«Перенести» вернул только выбор последней выдачи',
  );
  assert.equal(result[0]['Номер'], 'ЗАЯ-000001');
});

test('снятая отметка забывается и в «Перенести» не попадает', () => {
  const ctx = pickerContext();
  const config = {title: 'Подбор', serverSearch: true};
  ctx.open({columns, rows: [{id: 'u-1', data: {Номер: 'ЗАЯ-000001'}}], config}, 'КнопкаНайти', null);
  const cb = ctx.rowsOf()[0].querySelector('._ip-cb');
  cb.checked = true;
  cb.onchange();
  cb.checked = false;
  cb.onchange();
  ctx.transfer();
  const choice = ctx.fired.find((f) => f.event === 'Выбор');
  assert.deepEqual(JSON.parse(choice.params._pick_result), []);
});

test('пустой ответ поиска не держит очередь: следующий запрос уходит', () => {
  const ctx = pickerContext();
  const config = {title: 'Подбор', serverSearch: true};
  const picker = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  picker.search.value = 'га';
  picker.search.dispatch('input');
  ctx.flush();
  assert.equal(ctx.fired.length, 1);

  picker.search.value = 'гай';
  picker.search.dispatch('input');
  ctx.flush();
  assert.equal(ctx.fired.length, 1, 'второй запрос ушёл параллельно');

  // Обработчик ничего не показал: диалог остаётся, строки чистятся.
  ctx.searchEmpty();
  assert.equal(ctx.fired.length, 2, 'после пустого ответа отложенный запрос не ушёл');
  assert.equal(ctx.fired[1].params._pick_query, 'гай');
});
