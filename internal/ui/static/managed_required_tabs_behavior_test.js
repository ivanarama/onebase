'use strict';

// Обязательное поле на неактивной вкладке: браузер не может показать подсказку
// у скрытого контрола, поэтому managed.js по событию `invalid` открывает его
// вкладку. Проверяем настоящий обработчик из managed.js на дереве, которое
// отрендерил настоящий шаблон формы (его приносит managed_required_tabs_node_test.go),
// а не пересказ того и другого.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const domPath = process.env.ONEBASE_REQUIRED_TABS_DOM;
assert.ok(domPath, 'ONEBASE_REQUIRED_TABS_DOM must point to the rendered form tree');
const tree = JSON.parse(fs.readFileSync(domPath, 'utf8'));

// Срез = obManagedTabKey + obManagedSwitchTab + блок obManagedReady с
// обработчиком `invalid`. Конец блока — «});» с начала строки: все вложенные
// закрывающие скобки записаны с отступом.
const source = fs.readFileSync('static/managed.js', 'utf8');
const runtimeStart = source.indexOf('function obManagedTabKey(');
const invalidAt = source.indexOf("document.addEventListener('invalid'", runtimeStart);
assert.ok(runtimeStart >= 0 && invalidAt > runtimeStart, 'managed.js: обработчик вкладок не найден');
const runtimeEnd = source.indexOf('\n});', invalidAt);
assert.ok(runtimeEnd > invalidAt, 'managed.js: конец блока вкладок не найден');
const runtime = source.slice(runtimeStart, runtimeEnd + 4);

function parseStyle(value) {
  const style = {};
  String(value || '').split(';').forEach((rule) => {
    const colon = rule.indexOf(':');
    if (colon < 0) return;
    style[rule.slice(0, colon).trim()] = rule.slice(colon + 1).trim();
  });
  return style;
}

// Поддерживаем ровно те формы селекторов, которыми пользуется обработчик:
// «.класс» и «[атрибут="значение"]». Незнакомая форма — ошибка, а не тихое
// «ничего не нашлось»: иначе тест позеленеет на пустой выборке.
function parseSelector(selector) {
  const classes = [];
  const attrs = [];
  const token = /\.([\w-]+)|\[([\w-]+)="([^"]*)"\]/g;
  let consumed = 0;
  let match;
  while ((match = token.exec(selector))) {
    consumed += match[0].length;
    if (match[1] !== undefined) classes.push(match[1]);
    else attrs.push([match[2], match[3]]);
  }
  if (consumed !== selector.length) throw new Error('unsupported selector: ' + selector);
  return {classes, attrs};
}

class ClassList {
  constructor(element) {
    this.element = element;
    this.names = new Set(String(element.getAttribute('class') || '').split(/\s+/).filter(Boolean));
  }

  add(name) { this.names.add(name); this.sync(); }
  remove(name) { this.names.delete(name); this.sync(); }
  contains(name) { return this.names.has(name); }
  sync() { this.element.setAttribute('class', Array.from(this.names).join(' ')); }
}

class Element {
  constructor(spec) {
    this.tagName = String(spec.tag).toUpperCase();
    this.attributes = new Map(Object.entries(spec.attrs || {}));
    this.parentElement = null;
    this.children = (spec.children || []).map((child) => {
      const element = new Element(child);
      element.parentElement = this;
      return element;
    });
    this.style = parseStyle(this.attributes.get('style'));
    this.classList = new ClassList(this);
  }

  getAttribute(name) {
    return this.attributes.has(String(name)) ? this.attributes.get(String(name)) : null;
  }

  setAttribute(name, value) {
    this.attributes.set(String(name), String(value));
  }

  matches(parsed) {
    return parsed.classes.every((name) => this.classList.contains(name))
      && parsed.attrs.every(([name, value]) => this.getAttribute(name) === value);
  }

  closest(selector) {
    const parsed = parseSelector(selector);
    for (let node = this; node; node = node.parentElement) {
      if (node.matches(parsed)) return node;
    }
    return null;
  }

