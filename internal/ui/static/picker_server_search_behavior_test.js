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
const fireSource = managedSource.slice(managedSource.indexOf('  window.obFire = async function'), managedSource.indexOf('\n  // Отслеживание «грязной» формы'));
assert.ok(pickerSource && fireSource, 'не найдены границы runtime');

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
    body,
  };
  const window = {obManagedApplyTablePartRefOptions() {}, applyTableParts() {}};
  window.obFire = function (element, event, params, request) { fired.push({element, event, params, request}); };
  const api = new Function(
    'document', 'window', 'obFire', 'setTimeout', 'clearTimeout',
    pickerSource + '\n' +
      'return {openItemPicker: openItemPicker, searchEmpty: window.obPickerSearchEmpty,' +
      ' state: function () { return obPickerSearch; }};',
  )(
    document,
    window,
    (...args) => window.obFire(...args),
    function (fn) { timers.push(fn); return timers.length; },
    function () { timers.length = 0; },
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
    };
    new Function(...Object.keys(deps), fireSource)(...Object.values(deps));
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
      requests[index].resolve({json: async () => data});
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
    else if (failure === 'server') ctx.requests[1].resolve({json: async () => ({error: error.message})});
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
