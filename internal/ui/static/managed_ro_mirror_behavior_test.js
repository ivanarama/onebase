'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

// #1672 (план 181C): зеркало значения запертого select-поля синхронизируется
// при применении значений обработчиком (applyValues) и при смене состояния
// запрет (applyElementStates): активно только под запретом, значение — из select.

const managed = fs.readFileSync('static/managed.js', 'utf8');
function slice(beginMark, endMark) {
  const start = managed.indexOf(beginMark);
  const end = managed.indexOf(endMark, start);
  assert.ok(start >= 0 && end > start, 'slice not found: ' + beginMark);
  return managed.slice(start, end + endMark.length);
}
const applyValuesSrc = slice('// BEGIN onebase-ro-apply-values', '// END onebase-ro-apply-values');
const applyStatesSrc = slice('// BEGIN onebase-ro-apply-states', '// END onebase-ro-apply-states');

function element(tag, attrs) {
  const el = {
    tag,
    tagName: tag.toUpperCase(),
    attrs: new Map(),
    children: [],
    parent: null,
    dataset: {},
    style: {},
    value: '',
    disabled: false,
    readOnly: false,
  };
  for (const [k, v] of Object.entries(attrs || {})) el.attrs.set(k, String(v));
  el.hasAttribute = (name) => el.attrs.has(name);
  el.getAttribute = (name) => (el.attrs.has(name) ? el.attrs.get(name) : null);
  el.matches = (sel) => {
    if (sel === '[data-ob-tp]') return el.attrs.has('data-ob-tp');
    if (sel === '[data-ob-el]') return el.attrs.has('data-ob-el');
    if (sel === '[data-ob-ref-current]') return el.attrs.has('data-ob-ref-current');
    return false;
  };
  el.closest = (sel) => {
    let cur = el;
    while (cur) {
      if (cur.matches(sel)) return cur;
      cur = cur.parent;
    }
    return null;
  };
  el.querySelectorAll = (sel) => el.children.filter((c) => matchesSimple(c, sel));
  el.querySelector = (sel) => {
    const hits = el.querySelectorAll(sel);
    return hits.length ? hits[0] : null;
  };
  return el;
}

// Поддержка селекторов, которыми applyElementStates ищет контролы.
function matchesSimple(node, sel) {
  for (const part of sel.split(',').map((s) => s.trim())) {
    const m = /^([a-z]+)(?::not\(\[([a-z-]+)\]\))?$/.exec(part);
    if (m) {
      if (node.tag !== m[1]) continue;
      if (m[2] && node.attrs.has(m[2])) continue;
      return true;
    }
    const attrMirror = /^([a-z]+)\[data-ob-ro-mirror="1"\]$/.exec(part);
    if (attrMirror) {
      if (node.tag === attrMirror[1] && node.attrs.get('data-ob-ro-mirror') === '1') return true;
    }
  }
  return false;
}

function setup(initialSearchUnused) {
  const select = element('select', { name: 'Направление' });
  const mirror = element('input', { id: 'ro-mirror-Направление', name: 'Направление' });
  mirror.dataset.obRoMirror = '1';
  const anchor = element('div', { 'data-ob-el': 'ПолеНаправление' });
  anchor.children = [select, mirror];
  [select, mirror].forEach((n) => { n.parent = anchor; });

  const docHandlers = new Map();
  const sandbox = {
    document: {
      getElementById(id) {
        if (id === 'ro-mirror-Направление') return mirror;
        if (id !== 'main-form') return null;
        return {
          querySelector(sel) {
            const m = /^\[name="([^"]+)"\]$/.exec(sel);
            if (m) return m[1] === 'Направление' ? select : null;
            return null;
          },
        };
      },
      querySelector(sel) {
        const m = /^\[data-ob-el="([^"]+)"\]$/.exec(sel);
        if (m && m[1] === 'ПолеНаправление') return anchor;
        return null;
      },
    },
  };
  sandbox.window = sandbox;
  sandbox.CSS = { escape: (s) => s };
  // managedRefParts живёт вне среза; для проверки синхронизации зеркала
  // достаточно «не ссылка» — значение приедет строкой UUID, как из select.
  sandbox.managedRefParts = () => null;
  // ensureRefOption (дозаполнение опций) вне среза — для проверки синхронизации
  // зеркала достаточно no-op: значение select устанавливается напрямую.
  sandbox.ensureRefOption = () => {};
  sandbox.obRefreshChoiceFilters = undefined;
  sandbox.__select = select;
  sandbox.__mirror = mirror;
  vm.createContext(sandbox);
  vm.runInContext(applyValuesSrc, sandbox, { filename: 'apply-values.js' });
  vm.runInContext(applyStatesSrc, sandbox, { filename: 'apply-states.js' });
  return sandbox;
}

test('applyValues синхронизирует зеркало со значением select', () => {
  const app = setup();
  app.__select.disabled = true; // запертое поле: select не отправится, зеркало — да
  app.window.applyValues({ Направление: '79f4d98a-3ce0-4da4-82ea-e9c8686e804f' });
  assert.equal(app.__select.value, '79f4d98a-3ce0-4da4-82ea-e9c8686e804f');
  assert.equal(app.__mirror.value, '79f4d98a-3ce0-4da4-82ea-e9c8686e804f');
});

test('applyElementStates: под запретом активно зеркало, при снятии — select', () => {
  const app = setup();
  app.__select.value = 'a8b64d2d-d422-490a-9e4e-092eefc47a40';
  app.window.applyElementStates({ readonly: { ПолеНаправление: true } });
  assert.equal(app.__select.disabled, true);
  assert.equal(app.__mirror.disabled, false);
  assert.equal(app.__mirror.value, 'a8b64d2d-d422-490a-9e4e-092eefc47a40');

  app.window.applyElementStates({ readonly: { ПолеНаправление: false } });
  assert.equal(app.__select.disabled, false);
  assert.equal(app.__mirror.disabled, true);
});

test('без состояния запрета зеркало не трогается', () => {
  const app = setup();
  app.__mirror.disabled = true;
  app.window.applyElementStates({});
  assert.equal(app.__mirror.disabled, true);
});
