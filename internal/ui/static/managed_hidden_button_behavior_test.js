'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

// Кнопка, скрытая hidden_when при отрисовке, приходит в разметку с
// display:none. Первое применение состояний запоминало этот 'none' как
// «исходный» display, и hidden=false возвращал его же — кнопка так и не
// появлялась («Утвердить» после «ОК (зарезервировать)»).

const managed = fs.readFileSync('static/managed.js', 'utf8');
const start = managed.indexOf('// BEGIN onebase-ro-apply-states');
const end = managed.indexOf('// END onebase-ro-apply-states', start);
assert.ok(start >= 0 && end > start, 'apply-states slice not found');
const applyStatesSrc = managed.slice(start, end);

function anchor(path, display) {
  return {
    tagName: 'DIV',
    style: { display },
    dataset: {},
    getAttribute: () => null,
    querySelectorAll: () => [],
  };
}

function setup(anchors) {
  const sandbox = {
    document: {
      querySelector(sel) {
        const m = /^\[data-ob-el-path="([^"]+)"\]$/.exec(sel);
        return m && anchors[m[1]] ? anchors[m[1]] : null;
      },
      getElementById: () => null,
    },
    CSS: { escape: (s) => s },
  };
  sandbox.window = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(applyStatesSrc, sandbox, { filename: 'apply-states.js' });
  return sandbox;
}

test('кнопка, скрытая при отрисовке, показывается по hidden=false', () => {
  const button = anchor('0.2', 'none');
  const app = setup({ '0.2': button });
  app.window.applyElementStates({ hidden: { '0.2': false } });
  assert.equal(button.style.display, '', 'после снятия условия кнопка видна');
  app.window.applyElementStates({ hidden: { '0.2': true } });
  assert.equal(button.style.display, 'none');
  app.window.applyElementStates({ hidden: { '0.2': false } });
  assert.equal(button.style.display, '');
});

test('собственный display элемента (flex) по-прежнему восстанавливается', () => {
  const bar = anchor('0.3', 'flex');
  const app = setup({ '0.3': bar });
  app.window.applyElementStates({ hidden: { '0.3': true } });
  assert.equal(bar.style.display, 'none');
  app.window.applyElementStates({ hidden: { '0.3': false } });
  assert.equal(bar.style.display, 'flex');
});
