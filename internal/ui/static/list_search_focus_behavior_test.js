// Фокус и каретка строки поиска после отложенного автосабмита — поведение
// боевого кода из ui.js.
//
// Разметочный тест доказал бы только наличие атрибута `data-ob-auto-submit`, а
// он был на месте и в сломанном виде: страница перезагружалась, поле теряло
// фокус, и следующие буквы уходили в горячие клавиши списка. Поэтому здесь
// исполняется тот же кусок ui.js в поддельном DOM: сначала пользовательский
// путь «ввод → debounce → отправка», затем загрузка новой страницы.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const uiSource = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');

function slice(from, to) {
  const start = uiSource.indexOf(from);
  const end = uiSource.indexOf(to, start);
  if (start < 0 || end < 0) {
    throw new Error(`slice not found in ui.js: ${from}`);
  }
  return uiSource.slice(start, end);
}

const restoreSource = slice('// BEGIN onebase-list-search-restore', '// END onebase-list-search-restore');
const delegatesSource = slice('function obInitListDelegates() {', '\nfunction listSubmit');
const interactiveSource = slice('function obIsInteractiveTarget', '\nfunction obHasBlockingModal');

function storage() {
  const data = new Map();
  const log = [];
  return {
    log,
    getItem(key) { return data.has(key) ? data.get(key) : null; },
    setItem(key, value) { log.push('set'); data.set(String(key), String(value)); },
    removeItem(key) { log.push('remove'); data.delete(String(key)); },
  };
}

function input(attrs = {}, props = {}) {
  const attributes = new Map(Object.entries(attrs).map(([k, v]) => [k, String(v)]));
  const el = {
    nodeType: 1,
    tagName: 'INPUT',
    id: props.id || '',
    value: props.value === undefined ? '' : props.value,
    selectionStart: props.selectionStart,
    selectionEnd: props.selectionEnd,
    focused: false,
    range: null,
    getAttribute(name) { return attributes.has(name) ? attributes.get(name) : null; },
    focus() { el.focused = true; },
    setSelectionRange(start, end) { el.range = [start, end]; },
    closest(selector) {
      if (selector.includes('[data-ob-auto-submit]') && attributes.has('data-ob-auto-submit')) return el;
      if (selector.includes('input')) return el;
      return null;
    },
  };
  el.form = props.form === undefined ? { name: 'list-search-form' } : props.form;
  return el;
}

// Одна страница приложения: свой DOM, своя история вызовов, общее на вкладку
// хранилище передаётся снаружи — ровно так живёт sessionStorage в браузере.
// frameName — имя родительского iframe («ob-tab-<id>» из tabs.go): страница
// живёт во вкладке оболочки; без него — страница, открытая вне оболочки.
// hidden — вкладка неактивна и спрятана display:none.
function page(opts) {
  const listeners = new Map();
  const timers = new Map();
  const calls = [];
  let nextTimer = 1;
  const doc = {
    readyState: 'complete',
    hidden: opts.hidden === true,
    addEventListener(type, fn) { listeners.set(type, fn); },
    getElementById(id) { return opts.search && opts.search.id === id ? opts.search : null; },
  };
  const sandbox = {
    document: doc,
    location: { pathname: opts.pathname },
    setTimeout(fn) { const id = nextTimer++; timers.set(id, fn); return id; },
    clearTimeout(id) { timers.delete(id); },
  };
  sandbox.window = {
    document: doc,
    frameElement: opts.frameName ? { name: opts.frameName } : null,
    HTMLFormElement: { prototype: { submit() { calls.push('submit'); } } },
  };
  Object.defineProperty(sandbox.window, 'sessionStorage', {
    get() {
      if (opts.storageThrows) throw new Error('доступ к данным запрещён');
      return opts.storage;
    },
  });
  vm.createContext(sandbox);
  vm.runInContext(restoreSource, sandbox, { filename: 'ui-list-search-restore.js' });
  vm.runInContext(delegatesSource, sandbox, { filename: 'ui-list-delegates.js' });
  vm.runInContext(interactiveSource, sandbox, { filename: 'ui-interactive-target.js' });
  sandbox.obInitListDelegates();
  return {
    sandbox,
    calls,
    type(target) {
      listeners.get('input')({ target });
      for (const [id, fn] of [...timers]) { timers.delete(id); fn(); }
    },
    restore() { return sandbox.obRestoreListSearchFocus(); },
  };
}

test('каретка запоминается ровно перед отправкой формы', () => {
  const store = storage();
  const search = input({ 'data-ob-auto-submit': '320', 'data-ob-list-search': '' },
    { id: 'ob-list-search', value: 'Иванов', selectionStart: 6, selectionEnd: 6 });
  const app = page({ pathname: '/ui/catalog/контрагенты', storage: store, search });

  app.type(search);

  assert.deepEqual(app.calls, ['submit'], 'форма не отправилась');
  // Порядок важен: после submit страница уже уничтожается, сохранять поздно.
  assert.deepEqual(store.log, ['set'], 'каретка сохранена не одной записью до отправки');
  assert.deepEqual(JSON.parse(store.getItem('ob-list-search-focus')),
    { path: '/ui/catalog/контрагенты', start: 6, end: 6 });
});