  // Как в браузере: выборка идёт по всем потомкам, а не по прямым детям —
  // переключение обязано отсекать вложенные наборы само (obManagedOwnTabParts).
  querySelectorAll(selector) {
    const parsed = parseSelector(selector);
    const found = [];
    const walk = (node) => {
      for (const child of node.children) {
        if (child.matches(parsed)) found.push(child);
        walk(child);
      }
    };
    walk(this);
    return found;
  }

  querySelector(selector) {
    const found = this.querySelectorAll(selector);
    return found.length ? found[0] : null;
  }
}

function boot() {
  const root = new Element(tree);
  const documentListeners = new Map();
  const timers = [];
  const stored = new Map();
  const fired = [];

  const context = {
    document: {
      body: root,
      readyState: 'complete',
      querySelector(selector) { return root.querySelector(selector); },
      querySelectorAll(selector) { return root.querySelectorAll(selector); },
      addEventListener(type, listener, capture) {
        if (!documentListeners.has(type)) documentListeners.set(type, []);
        documentListeners.get(type).push({listener, capture: Boolean(capture)});
      }
    },
    location: {pathname: '/ui/catalog/Тест/new'},
    sessionStorage: {
      getItem(key) { return stored.has(String(key)) ? stored.get(String(key)) : null; },
      setItem(key, value) { stored.set(String(key), String(value)); }
    },
    setTimeout(fn) { timers.push(fn); return timers.length; },
    obManagedReady(fn) { fn(); },
    // Объект extra создан в песочнице vm — у него свой Object.prototype, и
    // deepStrictEqual отличал бы его от литерала теста. Сравниваем данные.
    obFire(element, event, extra) { fired.push(JSON.parse(JSON.stringify([element, event, extra]))); }
  };
  context.window = context;
  vm.runInNewContext(runtime, context, {filename: 'managed-tabs-runtime.js'});

  const invalidListeners = documentListeners.get('invalid') || [];
  const clickListeners = documentListeners.get('click') || [];
  return {
    root,
    invalidListeners,
    fired,
    // Щелчок по закладке набора name с индексом idx — как браузер: событие
    // приходит документу, цель — кнопка.
    click(name, idx) {
      const tabs = root.querySelector('[data-tabs="' + name + '"]');
      assert.ok(tabs, 'в отрендеренной форме нет набора ' + name);
      const btn = tabs.querySelectorAll('.managed-tab-btn')
        .filter((b) => b.closest('.managed-tabs') === tabs && b.getAttribute('data-tab-idx') === String(idx))[0];
      assert.ok(btn, 'в наборе ' + name + ' нет закладки ' + idx);
      for (const entry of clickListeners) entry.listener({target: btn});
    },
    control(name) {
      const control = root.querySelector('[name="' + name + '"]');
      assert.ok(control, 'в отрендеренной форме нет контрола ' + name);
      return control;
    },
    // Браузер фокусирует невалидный контрол — для этого он должен быть виден
    // на всей цепочке предков, а не только на своей странице.
    visible(element) {
      for (let node = element; node; node = node.parentElement) {
        if (node.style.display === 'none') return false;
      }
      return true;
    },
    fire(control) {
      for (const entry of invalidListeners) entry.listener({target: control});
    },
    flushTimers() {
      timers.splice(0).forEach((fn) => fn());
    },
    pageDisplays() {
      return root.querySelectorAll('.managed-tab-content').map((page) => page.style.display);
    },
    // Активная вкладка каждой группы. Выборка внутри группы захватывает и
    // кнопки вложенных наборов — отсекаем их по ближайшей группе, иначе
    // внешний набор отчитывался бы ещё и за внутренний.
    activeTabIndexes() {
      return root.querySelectorAll('.managed-tabs').map((tabs) => {
        const active = tabs.querySelectorAll('.managed-tab-btn')
          .filter((btn) => btn.closest('.managed-tabs') === tabs && btn.classList.contains('active'));
        return active.map((btn) => btn.getAttribute('data-tab-idx')).join(',');
      });
    }
  };
}

