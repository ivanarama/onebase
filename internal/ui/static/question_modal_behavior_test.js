'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

// #1528 (план 158 срез A): модал вопроса рисует кнопки из payload.variants;
// нажатие убирает модал и отправляет событие Ответ с _question_answer.

const uiSource = fs.readFileSync('static/ui.js', 'utf8');
const begin = uiSource.indexOf('// BEGIN onebase-question-modal');
const end = uiSource.indexOf('// END onebase-question-modal', begin);
assert.ok(begin >= 0 && end > begin, 'question modal slice not found');
const modalSource = uiSource.slice(begin, end);

const fired = [];

function makeNode(tag) {
  const node = {
    tag,
    style: { cssText: '' },
    children: [],
    listeners: new Map(),
    textContent: '',
    type: '',
    id: '',
    appendChild(child) { node.children.push(child); child.parent = node; },
    addEventListener(name, fn) { node.listeners.set(name, fn); },
    remove() { if (node.parent) node.parent.children = node.children.filter((c) => c !== node); node.parent = null; },
  };
  node.click = function () {
    const fn = node.listeners.get('click');
    if (fn) fn({ target: node });
  };
  Object.defineProperty(node, 'textContent', {
    get() { return node._text || ''; },
    set(v) { node._text = String(v); },
  });
  return node;
}

function setup() {
  const bodyChildren = [];
  const byId = new Map();
  const documentShim = {
    body: { appendChild(n) { bodyChildren.push(n); } },
    createElement(tag) { return makeNode(tag); },
    getElementById(id) {
      const n = byId.get(id);
      return n && n.parent ? n : null; // снятый с документа узел не находится
    },
  };
  const sandbox = {
    document: documentShim,
    obFire(elementName, eventName, params) { fired.push([elementName, eventName, params]); },
  };
  sandbox.window = sandbox;
  sandbox.__modalNode = function () { return bodyChildren[bodyChildren.length - 1]; };
  sandbox.__byId = byId;
  vm.createContext(sandbox);
  vm.runInContext(modalSource, sandbox, { filename: 'question-modal.js' });
  // Регистрация id в byId имитирует document: после appendChild фиксируем.
  documentShim.body.appendChild = function (n) {
    bodyChildren.push(n);
    n.parent = documentShim.body;
    if (n.id) byId.set(n.id, n);
  };
  return sandbox;
}

const payload = { text: 'Продолжить?', variants: ['Да', 'Нет'], title: 'Подтверждение' };

test('модал рисует текст, заголовок и кнопки вариантов', () => {
  const app = setup();
  app.window.obOpenQuestion(payload, 'Команда');
  const modal = app.__modalNode();
  assert.equal(modal.id, '_question-modal');
  const texts = [];
  (function walk(n) { if (n._text) texts.push(n._text); (n.children || []).forEach(walk); })(modal);
  assert.ok(texts.includes('Продолжить?'));
  assert.ok(texts.includes('Подтверждение'));
  assert.ok(texts.includes('Да') && texts.includes('Нет'));
  assert.equal(fired.length, 0);
});

test('кнопка варианта отправляет Ответ с _question_answer', () => {
  const app = setup();
  app.window.obOpenQuestion(payload, 'Команда');
  const modal = app.__modalNode();
  const buttons = [];
  (function walk(n) { if (n.tag === 'button') buttons.push(n); (n.children || []).forEach(walk); })(modal);
  assert.equal(buttons.length, 2);
  buttons[0].click();
  assert.equal(fired.length, 1);
  assert.equal(fired[0][0], 'Команда');
  assert.equal(fired[0][1], 'Ответ');
  assert.equal(fired[0][2] && fired[0][2]._question_answer, 'Да');
  // После ответа модал закрыт: повторный поиск по id — пусто.
  assert.equal(app.document.getElementById('_question-modal'), null);
});

test('пустой payload модал не создаёт', () => {
  const app = setup();
  app.window.obOpenQuestion({ text: 'x', variants: [] }, 'Команда');
  assert.equal(app.__modalNode(), undefined);
});