test('после перезагрузки той же страницы фокус и каретка возвращаются в поиск', () => {
  const store = storage();
  const before = input({ 'data-ob-auto-submit': '320' },
    { id: 'ob-list-search', value: 'Ива', selectionStart: 3, selectionEnd: 3 });
  page({ pathname: '/ui/catalog/контрагенты', storage: store, search: before }).type(before);

  // Новая страница: значение пришло с сервера, фокуса нет.
  const after = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: 'Ива' });
  const reloaded = page({ pathname: '/ui/catalog/контрагенты', storage: store, search: after });

  assert.equal(reloaded.restore(), after, 'поиск не найден на новой странице');
  assert.equal(after.focused, true, 'фокус не вернулся в строку поиска');
  assert.deepEqual(after.range, [3, 3], 'каретка не восстановлена');
});

test('отметка одноразовая: повторный показ страницы фокус уже не забирает', () => {
  const store = storage();
  const before = input({ 'data-ob-auto-submit': '320' },
    { id: 'ob-list-search', value: 'Ива', selectionStart: 3, selectionEnd: 3 });
  page({ pathname: '/ui/catalog/контрагенты', storage: store, search: before }).type(before);

  const after = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: 'Ива' });
  const first = page({ pathname: '/ui/catalog/контрагенты', storage: store, search: after });
  first.restore();
  after.focused = false;
  after.range = null;

  const again = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: 'Ива' });
  const second = page({ pathname: '/ui/catalog/контрагенты', storage: store, search: again });
  assert.equal(second.restore(), null, 'отметка пережила одну загрузку');
  assert.equal(again.focused, false, 'фокус забран повторно');
});

test('другая страница чужой фокус не забирает', () => {
  const store = storage();
  const before = input({ 'data-ob-auto-submit': '320' },
    { id: 'ob-list-search', value: 'Ива', selectionStart: 3, selectionEnd: 3 });
  page({ pathname: '/ui/catalog/контрагенты', storage: store, search: before }).type(before);

  const other = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: '' });
  const elsewhere = page({ pathname: '/ui/document/расходнаянакладная', storage: store, search: other });
  assert.equal(elsewhere.restore(), null, 'фокус восстановлен на чужой странице');
  assert.equal(other.focused, false, 'поиск другой страницы получил фокус');
});

test('обычный переход по ссылке фокус в поиск не переводит', () => {
  const store = storage();
  const search = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: '' });
  const fresh = page({ pathname: '/ui/catalog/контрагенты', storage: store, search });
  assert.equal(fresh.restore(), null, 'фокус восстановлен без сохранённой отметки');
  assert.equal(search.focused, false, 'поиск забрал фокус на обычной загрузке');
});

test('пока фокус в поиске, списковые горячие клавиши молчат', () => {
  const store = storage();
  const before = input({ 'data-ob-auto-submit': '320' },
    { id: 'ob-list-search', value: 'Ива', selectionStart: 3, selectionEnd: 3 });
  page({ pathname: '/ui/catalog/контрагенты', storage: store, search: before }).type(before);

  const after = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: 'Ива' });
  const reloaded = page({ pathname: '/ui/catalog/контрагенты', storage: store, search: after });
  const restored = reloaded.restore();

  // Тем же предикатом обработчик keydown отсекает Insert и клавиши списка.
  assert.equal(reloaded.sandbox.obIsInteractiveTarget(restored), true,
    'продолжение ввода снова уйдёт в горячие клавиши списка');
});

test('каретка не выходит за пределы значения, пришедшего с сервера', () => {
  const store = storage();
  const before = input({ 'data-ob-auto-submit': '320' },
    { id: 'ob-list-search', value: 'Иванов', selectionStart: 6, selectionEnd: 6 });
  page({ pathname: '/ui/catalog/контрагенты', storage: store, search: before }).type(before);

  const after = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: 'Ив' });
  const reloaded = page({ pathname: '/ui/catalog/контрагенты', storage: store, search: after });
  reloaded.restore();
  assert.deepEqual(after.range, [2, 2], 'каретка встала за концом строки');
});

test('поле автосабмита вне списка каретку не запоминает', () => {
  const store = storage();
  const other = input({ 'data-ob-auto-submit': '320' },
    { id: 'ob-filter-search', value: 'Ива', selectionStart: 3, selectionEnd: 3 });
  const app = page({ pathname: '/ui/catalog/контрагенты', storage: store, search: other });
  app.type(other);
  assert.deepEqual(app.calls, ['submit'], 'форма не отправилась');
  assert.deepEqual(store.log, [], 'сохранена каретка чужого поля');
});