test('обязательное поле на вложенной скрытой вкладке становится видимым', () => {
  const app = boot();
  const hidden = app.control('ПолеВложенное');
  assert.equal(app.visible(hidden), false, 'фикстура сломана: поле обязано быть скрытым до проверки');

  app.fire(hidden);

  assert.equal(app.visible(hidden), true, 'невалидное поле осталось скрытым — браузеру некуда ставить фокус');
  // Обход от внешней страницы к внутренней: при обратном порядке внешнее
  // переключение снова спрятало бы вложенную страницу, и вкладка «1,1» стала
  // бы «1,0» при внешне зелёном «поле на активной вкладке».
  assert.deepEqual(app.activeTabIndexes(), ['1', '1']);
});

test('в одном проходе валидации вкладку открывает только первое поле', () => {
  const app = boot();
  const first = app.control('ПолеВкладки');
  const later = app.control('ПолеВложенное');

  // Браузер шлёт invalid по всем незаполненным контролам в порядке документа и
  // фокусирует первый. Открыв вкладку последнего, мы спрятали бы первый.
  app.fire(first);
  app.fire(later);
  assert.equal(app.visible(first), true, 'следующее невалидное поле утащило вкладку у первого');
  assert.equal(app.visible(later), false);

  app.flushTimers();
  app.fire(later);
  assert.equal(app.visible(later), true, 'защёлка не снялась после прохода — вкладки перестали открываться');
});

test('невалидное поле вне вкладок ничего не переключает', () => {
  const app = boot();
  const before = app.pageDisplays();

  app.fire(app.control('ПолеСнаружи'));

  assert.deepEqual(app.pageDisplays(), before);
  assert.deepEqual(app.activeTabIndexes(), ['0', '0']);
});

test('обработчик слушает фазу перехвата — invalid не всплывает', () => {
  const app = boot();
  assert.equal(app.invalidListeners.length, 1);
  // Без capture=true документ не увидит событие вовсе: invalid не всплывает.
  assert.equal(app.invalidListeners[0].capture, true);
});

test('внешняя закладка не гасит вложенный набор: его первая страница видна', () => {
  const app = boot();
  app.click('Страницы', 1);
  // Раньше переключение внешнего набора снимало active и прятало страницы
  // вложенного: открытая «Дополнительно» показывала одни заголовки.
  assert.deepEqual(app.activeTabIndexes(), ['1', '0']);
  assert.equal(app.visible(app.control('Заметка')), true, 'первая страница вложенного набора скрыта');
});

test('выбор во вложенном наборе переживает уход с внешней закладки и возврат', () => {
  const app = boot();
  app.click('Страницы', 1);
  app.click('ВложенныеСтраницы', 1);
  app.click('Страницы', 0);
  app.click('Страницы', 1);
  assert.deepEqual(app.activeTabIndexes(), ['1', '1']);
  assert.equal(app.visible(app.control('ПолеВложенное')), true, 'выбранная вложенная страница скрыта');
  assert.equal(app.visible(app.control('Заметка')), false, 'вложенный набор показывает две страницы сразу');
});

test('ПриСменеСтраницы: щелчок по другой закладке шлёт событие набору с именем страницы', () => {
  const app = boot();
  app.click('Страницы', 1);
  assert.deepEqual(app.fired, [['Страницы', 'ПриСменеСтраницы', {_page: 'Дополнительно'}]]);

  // Щелчок по уже открытой закладке страницу не меняет — события нет.
  app.click('Страницы', 1);
  assert.equal(app.fired.length, 1, 'повторный щелчок по открытой закладке отправил событие');

  app.click('Страницы', 0);
  assert.deepEqual(app.fired[1], ['Страницы', 'ПриСменеСтраницы', {_page: 'Основная'}]);
});

test('ПриСменеСтраницы: набор без обработчика молчит и не будит внешний', () => {
  const app = boot();
  app.click('Страницы', 1);
  app.fired.length = 0;
  app.click('ВложенныеСтраницы', 1);
  assert.deepEqual(app.fired, [], 'вложенный набор без обработчика отправил событие');
});

test('ПриСменеСтраницы: переход к невалидному полю — не пользовательская смена', () => {
  const app = boot();
  app.fire(app.control('ПолеВложенное'));
  assert.deepEqual(app.activeTabIndexes(), ['1', '1'], 'фикстура: вкладки не переключились');
  assert.deepEqual(app.fired, [], 'открытие вкладки невалидного поля отправило ПриСменеСтраницы');
});
