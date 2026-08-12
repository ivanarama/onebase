'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '..', 'static', 'configurator.js'), 'utf8');
const start = source.indexOf('function dbgCaptureLocalBP(');
const end = source.indexOf('function dbgToggleBreakpoint(', start);
assert.ok(start >= 0 && end > start, 'breakpoint helpers not found in configurator.js');

const context = {
  Promise,
  Object,
  JSON,
  _dbgBase: 'base-1',
  _dbgEnabled: true,
  _dbgBreakpoints: {},
  monacoEditors: {},
  document: {getElementById: () => null},
  esc: String,
  alert: () => {},
  dbgRenderBPList: () => {},
  dbgRenderBreakpoints: () => {},
};
vm.createContext(context);
vm.runInContext(source.slice(start, end), context);

function rejectedFetch(message) {
  return async () => ({status: 400, json: async () => ({error: message})});
}

test('invalid edit restores the previous condition', async () => {
  context._dbgBreakpoints = {module: {7: 'Новое неверное('}};
  context.fetch = rejectedFetch('bad condition');

  await context.dbgSendBP('module', 7, 'set', 'Новое неверное(', {
    had: true,
    value: 'Сч = 4',
  });

  assert.equal(context._dbgBreakpoints.module[7], 'Сч = 4');
});

test('rejected new breakpoint is removed locally', async () => {
  context._dbgBreakpoints = {module: {8: 'Неверное('}};
  context.fetch = rejectedFetch('bad condition');

  await context.dbgSendBP('module', 8, 'set', 'Неверное(', {had: false});

  assert.equal(Object.hasOwn(context._dbgBreakpoints, 'module'), false);
});

test('late rejection does not overwrite a newer local edit', async () => {
  let rejectFirst;
  context._dbgBreakpoints = {module: {9: 'Первое неверное('}};
  context.fetch = () => new Promise((resolve) => {
    rejectFirst = () => resolve({status: 400, json: async () => ({error: 'bad condition'})});
  });
  const first = context.dbgSendBP('module', 9, 'set', 'Первое неверное(', {
    had: true,
    value: 'Сч = 4',
  });

  context._dbgBreakpoints.module[9] = 'Сч = 5';
  context.fetch = async () => ({status: 200, json: async () => ({id: 'bp-9'})});
  await context.dbgSendBP('module', 9, 'set', 'Сч = 5', {
    had: true,
    value: 'Первое неверное(',
  });

  rejectFirst();
  await first;
  assert.equal(context._dbgBreakpoints.module[9], 'Сч = 5');
});
