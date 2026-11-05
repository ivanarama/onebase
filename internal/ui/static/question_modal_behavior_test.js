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

function walk(n, out) { out.push(n); (n.children || []).forEach((c) => walk(c, out)); return out; }

test('поля диалога: строка только для показа, поле ввода и многострочное', () => {
  fired.length = 0;
  const app = setup();
  app.window.obOpenQuestion({
    text: 'Уведомить тех. отдел?', variants: ['Отправить', 'Отмена'],
    fields: [
      { name: 'Номер', label: 'Заявка', value: 'ЗК-17', strong: true },
      { name: 'Кому', label: 'Кому', value: 'техотдел', editable: true },
      { name: 'Ошибка', label: 'Текст ошибки', value: '', editable: true, multiline: true },
    ],
  }, 'Команда');
  const nodes = walk(app.__modalNode(), []);
  assert.ok(nodes.some((n) => n._text === 'ЗК-17'), 'строка для показа выводится текстом');
  const inputs = nodes.filter((n) => n.tag === 'input');
  const areas = nodes.filter((n) => n.tag === 'textarea');
  assert.equal(inputs.length, 1, 'однострочное поле ввода');
  assert.equal(areas.length, 1, 'многострочное поле — textarea');
  inputs[0].value = 'дежурный';
  areas[0].value = 'строка 1\nстрока 2';
  nodes.filter((n) => n.tag === 'button')[0].click();
  assert.equal(fired.length, 1);
  assert.equal(fired[0][2]._question_answer, 'Отправить');
  assert.deepEqual(JSON.parse(fired[0][2]._question_fields), { Кому: 'дежурный', Ошибка: 'строка 1\nстрока 2' },
    'введённое уезжает в _question_fields, строка «только показать» — нет');
});

test('строк: высота многострочного поля и окно шире', () => {
  const app = setup();
  app.window.obOpenQuestion({
    text: 'Уведомить тех. отдел?', variants: ['Отправить', 'Отмена'],
    fields: [
      { name: 'Ошибка', editable: true, multiline: true, rows: 7 },
      { name: 'Примечание', editable: true, multiline: true },
    ],
  }, 'Команда');
  const modal = app.__modalNode();
  const areas = walk(modal, []).filter((n) => n.tag === 'textarea');
  assert.equal(areas[0].rows, 7, 'строк задаёт видимых строк');
  assert.equal(areas[1].rows, 4, 'без строк — 4');
  assert.match(areas[0].style.cssText, /width:100%/, 'поле во всю ширину окна');
  assert.match(modal.children[0].style.cssText, /width:640px/, 'с многострочным полем окно шире');
});

test('без многострочного поля окно прежней ширины', () => {
  const app = setup();
  app.window.obOpenQuestion({ text: 'Готово', variants: ['ОК'], fields: [{ name: 'Кому', editable: true }] }, 'Команда');
  assert.match(app.__modalNode().children[0].style.cssText, /width:460px/);
});

test('без редактируемых полей _question_fields не отправляется', () => {
  fired.length = 0;
  const app = setup();
  app.window.obOpenQuestion({ text: 'Готово', variants: ['ОК'], fields: [{ value: 'ЗК-17' }] }, 'Команда');
  walk(app.__modalNode(), []).filter((n) => n.tag === 'button')[0].click();
  assert.equal(fired[0][2]._question_fields, undefined);
});

test('четыре и больше вариантов — столбиком с прокруткой', () => {
  const app = setup();
  app.window.obOpenQuestion({ text: 'Причина?', variants: ['А', 'Б', 'В', 'Г'] }, 'Команда');
  const row = walk(app.__modalNode(), []).find((n) => (n.children || []).some((c) => c.tag === 'button'));
  assert.match(row.style.cssText, /flex-direction:column/);
  assert.match(row.style.cssText, /overflow-y:auto/);
});

test('обработчики вызывающего получают ответ и закрытие мимо кнопок вместо события Ответ', () => {
  const app = setup();
  const answers = [];
  let dismissed = 0;
  const before = fired.length;
  app.window.obOpenQuestion(payload, '', { onAnswer: (v) => answers.push(v), onDismiss: () => { dismissed++; } });
  let modal = app.__modalNode();
  const buttons = [];
  (function walk(n) { if (n.tag === 'button') buttons.push(n); (n.children || []).forEach(walk); })(modal);
  buttons[1].click();
  assert.deepEqual(answers, ['Нет']);
  assert.equal(fired.length, before, 'событие Ответ при своих обработчиках не отправляется');

  app.window.obOpenQuestion(payload, '', { onAnswer: (v) => answers.push(v), onDismiss: () => { dismissed++; } });
  modal = app.__modalNode();
  modal.listeners.get('click')({ target: modal });
  assert.equal(dismissed, 1);
  assert.deepEqual(answers, ['Нет']);
});


test('callback получает один ответ со строковым и многострочным вводом', () => {
  const app = setup();
  const answers = [];
  const before = fired.length;
  app.obOpenQuestion({text: 'Провести?', variants: ['Да'], fields: [
    {name: 'Кому', editable: true}, {name: 'Комментарий', editable: true, multiline: true},
  ]}, '', {onAnswer: (variant, extra) => answers.push({variant, extra})});
  const nodes = walk(app.__modalNode(), []);
  nodes.find((n) => n.tag === 'input').value = 'дежурный';
  nodes.find((n) => n.tag === 'textarea').value = 'строка 1\nстрока 2';
  nodes.find((n) => n.tag === 'button').click();
  assert.equal(answers.length, 1);
  assert.equal(answers[0].variant, 'Да');
  assert.equal(answers[0].extra._question_answer, 'Да');
  assert.deepEqual(JSON.parse(answers[0].extra._question_fields), {Кому: 'дежурный', Комментарий: 'строка 1\nстрока 2'});
  assert.equal(fired.length, before);
});

test('callback без ввода получает только ответ; backdrop не отвечает', () => {
  const app = setup();
  const answers = [];
  let dismissed = 0;
  const handlers = {onAnswer: (variant, extra) => answers.push(extra), onDismiss: () => dismissed++};
  const question = {text: 'Провести?', variants: ['Да'], fields: [{name: 'Показ', value: 'текст'}]};
  app.obOpenQuestion(question, '', handlers);
  walk(app.__modalNode(), []).find((n) => n.tag === 'button').click();
  assert.equal(answers.length, 1);
  assert.equal(answers[0]._question_answer, 'Да');
  assert.equal(Object.hasOwn(answers[0], '_question_fields'), false);
  app.obOpenQuestion(question, '', handlers);
  const modal = app.__modalNode();
  modal.listeners.get('click')({target: modal});
  assert.equal(dismissed, 1);
  assert.equal(answers.length, 1);
});