test('запрещённое хранилище не ломает ни отправку, ни загрузку', () => {
  const search = input({ 'data-ob-auto-submit': '320' },
    { id: 'ob-list-search', value: 'Ива', selectionStart: 3, selectionEnd: 3 });
  const app = page({ pathname: '/ui/catalog/контрагенты', storageThrows: true, search });
  app.type(search);
  assert.deepEqual(app.calls, ['submit'], 'поиск перестал работать без sessionStorage');
  assert.equal(app.restore(), null, 'восстановление не пережило отказ хранилища');
});

// Вкладки оболочки — same-origin iframe с общим sessionStorage: обе печатают
// «Ива» перед перезагрузкой, и восстановление обязано сработать в ЛЮБОМ
// порядке, включая два экземпляра одного URL (#1599).
test('вкладки оболочки не глотают отметку друг друга даже на одном адресе', () => {
  const store = storage();
  const typeIn = (frameName) => {
    const field = input({ 'data-ob-auto-submit': '320' },
      { id: 'ob-list-search', value: 'Ива', selectionStart: 3, selectionEnd: 3 });
    page({ pathname: '/ui/catalog/контрагенты', storage: store, search: field, frameName }).type(field);
  };
  const reloaded = (frameName) => {
    const field = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: 'Ива' });
    return { field, app: page({ pathname: '/ui/catalog/контрагенты', storage: store, search: field, frameName }) };
  };

  typeIn('ob-tab-a');
  typeIn('ob-tab-b');

  const b = reloaded('ob-tab-b');
  assert.equal(b.app.restore(), b.field, 'вкладка B не нашла свою отметку');
  assert.equal(b.field.focused, true);
  assert.deepEqual(b.field.range, [3, 3]);

  const a = reloaded('ob-tab-a');
  assert.equal(a.app.restore(), a.field, 'вкладка A потеряла отметку после восстановления B');
  assert.equal(a.field.focused, true);
  assert.deepEqual(a.field.range, [3, 3]);
});

// Между сабмитом вкладки A и её перезагрузкой успевают загрузиться чужие
// страницы — они не должны расходовать отметку A.
test('чужие загрузки между сохранением и восстановлением отметку не едят', () => {
  const store = storage();
  const before = input({ 'data-ob-auto-submit': '320' },
    { id: 'ob-list-search', value: 'Ива', selectionStart: 3, selectionEnd: 3 });
  page({ pathname: '/ui/catalog/контрагенты', storage: store, search: before, frameName: 'ob-tab-a' }).type(before);

  const strangerField = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: '' });
  const stranger = page({ pathname: '/ui/document/расходнаянакладная', storage: store, search: strangerField, frameName: 'ob-tab-z' });
  assert.equal(stranger.restore(), null, 'чужая страница потратила отметку A');

  const sameUrlOtherField = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: '' });
  const sameUrlOtherTab = page({ pathname: '/ui/catalog/контрагенты', storage: store, search: sameUrlOtherField, frameName: 'ob-tab-z' });
  assert.equal(sameUrlOtherTab.restore(), null, 'тот же адрес в другой вкладке потратил отметку A');
  assert.equal(sameUrlOtherField.focused, false);

  const after = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: 'Ива' });
  const reloaded = page({ pathname: '/ui/catalog/контрагенты', storage: store, search: after, frameName: 'ob-tab-a' });
  assert.equal(reloaded.restore(), after, 'вкладка A потеряла свою отметку из-за чужих загрузок');
  assert.deepEqual(after.range, [3, 3]);
});

// Скрытая (неактивная) вкладка перезагрузилась сама: фокус активной вкладки
// оболочки она забирать не должна, а собственная отметка расходуется один раз.
test('скрытая вкладка не забирает активный фокус оболочки', () => {
  const store = storage();
  const typeIn = (frameName, hidden) => {
    const field = input({ 'data-ob-auto-submit': '320' },
      { id: 'ob-list-search', value: 'Ива', selectionStart: 3, selectionEnd: 3 });
    page({ pathname: '/ui/catalog/контрагенты', storage: store, search: field, frameName, hidden }).type(field);
  };

  typeIn('ob-tab-a');
  const hiddenField = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: 'Ива' });
  const hiddenTab = page({ pathname: '/ui/catalog/контрагенты', storage: store, search: hiddenField, frameName: 'ob-tab-a', hidden: true });
  assert.equal(hiddenTab.restore(), null, 'скрытая вкладка забрала фокус');
  assert.equal(hiddenField.focused, false, 'скрытая вкладка сфокусировала поиск');

  // Отметка уже потрачена: повторная (теперь активная) загрузка не восстанавливает.
  const again = input({ 'data-ob-auto-submit': '320' }, { id: 'ob-list-search', value: 'Ива' });
  const activeTab = page({ pathname: '/ui/catalog/контрагенты', storage: store, search: again, frameName: 'ob-tab-a' });
  assert.equal(activeTab.restore(), null, 'отметка пережила скрытую загрузку');
  assert.equal(again.focused, false);
});
