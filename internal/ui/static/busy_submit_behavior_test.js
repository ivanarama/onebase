// Индикатор выполнения у форм с data-ob-busy — поведение боевого кода из ui.js.
//
// Разметочный тест доказывает только наличие атрибута. Он остался бы зелёным и
// в том случае, если бы скрипт выключал кнопку ДО отправки (форма бы никуда не
// ушла) или трогал формы без признака. Поэтому здесь исполняется тот же кусок
// ui.js в поддельном DOM.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const uiSource = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const start = uiSource.indexOf('/* Индикатор выполнения у форм с data-ob-busy');
if (start < 0) {
  throw new Error('busy submit slice not found in ui.js');
}
const busySource = uiSource.slice(start);

function element(tag, attrs = {}) {
  const attributes = new Map(Object.entries(attrs).map(([k, v]) => [k, String(v)]));
  return {
    nodeType: 1,
    tagName: String(tag || 'div').toUpperCase(),
    disabled: false,
    textContent: 'Выполнить',
    value: 'Выполнить',
    children: [],
    classes: [],
    style: {},
    classList: { add(name) { this.owner.classes.push(name); } },
    getAttribute(name) { return attributes.has(name) ? attributes.get(name) : null; },
    setAttribute(name, v) { attributes.set(String(name), String(v)); },
    appendChild(child) { this.children.push(child); return child; },
    querySelector() { return this.submit || null; },
  };
}

function form(attrs, opts = {}) {
  const f = element('form', attrs);
  const btn = element(opts.buttonTag || 'button');
  btn.classList = { owner: btn, add(name) { btn.classes.push(name); } };
  f.submit = btn;
  f.checkValidity = () => opts.valid !== false;
  return { form: f, button: btn };
}

function run() {
  const timers = [];
  let submitHandler = null;
  const doc = {
    head: element('head'),
    documentElement: element('html'),
    createElement: (tag) => element(tag),
    createTextNode: (text) => ({ nodeType: 3, text: String(text) }),
    addEventListener(type, fn) { if (type === 'submit') submitHandler = fn; },
  };
  doc.head.appendChild = () => {};
  const sandbox = {
    document: doc,
    window: {},
    setTimeout: (fn) => { timers.push(fn); return timers.length; },
  };
  sandbox.window.document = doc;
  vm.createContext(sandbox);
  vm.runInContext(busySource, sandbox);
  assert.ok(submitHandler, 'обработчик submit не установлен');
  return {
    submit(target, defaultPrevented = false) {
      submitHandler({ target, defaultPrevented });
      while (timers.length) timers.shift()();
    },
  };
}

test('кнопка выключается и сообщает о работе — но только после отправки', () => {
  const app = run();
  const { form: f, button: btn } = form({ 'data-ob-busy': 'Выполняется…' });

  // До срабатывания таймера кнопка обязана остаться рабочей: выключенная
  // кнопка не отправляет форму, и запуск бы попросту не состоялся.
  let pending = null;
  const original = btn.disabled;
  app.submit(f);
  pending = btn.disabled;
  assert.equal(original, false, 'кнопка была выключена до отправки');
  assert.equal(pending, true, 'кнопка не выключилась после отправки');
  assert.ok(btn.classes.includes('ob-busy'), 'нет класса занятости');
  assert.equal(btn.children.length, 2, 'ожидались волчок и подпись');
  assert.equal(btn.children[0].className, 'ob-busy-spin');
  assert.equal(btn.children[1].text, 'Выполняется…', 'подпись берётся из разметки, а не зашита в скрипт');
});

test('форма без признака не трогается', () => {
  const app = run();
  const { form: f, button: btn } = form({});
  app.submit(f);
  assert.equal(btn.disabled, false, 'выключена кнопка формы без data-ob-busy');
  assert.equal(btn.textContent, 'Выполнить');
});

test('незаполненная обязательными полями форма остаётся рабочей', () => {
  const app = run();
  // Браузер отменит отправку сам; выключить кнопку здесь значит запереть
  // человека на странице, где нечем повторить отправку.
  const { form: f, button: btn } = form({ 'data-ob-busy': 'Выполняется…' }, { valid: false });
  app.submit(f);
  assert.equal(btn.disabled, false, 'кнопка выключена при непройденной проверке полей');
});

test('отменённая отправка не считается запуском', () => {
  const app = run();
  const { form: f, button: btn } = form({ 'data-ob-busy': 'Выполняется…' });
  app.submit(f, true);
  assert.equal(btn.disabled, false, 'кнопка выключена при отменённой отправке');
});

test('input[type=submit] получает подпись значением', () => {
  const app = run();
  const { form: f, button: btn } = form({ 'data-ob-busy': 'Выполняется…' }, { buttonTag: 'input' });
  app.submit(f);
  assert.equal(btn.value, 'Выполняется…');
  assert.equal(btn.children.length, 0, 'у input не бывает дочерних узлов');
});
