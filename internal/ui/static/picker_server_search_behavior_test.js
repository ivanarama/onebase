// Строка поиска диалога подбора при Конфиг.ПоискНаСервере обязана спрашивать
// СЕРВЕР, а не фильтровать уже приехавшие строки. Регрессия здесь не видна
// глазами: диалог выглядит одинаково, просто перестаёт находить то, чего нет в
// привезённой выдаче (предел ВЫБРАТЬ ПЕРВЫЕ N) или что показано маской ПДн.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');

const source = fs.readFileSync('static/ui.js', 'utf8');
const managedSource = fs.readFileSync('static/managed.js', 'utf8');
// Исполняем состояние и весь диалог, а для сетевых регрессий — настоящую
// публичную obFire. Подменены только DOM и внешние зависимости отправки формы.
const pickerSource = source.slice(source.indexOf('var obPickerSearch = {'), source.indexOf('\nfunction openRefPicker('));
// После слияния с конвейером очереди событий (#1557/#1528) настоящий obFire
// тянет за собой snapshot/dispatch и close-контроллер: вырезаем весь домен.
const controlStateSource = managedSource.slice(managedSource.indexOf('  function controlSubmittable('), managedSource.indexOf('  function applyValues('));
// Тост сообщений модуля (obFlashMessage) живёт в том же IIFE, что obFire.
const messageStatusSource = managedSource.slice(managedSource.indexOf('  // BEGIN onebase-message-status'), managedSource.indexOf('  // END onebase-message-status'));
const fireSource = managedSource.slice(managedSource.indexOf('  // obFire(elementName'), managedSource.indexOf('\n  // Отслеживание'));

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
    set innerHTML(value) { this.children = []; this.rows = []; this.html = value; },
    get innerHTML() { return this.html || ''; },
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
function pickerContext(managed = false) {
  const timers = [];
  const fired = [];
  const requests = [];
  const messages = [];
  const applied = [];
  const body = node('body');
  const form = {querySelectorAll() { return []; }, querySelector() { return null; }};
  const document = {
    getElementById(id) { return id === 'main-form' ? form : body.children.find((el) => el.id === id) || null; },
    createElement(tag) { return node(tag); },
    // close-контроллер подписывается на submit/keydown при инициализации.
    listeners: {},
    addEventListener(type, fn) { (this.listeners[type] = this.listeners[type] || []).push(fn); },
    body,
  };
  const window = {obManagedApplyTablePartRefOptions() {}, applyTableParts() {}};
  window.obFire = function (element, event, params, request) { fired.push({element, event, params, request}); };
  const api = new Function(
    'document', 'window', 'obFire', 'setTimeout', 'clearTimeout', 'obReady',
    pickerSource + '\n' +
      'return {openItemPicker: openItemPicker, searchEmpty: window.obPickerSearchEmpty,' +
      ' state: function () { return obPickerSearch; }};',
  )(
    document,
    window,
    (...args) => window.obFire(...args),
    function (fn) { timers.push(fn); return timers.length; },
    function () { timers.length = 0; },
    // main вынес инициализацию диалога под obReady; в песочнице DOM готов всегда.
    (fn) => fn(),
  );
  if (managed) {
    const deps = {
      window, document, URLSearchParams,
      FormData: class extends Map { constructor() { super(); } },
      URL: '/ui/test/form-event', DOC_ID: '',
      awaitCurrentFileReads: async () => true,
      serviceField: (name) => name,
      obManagedWritableTableBody: () => null,
      openItemPicker: api.openItemPicker,
      flash: (message) => messages.push(message),
      applyFormConditionalCSS() {}, applyElementStates() {},
      applyValues: (values) => applied.push(values),
      applyChoiceList() {}, applyFormTables() {},
      fetch(url, options) {
        return new Promise((resolve, reject) => requests.push({url, options, resolve, reject}));
      },
      // close-контроллер читает конфигурацию на инициализации.
      cfg: {},
    };
    new Function(...Object.keys(deps), messageStatusSource + controlStateSource + fireSource)(...Object.values(deps));
  }
  function modal() { return document.getElementById('_item-picker-modal'); }
  function box() { return modal() ? modal().children[0] : null; }
  function foot() {
    const children = box().children;
    return children[children.length - 1];
  }
  return {
    fired,
    requests, messages, applied, window,
    fire: (...args) => window.obFire(...args),
    async settle(index, data) {
      // Контракт конвейера: HTTP 200 и обязательный ok:true в конверте.
      requests[index].resolve({ok: true, json: async () => Object.assign({ok: true}, data)});
      await this.tick();
    },
    tick: () => new Promise((resolve) => setImmediate(resolve)),
    state: api.state,
    searchEmpty() { api.searchEmpty(fired[fired.length - 1].request); },
    modal,
    flush() { timers.splice(0).forEach((fn) => fn()); },
    rowsOf() { return box().children[2].children[0].children[1].rows; },
    cancel() { foot().children[0].dispatch('click'); },
    transfer() { foot().children[1].dispatch('click'); },
    escape() { if (modal() && modal()._obClose) modal()._obClose(); },
    search() { return box() && findSearchInput(box()); },
    open(payload, elementName, eventContext, request) {
      api.openItemPicker(payload, elementName, eventContext || null, request);
      if (!modal()) return {search: null};
      return {search: findSearchInput(box())};
    },
    respond(payload, elementName, eventContext) {
      return this.open(payload, elementName, eventContext, fired[fired.length - 1].request);
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
  const second = ctx.respond({columns, rows: [], config}, 'КнопкаНайти', null);
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

  const late = ctx.respond({columns, rows, config}, 'КнопкаНайти', null);
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
  const second = ctx.respond({columns, rows, config}, 'КнопкаНайти', null);
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
  ctx.respond({columns, rows: [{id: 'u-2', data: {Номер: 'ЗАЯ-000002'}}], config}, 'КнопкаНайти', null);
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

test('ОдинВыбор с серверным поиском сохраняет клик и заменяет выбор в следующей выдаче', () => {
  const ctx = pickerContext();
  const config = {title: 'Подбор', serverSearch: true, single: true};
  const first = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  ctx.rowsOf()[0].dispatch('click', {target: ctx.rowsOf()[0]});
  assert.deepEqual(ctx.state().order, ['u-1'], 'клик по строке не запомнил radio');

  first.search.value = 'следующая';
  first.search.dispatch('input');
  ctx.flush();
  ctx.respond({columns, rows: [{id: 'u-2', data: {Номер: 'ЗАЯ-000002'}}], config}, 'КнопкаНайти', null);
  const next = ctx.rowsOf()[0];
  const radio = next.querySelector('._ip-cb');
  radio.checked = true;
  radio.onchange();
  assert.deepEqual(ctx.state().order, ['u-2'], 'radio сохранил старую строку вместе с новой');
  ctx.transfer();
  const choice = ctx.fired.find((f) => f.event === 'Выбор');
  assert.deepEqual(JSON.parse(choice.params._pick_result).map((row) => row.id), ['u-2']);
});

test('ОдинВыбор с серверным поиском отправляет кликнутую строку', () => {
  const ctx = pickerContext();
  ctx.open({columns, rows, config: {serverSearch: true, single: true}}, 'КнопкаНайти', null);
  const tr = ctx.rowsOf()[0];
  tr.dispatch('click', {target: tr});
  ctx.transfer();
  const choice = ctx.fired.find((f) => f.event === 'Выбор');
  assert.deepEqual(JSON.parse(choice.params._pick_result).map((row) => row.id), ['u-1']);
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

test('пустой ответ использует словарь страницы OB_I18N', () => {
  const config = {title: 'Подбор', serverSearch: true};

  // Язык пользователя английский: tplHead отдал словарь, сообщение переводится.
  const ctx = pickerContext();
  ctx.window.OB_I18N = {'Ничего не найдено': 'No matches found'};
  const picker = ctx.open({columns, rows, config}, 'КнопкаНайти', null);
  picker.search.value = 'гай';
  picker.search.dispatch('input');
  ctx.flush();
  ctx.searchEmpty();
  const td = ctx.modal().querySelectorAll('td').find((el) => el.textContent);
  assert.equal(td.textContent, 'No matches found');

  // Без словаря — ключ, то есть русский текст.
  const ctx2 = pickerContext();
  const picker2 = ctx2.open({columns, rows, config}, 'КнопкаНайти', null);
  picker2.search.value = 'гай';
  picker2.search.dispatch('input');
  ctx2.flush();
  ctx2.searchEmpty();
  const td2 = ctx2.modal().querySelectorAll('td').find((el) => el.textContent);
  assert.equal(td2.textContent, 'Ничего не найдено');
});

const serverConfig = {title: 'Подбор', serverSearch: true};
const pickerData = {columns, rows, config: serverConfig};

async function openManaged(ctx, element = 'КнопкаНайти') {
  const opening = ctx.fire(element, 'Нажатие');
  await ctx.tick();
  await ctx.settle(ctx.requests.length - 1, {pickerData});
  await opening;
  assert.ok(ctx.search(), 'obFire не открыл подбор');
}

async function searchManaged(ctx, query) {
  ctx.search().value = query;
  ctx.search().dispatch('input');
  ctx.flush();
  await ctx.tick();
}

for (const failure of ['fetch', 'json', 'server']) {
  test('реальная obFire освобождает поиск после ошибки ' + failure, async () => {
    const ctx = pickerContext(true);
    await openManaged(ctx);
    await searchManaged(ctx, 'первый');
    await searchManaged(ctx, 'повтор');
    assert.equal(ctx.requests.length, 2, 'поиски ушли параллельно');
    const error = new Error('temporary failure');
    if (failure === 'fetch') ctx.requests[1].reject(error);
    else if (failure === 'server') ctx.requests[1].resolve({ok: true, json: async () => ({ok: false, error: error.message})});
    else ctx.requests[1].resolve({json: async () => { throw error; }});
    await ctx.tick();
    assert.equal(ctx.requests.length, 3, 'ошибка навсегда заблокировала pending');
    assert.equal(ctx.requests[2].options.body.get('_pick_query'), 'повтор');
    assert.ok(ctx.messages.some((m) => m.includes('temporary failure')));
    await ctx.settle(2, {pickerData});
    assert.equal(ctx.search().value, 'повтор');
  });
}

for (const failure of ['veto', 'throw']) {
  test('реальная obFire освобождает неотправленный поиск: ' + failure, async () => {
    const ctx = pickerContext(true);
    await openManaged(ctx);
    ctx.window.obGridSync = () => {
      if (failure === 'throw') throw new Error('editor error');
      return false;
    };
    await searchManaged(ctx, 'не отправлен');
    assert.equal(ctx.requests.length, 1, 'veto не остановил отправку');
    ctx.window.obGridSync = () => true;
    await searchManaged(ctx, 'повтор');
    assert.equal(ctx.requests.length, 2, 'ранний выход оставил поиск заблокированным');
    assert.equal(ctx.requests[1].options.body.get('_pick_query'), 'повтор');
    await ctx.settle(1, {pickerData});
  });
}

test('закрытие во время подготовки obFire отменяет ещё не отправленный поиск', async () => {
  const ctx = pickerContext(true);
  await openManaged(ctx);
  ctx.search().value = 'отменён';
  ctx.search().dispatch('input');
  ctx.flush(); // obFire приостановлена на awaitCurrentFileReads.
  ctx.cancel();
  await ctx.tick();
  assert.equal(ctx.requests.length, 1, 'поиск ушёл после закрытия окна');
  await openManaged(ctx);
  await searchManaged(ctx, 'новый');
  assert.equal(ctx.requests.length, 3);
  await ctx.settle(2, {pickerData});
});

for (const element of ['КнопкаНайти', 'ДругаяКнопка']) {
  for (const order of ['A-first', 'B-first']) {
    for (const response of ['picker', 'empty', 'error']) {
      test(`закрытый поиск A не меняет новое открытие B: ${element}, ${order}, ${response}`, async () => {
        const ctx = pickerContext(true);
        await openManaged(ctx);
        await searchManaged(ctx, 'старый поиск A');
        ctx.cancel();
        const openingB = ctx.fire(element, 'Нажатие');
        await ctx.tick();
        assert.equal(ctx.requests.length, 3);
        const dataB = {columns, rows: [{id: 'b-1', data: {Номер: 'B'}}], config: serverConfig};
        async function lateA() {
          if (response === 'error') {
            ctx.requests[1].reject(new Error('late A'));
            await ctx.tick();
          } else {
            await ctx.settle(1, response === 'picker' ? {pickerData} : {values: {stale: 'A'}});
          }
        }
        if (order === 'A-first') {
          await lateA();
          assert.equal(ctx.modal(), null, 'ответ A заново открыл закрытое окно');
        }
        await ctx.settle(2, {pickerData: dataB});
        await openingB;
        assert.ok(ctx.search(), 'собственный ответ открытия B отброшен');
        assert.equal(ctx.search().value, '', 'B унаследовал запрос A');
        if (order === 'B-first') {
          await searchManaged(ctx, 'поиск B');
          await searchManaged(ctx, 'следующий B');
          assert.equal(ctx.requests.length, 4);
          await lateA();
          assert.equal(ctx.requests.length, 4, 'ответ A освободил запрос B');
          assert.equal(ctx.search().value, 'следующий B');
          await ctx.settle(3, {pickerData: dataB});
          assert.equal(ctx.requests.length, 5, 'поиск B перестал работать');
          await ctx.settle(4, {pickerData: dataB});
        }
        assert.deepEqual(ctx.rowsOf().map((r) => r.getAttribute('data-id')), ['b-1']);
        assert.deepEqual(ctx.applied, [], 'поздний пустой ответ A изменил форму B');
      });
    }
  }
}

// ── Одиночный выбор мышью и с клавиатуры, отборы по колонкам ─────────────────
// Окно выбора 1С выбирает строку двойным щелчком и Enter; здесь одиночный
// подбор раньше выбирал только кнопкой «Выбрать». Отборы (Конфиг.Отборы)
// сравнивают значение колонки целиком: строка поиска ищет подстроку во всех
// колонках и не отличает направление «СМ» от мастера «Смирнов».
const masterColumns = [
  {name: 'Мастер', title: 'Мастер', type: 'string'},
  {name: 'Направление', title: 'Направление', type: 'string'},
  {name: 'Филиал', title: 'Филиал', type: 'string'},
];
const masterRows = [
  {id: 'm-1', data: {Мастер: 'Смирнов', Направление: 'ХД', Филиал: 'МСК'}},
  {id: 'm-2', data: {Мастер: 'Иванов', Направление: 'СМ', Филиал: 'МСК'}},
  {id: 'm-3', data: {Мастер: 'Петров', Направление: 'СМ', Филиал: 'СПБ'}},
  {id: 'm-4', data: {Мастер: 'Сидоров', Направление: 'ТВ', Филиал: 'СПБ'}},
];

function tbodyOf(ctx) { return ctx.modal().querySelector('tbody'); }
function visibleIds(ctx) {
  return tbodyOf(ctx).rows.filter((tr) => tr.style.display !== 'none').map((tr) => tr.getAttribute('data-id'));
}
function filterSelect(ctx, col) { return ctx.modal().querySelector('select[data-col="' + col + '"]'); }
function optionValues(sel) { return sel.children.map((o) => o.value); }
function choose(sel, value) { sel.value = value; sel.dispatch('change'); }
function key(input, name) { input.dispatch('keydown', {key: name, preventDefault() {}}); }
function picked(ctx) {
  const last = ctx.fired[ctx.fired.length - 1];
  assert.ok(last && last.event === 'Выбор', 'событие Выбор не ушло');
  return JSON.parse(last.params._pick_result).map((r) => r.id);
}

test('ОдинВыбор: двойной щелчок выбирает строку и закрывает окно', () => {
  const ctx = pickerContext();
  ctx.open({columns: masterColumns, rows: masterRows, config: {single: true}}, 'КнВыбратьМастера', null);
  tbodyOf(ctx).rows[2].dispatch('dblclick');
  assert.deepEqual(picked(ctx), ['m-3']);
  assert.equal(ctx.modal(), null, 'окно осталось открытым');
});

test('мультивыбор двойным щелчком ничего не отправляет', () => {
  const ctx = pickerContext();
  ctx.open({columns: masterColumns, rows: masterRows, config: {}}, 'КнПодбор', null);
  tbodyOf(ctx).rows[0].dispatch('dblclick');
  assert.deepEqual(ctx.fired, []);
  assert.ok(ctx.modal(), 'мультивыбор закрылся двойным щелчком');
});

test('ОдинВыбор: Enter без отметки выбирает первую видимую строку', () => {
  const ctx = pickerContext();
  const picker = ctx.open({columns: masterColumns, rows: masterRows, config: {single: true}}, 'КнВыбратьМастера', null);
  picker.search.value = 'петр';
  picker.search.dispatch('input');
  key(picker.search, 'Enter');
  assert.deepEqual(picked(ctx), ['m-3']);
});

test('ОдинВыбор: стрелки ведут отметку по видимым строкам, Enter её выбирает', () => {
  const ctx = pickerContext();
  const picker = ctx.open({columns: masterColumns, rows: masterRows, config: {single: true}}, 'КнВыбратьМастера', null);
  key(picker.search, 'ArrowDown');
  key(picker.search, 'ArrowDown');
  key(picker.search, 'ArrowUp');
  key(picker.search, 'ArrowUp');
  assert.deepEqual(ctx.fired, [], 'стрелка отправила выбор');
  key(picker.search, 'Enter');
  assert.deepEqual(picked(ctx), ['m-4'], 'вверх с первой строки уходит на последнюю');
});

test('без Отборы панели отборов нет', () => {
  const ctx = pickerContext();
  ctx.open({columns: masterColumns, rows: masterRows, config: {single: true}}, 'КнВыбратьМастера', null);
  assert.equal(ctx.modal().querySelector('select._ip-flt'), null);
});

test('отборы: значения колонки, «Все» первым, выбор оставляет только совпадения', () => {
  const ctx = pickerContext();
  const picker = ctx.open({columns: masterColumns, rows: masterRows,
    config: {single: true, filters: ['Направление', 'Филиал']}}, 'КнВыбратьМастера', null);
  assert.deepEqual(optionValues(filterSelect(ctx, 'Направление')), ['', 'СМ', 'ТВ', 'ХД']);
  assert.deepEqual(optionValues(filterSelect(ctx, 'Филиал')), ['', 'МСК', 'СПБ']);

  choose(filterSelect(ctx, 'Направление'), 'СМ');
  assert.deepEqual(visibleIds(ctx), ['m-2', 'm-3'], 'отбор «СМ» показал Смирнова из ХД');
  assert.deepEqual(optionValues(filterSelect(ctx, 'Филиал')), ['', 'МСК', 'СПБ']);

  choose(filterSelect(ctx, 'Филиал'), 'СПБ');
  assert.deepEqual(visibleIds(ctx), ['m-3']);
  assert.deepEqual(optionValues(filterSelect(ctx, 'Направление')), ['', 'СМ', 'ТВ'], 'филиал не сузил направления');

  // Поиск работает поверх отборов.
  choose(filterSelect(ctx, 'Направление'), '');
  picker.search.value = 'сид';
  picker.search.dispatch('input');
  assert.deepEqual(visibleIds(ctx), ['m-4']);
  key(picker.search, 'Enter');
  assert.deepEqual(picked(ctx), ['m-4']);
});

test('отбор, которого не стало в списке, сбрасывается на «Все»', () => {
  const ctx = pickerContext();
  ctx.open({columns: masterColumns, rows: masterRows,
    config: {single: true, filters: ['Направление', 'Филиал']}}, 'КнВыбратьМастера', null);
  choose(filterSelect(ctx, 'Направление'), 'ХД');
  choose(filterSelect(ctx, 'Филиал'), '');
  assert.deepEqual(optionValues(filterSelect(ctx, 'Филиал')), ['', 'МСК']);
  choose(filterSelect(ctx, 'Направление'), 'ТВ');
  assert.deepEqual(visibleIds(ctx), ['m-4']);
  assert.equal(filterSelect(ctx, 'Филиал').value, '');
});

test('при серверном поиске отборы переживают новую выдачу, новое открытие их сбрасывает', () => {
  const ctx = pickerContext();
  const config = {single: true, serverSearch: true, filters: ['Филиал']};
  const picker = ctx.open({columns: masterColumns, rows: masterRows, config}, 'КнВыбратьМастера', null);
  choose(filterSelect(ctx, 'Филиал'), 'СПБ');
  picker.search.value = 'о';
  picker.search.dispatch('input');
  ctx.flush();
  ctx.respond({columns: masterColumns, rows: masterRows, config}, 'КнВыбратьМастера', null);
  assert.equal(filterSelect(ctx, 'Филиал').value, 'СПБ', 'отбор потерялся после ответа сервера');
  assert.deepEqual(visibleIds(ctx), ['m-3', 'm-4']);

  ctx.cancel();
  ctx.open({columns: masterColumns, rows: masterRows, config}, 'КнВыбратьМастера', null);
  assert.equal(filterSelect(ctx, 'Филиал').value, '', 'закрытое окно передало отбор следующему');
});

for (const phase of ['debounce', 'in-flight', 'pending', 'newer-debounce', 'failed']) {
  test('ОдинВыбор: Enter ждёт актуальную серверную выдачу — ' + phase, () => {
    const ctx = pickerContext();
    const config = {single: true, serverSearch: true};
    const payload = (row) => ({columns: masterColumns, rows: [row], config});
    const picker = ctx.open(payload(masterRows[0]), 'КнВыбратьМастера', null);
    key(picker.search, 'ArrowDown'); // Ранее отмеченный Смирнов тоже не должен уйти.
    picker.search.value = 'Петров';
    picker.search.dispatch('input');
    if (phase !== 'debounce') ctx.flush();
    if (phase === 'pending' || phase === 'newer-debounce') {
      picker.search.value = 'Сидоров';
      picker.search.dispatch('input');
      if (phase === 'pending') ctx.flush();
      // Приходит Петров, но поле поиска уже содержит Сидоров.
      ctx.respond(payload(masterRows[2]), 'КнВыбратьМастера', null);
    }
    if (phase === 'failed') {
      ctx.window.obPickerSearchFinished(ctx.fired[0].request);
      assert.equal(ctx.state().inFlight, null);
    }
    key(ctx.search(), 'Enter');
    assert.ok(ctx.modal(), 'Enter закрыл диалог до актуального ответа');
    assert.equal(ctx.fired.filter((f) => f.event === 'Выбор').length, 0, 'ушла устаревшая строка');

    // Точный актуальный ответ разрешает обычный Enter; после ошибки — повтор поиска.
    if (phase === 'failed') ctx.search().dispatch('input');
    ctx.flush();
    const row = phase === 'pending' || phase === 'newer-debounce' ? masterRows[3] : masterRows[2];
    ctx.respond(payload(row), 'КнВыбратьМастера', null);
    key(ctx.search(), 'Enter');
    assert.deepEqual(picked(ctx), [row.id]);
    assert.equal(ctx.modal(), null);
  });
}

// Имена и значения Object.prototype — обычные данные подбора, в том числе
// __proto__: computed key создаёт собственное поле, а не меняет прототип fixture.
const prototypeNames = ['constructor', 'toString', '__proto__'];
const prototypeValues = [...prototypeNames, 'Обычное'];
const sortedPrototypeValues = [...prototypeValues].sort((a, b) => a.localeCompare(b, 'ru'));

for (const serverSearch of [false, true]) {
  for (const name of prototypeNames) {
    test(`отбор ${name}: все значения доступны, серверный поиск=${serverSearch}`, () => {
      const ctx = pickerContext();
      const columns = [{name, type: 'string'}];
      const rows = [...prototypeValues, 'constructor'].map((value, i) => ({
        id: 'p-' + i, data: {[name]: value},
      }));
      ctx.open({columns, rows, config: {filters: [name], serverSearch}}, 'Подбор');
      assert.deepEqual(visibleIds(ctx), rows.map((r) => r.id), 'отбор включился сам при открытии');
      assert.deepEqual(optionValues(filterSelect(ctx, name)), ['', ...sortedPrototypeValues],
        'значение потерялось или повторилось в отборе');
      for (const value of prototypeValues) {
        choose(filterSelect(ctx, name), value);
        assert.equal(filterSelect(ctx, name).value, value);
        assert.deepEqual(visibleIds(ctx), rows.filter((r) => r.data[name] === value).map((r) => r.id));
      }
      choose(filterSelect(ctx, name), '');
      assert.deepEqual(visibleIds(ctx), rows.map((r) => r.id), '«Все» не сбросил отбор');
    });
  }
}

const prototypeColumns = prototypeNames.map((name) => ({name, type: 'string'}));
const prototypeRows = [
  ['constructor', 'toString', '__proto__'],
  ['constructor', 'Обычное', 'toString'],
  ['toString', 'toString', 'Обычное'],
  ['__proto__', 'constructor', '__proto__'],
].map((values, i) => ({
  id: 'linked-' + i,
  data: Object.fromEntries(prototypeNames.map((name, j) => [name, values[j]])),
}));

for (const serverSearch of [false, true]) {
  test(`связанные отборы по именам Object.prototype, серверный поиск=${serverSearch}`, () => {
    const ctx = pickerContext();
    const config = {filters: prototypeNames, serverSearch};
    ctx.open({columns: prototypeColumns, rows: prototypeRows, config}, 'Подбор');
    assert.deepEqual(visibleIds(ctx), prototypeRows.map((r) => r.id));
    choose(filterSelect(ctx, 'constructor'), 'constructor');
    assert.deepEqual(visibleIds(ctx), ['linked-0', 'linked-1']);
    assert.deepEqual(optionValues(filterSelect(ctx, 'toString')), ['', 'Обычное', 'toString']);
    choose(filterSelect(ctx, 'toString'), 'toString');
    choose(filterSelect(ctx, '__proto__'), '__proto__');
    assert.deepEqual(visibleIds(ctx), ['linked-0']);
    assert.deepEqual(optionValues(filterSelect(ctx, 'constructor')), ['', 'constructor']);

    if (serverSearch) {
      function respond(rows, query) {
        ctx.search().value = query;
        ctx.search().dispatch('input');
        ctx.flush();
        ctx.respond({columns: prototypeColumns, rows, config}, 'Подбор');
      }
      const newRow = {...prototypeRows[0], id: 'new-result'};
      respond([newRow, prototypeRows[1]], 'новая выдача');
      prototypeNames.forEach((name) => assert.equal(filterSelect(ctx, name).value, name,
        'выбранный отбор потерян после пересборки'));
      assert.deepEqual(visibleIds(ctx), ['new-result']);

      // Ни одна строка новой выдачи не проходит прежние связанные отборы:
      // все три сбрасываются, и варианты собираются заново после сброса соседей.
      respond([prototypeRows[2]], 'исчезнувшие значения');
      prototypeNames.forEach((name) => {
        assert.equal(filterSelect(ctx, name).value, '');
        assert.deepEqual(optionValues(filterSelect(ctx, name)), ['', prototypeRows[2].data[name]]);
      });
      assert.deepEqual(visibleIds(ctx), ['linked-2']);
      respond(prototypeRows, 'восстановленная выдача');
      assert.deepEqual(visibleIds(ctx), prototypeRows.map((r) => r.id));
      choose(filterSelect(ctx, '__proto__'), '__proto__');
      assert.deepEqual(visibleIds(ctx), ['linked-0', 'linked-3']);
    }
  });
}

for (const close of ['cancel', 'escape', 'transfer', 'other-button']) {
  test(`отбор __proto__ не наследуется после ${close}`, () => {
    const ctx = pickerContext();
    const config = {serverSearch: true, filters: prototypeNames};
    const payload = {columns: prototypeColumns, rows: prototypeRows, config};
    ctx.open(payload, 'Подбор');
    choose(filterSelect(ctx, '__proto__'), '__proto__');
    assert.deepEqual(visibleIds(ctx), ['linked-0', 'linked-3']);
    if (close !== 'other-button') ctx[close]();
    ctx.open(payload, close === 'other-button' ? 'ДругойПодбор' : 'Подбор');
    prototypeNames.forEach((name) => assert.equal(filterSelect(ctx, name).value, ''));
    assert.deepEqual(visibleIds(ctx), prototypeRows.map((r) => r.id));
    choose(filterSelect(ctx, '__proto__'), 'toString');
    assert.deepEqual(visibleIds(ctx), ['linked-1'], 'повторное открытие вернуло коллизии ключей');
  });
}

for (const serverSearch of [false, true]) {
  for (const editable of [false, true]) {
    test(`перенос сохраняет колонки Object.prototype, серверный поиск=${serverSearch}, редактирование=${editable}`, () => {
      const ctx = pickerContext();
      const names = ['__proto__', 'constructor', 'toString', 'Номер'];
      const values = Object.fromEntries(names.map((name) => [name, 'value-' + name]));
      ctx.open({
        columns: names.map((name) => ({name, type: 'string', editable})),
        rows: [{id: 'prototype-row', data: values}],
        config: {serverSearch},
      }, 'Подбор');
      const tr = ctx.rowsOf()[0];
      const cb = tr.querySelector('._ip-cb');
      if (editable) {
        names.forEach((name) => {
          const input = tr.querySelector('._ip-val[data-col="' + name + '"]');
          input.value = 'edited-' + name;
          input.dispatch('input', {target: input});
        });
      }
      cb.checked = true;
      cb.onchange();
      ctx.transfer();
      const choices = ctx.fired.filter((event) => event.event === 'Выбор');
      assert.equal(choices.length, 1, 'перенос должен отправить одно событие Выбор');
      const result = JSON.parse(choices[0].params._pick_result);
      assert.equal(result.length, 1);
      assert.equal(result[0].id, 'prototype-row');
      names.forEach((name) => {
        assert.ok(Object.prototype.hasOwnProperty.call(result[0], name),
          'результат потерял собственную колонку ' + name);
        assert.equal(result[0][name], (editable ? 'edited-' : 'value-') + name);
      });
    });
  }
}

test('Конфиг.Ширина задаёт ширину окна, без неё — прежние 720px', () => {
  const ctx = pickerContext();
  ctx.open({columns, rows, config: {title: 'Подбор', width: 1440}}, 'КнопкаНайти', null);
  const wide = ctx.modal().children[0].style.cssText;
  assert.match(wide, /(^|;)width:1440px;/, wide);
  assert.match(wide, /max-width:96vw/, 'на узком экране окно обязано ужиматься');

  ctx.open({columns, rows, config: {title: 'Подбор'}}, 'КнопкаНайти', null);
  assert.match(ctx.modal().children[0].style.cssText, /(^|;)width:720px;/);
});

// Конфиг.ПоляШапки — поля отбора в шапке окна, как «Список номенклатуры» 1С:
// что искать, решает сервер (событие Поиск с _pick_fields).
const headerConfig = (brand) => ({
  title: 'Список номенклатуры',
  headerFields: [
    {name: 'Бренд', title: 'Бренд', type: 'list', value: brand,
      options: [{value: 'b-1', title: 'ARISTON'}, {value: 'b-2', title: 'BOSCH'}]},
    {name: 'Артикул', title: 'Артикул', type: 'string', value: ''},
    {name: 'СкрыватьНулевые', title: 'Скрывать нулевые', type: 'bool', value: false},
  ],
});
const headerBox = (ctx) => ctx.modal().children[0];
const headerCtl = (ctx, name) => headerBox(ctx).querySelector(`._ip-hf[data-col="${name}"]`);

test('поле шапки: смена списка и Enter в строке отправляют Поиск с _pick_fields', () => {
  const ctx = pickerContext();
  ctx.open({columns, rows, config: headerConfig('b-1')}, 'КнПодбор', {_tp: 'Товары'});
  assert.equal(headerCtl(ctx, 'Бренд').value, 'b-1', 'значение с сервера не подставлено');

  const brand = headerCtl(ctx, 'Бренд');
  brand.value = 'b-2';
  brand.dispatch('change');
  assert.equal(ctx.fired.length, 1);
  assert.equal(ctx.fired[0].element, 'КнПодбор');
  assert.equal(ctx.fired[0].event, 'Поиск');
  assert.equal(ctx.fired[0].params._tp, 'Товары', 'контекст события потерян');
  assert.deepEqual(JSON.parse(ctx.fired[0].params._pick_fields),
    {Бренд: 'b-2', Артикул: '', СкрыватьНулевые: false});

  // Ответ сервера пересобирает окно; второй запрос — после него.
  ctx.respond({columns, rows, config: headerConfig('b-2')}, 'КнПодбор', {_tp: 'Товары'});
  const art = headerCtl(ctx, 'Артикул');
  art.value = 'C005';
  art.dispatch('input');
  art.dispatch('keydown', {key: 'Enter', preventDefault() {}});
  assert.equal(ctx.fired.length, 2);
  assert.equal(JSON.parse(ctx.fired[1].params._pick_fields).Артикул, 'C005');
});

test('поле шапки: набранное переживает пересборку, чужое значение списка — с сервера', () => {
  const ctx = pickerContext();
  ctx.open({columns, rows, config: headerConfig('b-1')}, 'КнПодбор', null);
  const art = headerCtl(ctx, 'Артикул');
  art.value = 'C005';
  art.dispatch('input');
  headerBox(ctx).querySelector('._ip-refresh').dispatch('click');
  assert.equal(ctx.fired.length, 1, '«Обновить» не отправил Поиск');

  // Сервер сменил варианты бренда: выбранного b-1 среди них больше нет.
  const cfg = headerConfig('b-3');
  cfg.headerFields[0].options = [{value: 'b-3', title: 'GORENJE'}];
  ctx.respond({columns, rows, config: cfg}, 'КнПодбор', null);
  assert.equal(headerCtl(ctx, 'Артикул').value, 'C005', 'набранный артикул пропал');
  assert.equal(headerCtl(ctx, 'Бренд').value, 'b-3');
});

test('поля шапки уходят и с выбором', () => {
  const ctx = pickerContext();
  ctx.open({columns, rows, config: Object.assign(headerConfig('b-1'), {single: true})}, 'КнПодбор', null);
  headerBox(ctx).querySelector('tbody').rows[0].dispatch('dblclick');
  const pick = ctx.fired.find((f) => f.event === 'Выбор');
  assert.ok(pick, 'Выбор не отправлен');
  assert.equal(JSON.parse(pick.params._pick_fields).Бренд, 'b-1');
});

test('БезВыбора: ни отметок, ни «Выбрать» — одна кнопка «Закрыть»', () => {
  const ctx = pickerContext();
  ctx.open({columns, rows, config: {title: 'Список номенклатуры', single: true, noSelect: true}}, 'КнПодбор', null);
  const box = headerBox(ctx);
  const foot = box.children[box.children.length - 1];
  assert.equal(foot.children.length, 1);
  assert.equal(foot.children[0].textContent, 'Закрыть');
  assert.equal(box.querySelector('._ip-cb'), null, 'у строк остались отметки');
  ctx.rowsOf()[0].dispatch('dblclick');
  assert.equal(ctx.fired.length, 0, 'двойной щелчок что-то выбрал');
});

const headerPickerData = (brand, config = {}) => ({
  columns: [{name: 'Бренд', title: 'Бренд', type: 'string'}],
  rows: [{id: brand + '-row', data: {Бренд: brand}}],
  config: Object.assign(headerConfig(brand), {single: true}, config),
});

async function openManagedHeader(ctx) {
  const opening = ctx.fire('КнПодбор', 'Нажатие');
  await ctx.tick();
  await ctx.settle(0, {pickerData: headerPickerData('b-1')});
  await opening;
}

function chooseHeaderRow(ctx, action) {
  const row = headerBox(ctx).querySelector('tbody').rows[0];
  if (action === 'double-click') row.dispatch('dblclick');
  else if (action === 'button') {
    row.dispatch('click');
    ctx.transfer();
  } else ctx.search().dispatch('keydown', {key: 'Enter', preventDefault() {}});
}

for (const action of ['Enter', 'double-click', 'button']) {
  for (const failure of ['fetch', 'json', 'server']) {
    test(`поля шапки: ${action} ждёт успешного обновления после ошибки ${failure}`, async () => {
      const ctx = pickerContext(true);
      await openManagedHeader(ctx);
      const brand = headerCtl(ctx, 'Бренд');
      brand.value = 'b-2';
      brand.dispatch('change');
      await ctx.tick();
      assert.equal(ctx.requests[1].options.body.get('_event'), 'Поиск');
      assert.equal(JSON.parse(ctx.requests[1].options.body.get('_pick_fields')).Бренд, 'b-2');
      chooseHeaderRow(ctx, action);
      await ctx.tick();
      assert.ok(ctx.modal(), 'выбор закрыл окно во время обновления');
      assert.equal(ctx.requests.length, 2, 'старая строка ушла, пока Поиск в пути');
      const error = new Error('refresh failed');
      if (failure === 'fetch') ctx.requests[1].reject(error);
      else if (failure === 'json') ctx.requests[1].resolve({ok: true, json: async () => { throw error; }});
      else ctx.requests[1].resolve({ok: true, json: async () => ({ok: false, error: error.message})});
      await ctx.tick();
      assert.ok(ctx.messages.some((m) => m.includes('refresh failed')));
      chooseHeaderRow(ctx, action);
      await ctx.tick();
      assert.ok(ctx.modal(), 'выбор закрыл окно после ошибки обновления');
      assert.equal(ctx.requests.length, 2, 'ошибка разрешила выбор прежней строки с новым брендом');
      headerBox(ctx).querySelector('._ip-refresh').dispatch('click');
      await ctx.tick();
      await ctx.settle(2, {pickerData: headerPickerData('b-2')});
      chooseHeaderRow(ctx, action);
      await ctx.tick();
      assert.equal(ctx.modal(), null, 'успешное обновление не разрешило выбор');
      assert.equal(ctx.requests.length, 4);
      assert.equal(ctx.requests[3].options.body.get('_event'), 'Выбор');
      assert.equal(JSON.parse(ctx.requests[3].options.body.get('_pick_fields')).Бренд, 'b-2');
      assert.deepEqual(JSON.parse(ctx.requests[3].options.body.get('_pick_result')), [{id: 'b-2-row', Бренд: 'b-2'}]);
      await ctx.settle(3, {});
    });
  }
}

test('поля шапки: набранное без запроса не разрешает выбор; ответ старого запроса тоже', async () => {
  const ctx = pickerContext(true);
  await openManagedHeader(ctx);
  const art = headerCtl(ctx, 'Артикул');
  art.value = 'first';
  art.dispatch('input');
  chooseHeaderRow(ctx, 'double-click');
  await ctx.tick();
  assert.ok(ctx.modal());
  assert.equal(ctx.requests.length, 1, 'набранный артикул ушёл с прежней выдачей');
  headerBox(ctx).querySelector('._ip-refresh').dispatch('click');
  await ctx.tick();
  art.value = 'second';
  art.dispatch('input'); // Ответ для first не подтверждает новое значение second.
  await ctx.settle(1, {pickerData: headerPickerData('b-1')});
  assert.equal(headerCtl(ctx, 'Артикул').value, 'second', 'набранное пропало');
  chooseHeaderRow(ctx, 'button');
  await ctx.tick();
  assert.ok(ctx.modal());
  assert.equal(ctx.requests.length, 2, 'старый успешный ответ подтвердил новое значение');
  headerBox(ctx).querySelector('._ip-refresh').dispatch('click');
  await ctx.tick();
  await ctx.settle(2, {pickerData: headerPickerData('b-1')});
  chooseHeaderRow(ctx, 'Enter');
  await ctx.tick();
  assert.equal(JSON.parse(ctx.requests[3].options.body.get('_pick_fields')).Артикул, 'second');
  await ctx.settle(3, {});
});

test('поля шапки: очередь и пустой ответ не разрешают выбор до последнего успешного ответа', async () => {
  const ctx = pickerContext(true);
  await openManagedHeader(ctx);
  headerCtl(ctx, 'Бренд').value = 'b-2';
  headerCtl(ctx, 'Бренд').dispatch('change');
  await ctx.tick();
  const hideZero = headerBox(ctx).querySelector('._ip-head-fields').querySelectorAll('input')
    .find((ctl) => ctl.type === 'checkbox');
  hideZero.checked = true;
  hideZero.dispatch('change');
  assert.equal(ctx.requests.length, 2, 'обновления ушли параллельно');
  await ctx.settle(1, {pickerData: headerPickerData('b-2')});
  assert.equal(ctx.requests.length, 3, 'очередь не отправила новое значение флажка');
  assert.equal(JSON.parse(ctx.requests[2].options.body.get('_pick_fields')).СкрыватьНулевые, true);
  chooseHeaderRow(ctx, 'double-click');
  await ctx.tick();
  assert.ok(ctx.modal());
  assert.equal(ctx.requests.length, 3);
  await ctx.settle(2, {}); // Обработчик не вернул ПоказатьПодбор.
  ctx.transfer();
  await ctx.tick();
  assert.ok(ctx.modal(), 'пустой ответ разрешил перенести прежнюю отметку');
  assert.equal(ctx.requests.length, 3);
  headerBox(ctx).querySelector('._ip-refresh').dispatch('click');
  await ctx.tick();
  await ctx.settle(3, {pickerData: headerPickerData('b-2')});
  chooseHeaderRow(ctx, 'button');
  await ctx.tick();
  assert.equal(ctx.requests[4].options.body.get('_event'), 'Выбор');
  assert.equal(JSON.parse(ctx.requests[4].options.body.get('_pick_fields')).СкрыватьНулевые, true);
  await ctx.settle(4, {});
});

test('поля шапки: серверная замена недоступного варианта списка разрешает выбор', async () => {
  const ctx = pickerContext(true);
  await openManagedHeader(ctx);
  headerBox(ctx).querySelector('._ip-refresh').dispatch('click');
  await ctx.tick();
  const data = headerPickerData('b-3');
  data.config.headerFields[0].options = [{value: 'b-3', title: 'GORENJE'}];
  await ctx.settle(1, {pickerData: data});
  assert.equal(headerCtl(ctx, 'Бренд').value, 'b-3');
  chooseHeaderRow(ctx, 'Enter');
  await ctx.tick();
  assert.equal(ctx.modal(), null);
  assert.equal(JSON.parse(ctx.requests[2].options.body.get('_pick_fields')).Бренд, 'b-3');
  assert.deepEqual(JSON.parse(ctx.requests[2].options.body.get('_pick_result')), [{id: 'b-3-row', Бренд: 'b-3'}]);
  await ctx.settle(2, {});
});

for (const action of ['Enter', 'double-click', 'button']) {
  for (const serverSearch of [false, true]) {
    for (const removal of ['one', 'last']) {
      test(`поля шапки: ${action} после удаления ${removal}, серверный поиск ${serverSearch}`, async () => {
        const ctx = pickerContext(true);
        const initial = headerPickerData('b-1', {serverSearch});
        if (removal === 'last') initial.config.headerFields = [initial.config.headerFields[1]];
        const opening = ctx.fire('КнПодбор', 'Нажатие');
        await ctx.tick();
        await ctx.settle(0, {pickerData: initial});
        await opening;
        headerCtl(ctx, 'Артикул').value = 'obsolete';
        headerCtl(ctx, 'Артикул').dispatch('input');
        if (removal === 'one') {
          headerCtl(ctx, 'Бренд').value = 'b-2';
          headerCtl(ctx, 'Бренд').dispatch('change');
        } else headerBox(ctx).querySelector('._ip-refresh').dispatch('click');
        await ctx.tick();
        assert.equal(JSON.parse(ctx.requests[1].options.body.get('_pick_fields')).Артикул, 'obsolete');

        const data = headerPickerData('b-2', {serverSearch});
        data.config.headerFields = removal === 'one'
          ? data.config.headerFields.filter((f) => f.name !== 'Артикул') : [];
        await ctx.settle(1, {pickerData: data});
        assert.equal(headerCtl(ctx, 'Артикул'), null);
        assert.equal(Object.getPrototypeOf(ctx.state().fields), null);
        assert.equal(Object.hasOwn(ctx.state().fields, 'Артикул'), false, 'удалённый реквизит остался в состоянии');
        if (removal === 'one') {
          assert.equal(headerCtl(ctx, 'Бренд').value, 'b-2', 'значение оставшегося поля потеряно');
          headerBox(ctx).querySelector('._ip-refresh').dispatch('click');
        } else if (serverSearch) {
          assert.deepEqual(Object.keys(ctx.state().fields), []);
          ctx.search().value = 'b-2';
          ctx.search().dispatch('input');
          ctx.flush();
        }
        await ctx.tick();
        let pickIndex = 2;
        if (removal === 'one' || serverSearch) {
          assert.equal(ctx.requests[2].options.body.get('_event'), 'Поиск');
          const fields = ctx.requests[2].options.body.get('_pick_fields');
          if (removal === 'one') assert.deepEqual(JSON.parse(fields), {Бренд: 'b-2', СкрыватьНулевые: false});
          else assert.equal(fields, null, 'пустая шапка отправила прежние значения');
          await ctx.settle(2, {pickerData: data});
          pickIndex = 3;
        }
        chooseHeaderRow(ctx, action);
        await ctx.tick();
        assert.equal(ctx.modal(), null, 'удаление поля заблокировало выбор');
        const pick = ctx.requests[pickIndex].options.body;
        assert.equal(pick.get('_event'), 'Выбор');
        assert.deepEqual(JSON.parse(pick.get('_pick_result')), [{id: 'b-2-row', Бренд: 'b-2'}]);
        if (removal === 'one') assert.deepEqual(JSON.parse(pick.get('_pick_fields')), {Бренд: 'b-2', СкрыватьНулевые: false});
        else assert.equal(pick.get('_pick_fields'), null, 'выбор вернул удалённое последнее поле');
        await ctx.settle(pickIndex, {});
      });
    }
  }

  test(`поля шапки: удаление другого поля не подтверждает поздний ввод — ${action}`, async () => {
    const ctx = pickerContext(true);
    await openManagedHeader(ctx);
    headerCtl(ctx, 'Артикул').value = 'sent';
    headerCtl(ctx, 'Артикул').dispatch('input');
    headerBox(ctx).querySelector('._ip-refresh').dispatch('click');
    await ctx.tick();
    headerCtl(ctx, 'Артикул').value = 'late';
    headerCtl(ctx, 'Артикул').dispatch('input');
    const data = headerPickerData('b-1');
    data.config.headerFields = data.config.headerFields.filter((f) => f.name !== 'СкрыватьНулевые');
    await ctx.settle(1, {pickerData: data});
    assert.equal(headerCtl(ctx, 'Артикул').value, 'late', 'поздний ввод потерян');
    chooseHeaderRow(ctx, action);
    await ctx.tick();
    assert.ok(ctx.modal(), 'удаление другого поля подтвердило поздний ввод');
    assert.equal(ctx.requests.length, 2);
    headerBox(ctx).querySelector('._ip-refresh').dispatch('click');
    await ctx.tick();
    assert.deepEqual(JSON.parse(ctx.requests[2].options.body.get('_pick_fields')), {Бренд: 'b-1', Артикул: 'late'});
    await ctx.settle(2, {pickerData: data});
    chooseHeaderRow(ctx, action);
    await ctx.tick();
    assert.equal(ctx.modal(), null);
    assert.deepEqual(JSON.parse(ctx.requests[3].options.body.get('_pick_fields')), {Бренд: 'b-1', Артикул: 'late'});
    await ctx.settle(3, {});
  });

  test(`поля шапки: пустая шапка сохраняет запрет выбора до ответа очереди — ${action}`, async () => {
    const ctx = pickerContext(true);
    await openManagedHeader(ctx);
    headerCtl(ctx, 'Бренд').value = 'b-2';
    headerCtl(ctx, 'Бренд').dispatch('change');
    await ctx.tick();
    const hideZero = headerBox(ctx).querySelector('._ip-head-fields').querySelectorAll('input')
      .find((ctl) => ctl.type === 'checkbox');
    hideZero.checked = true;
    hideZero.dispatch('change');
    const data = headerPickerData('b-2', {headerFields: []});
    await ctx.settle(1, {pickerData: data});
    assert.equal(ctx.requests.length, 3, 'последний запрос очереди потерян');
    assert.equal(ctx.requests[2].options.body.get('_pick_fields'), null, 'очередь отправила удалённые поля');
    chooseHeaderRow(ctx, action);
    await ctx.tick();
    assert.ok(ctx.modal(), 'пустая шапка разрешила выбрать строку до завершения очереди');
    assert.equal(ctx.requests.length, 3);
    await ctx.settle(2, {pickerData: data});
    chooseHeaderRow(ctx, action);
    await ctx.tick();
    assert.equal(ctx.modal(), null);
    assert.equal(ctx.requests[3].options.body.get('_event'), 'Выбор');
    assert.equal(ctx.requests[3].options.body.get('_pick_fields'), null);
    await ctx.settle(3, {});
  });
}
