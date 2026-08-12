'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '..', 'static', 'configurator.js'), 'utf8');
const start = source.indexOf('function dbgCaptureLocalBP(');
const end = source.indexOf('// dbgEditBPCondition', start);
assert.ok(start >= 0 && end > start, 'breakpoint helpers not found in configurator.js');

let alerts = [];
const context = {
  Promise,
  Object,
  JSON,
  String,
  _dbgBase: 'base-1',
  _dbgEnabled: true,
  _dbgBreakpoints: {},
  _dbgBPSync: Object.create(null),
  monacoEditors: {module: {}},
  document: {getElementById: () => null},
  esc: String,
  alert: (message) => alerts.push(message),
  dbgRenderBPList: () => {},
  dbgRenderBreakpoints: () => {},
};
vm.createContext(context);
vm.runInContext(source.slice(start, end), context);

function response(status, body) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  };
}

function reset(local) {
  alerts = [];
  context._dbgBreakpoints = local;
  context._dbgBPSync = Object.create(null);
  context.monacoEditors = {module: {}};
}

test('invalid edit restores the last server-confirmed condition', async () => {
  reset({module: {7: 'Новое неверное('}});
  context.fetch = async () => response(400, {error: 'bad condition'});

  await context.dbgSendBP('module', 7, 'set', 'Новое неверное(', {
    had: true,
    value: 'Сч = 4',
  });

  assert.equal(context._dbgBreakpoints.module[7], 'Сч = 4');
  assert.equal(alerts.length, 1);
});

test('rejected new breakpoint is removed locally', async () => {
  reset({module: {8: 'Неверное('}});
  context.fetch = async () => response(400, {error: 'bad condition'});

  await context.dbgSendBP('module', 8, 'set', 'Неверное(', {had: false});

  assert.equal(Object.hasOwn(context._dbgBreakpoints, 'module'), false);
});

test('network rejection rolls optimistic state back', async () => {
  reset({module: {9: 'Новое'}});
  context.fetch = async () => { throw new Error('connection lost'); };

  const result = await context.dbgSendBP('module', 9, 'set', 'Новое', {
    had: true,
    value: 'Старое',
  });

  assert.match(result.error, /connection lost/);
  assert.equal(context._dbgBreakpoints.module[9], 'Старое');
});

test('malformed response rolls optimistic state back', async () => {
  reset({module: {10: 'Новое'}});
  context.fetch = async () => ({
    ok: true,
    status: 200,
    json: async () => { throw new Error('not json'); },
  });

  const result = await context.dbgSendBP('module', 10, 'set', 'Новое', {
    had: true,
    value: 'Старое',
  });

  assert.match(result.error, /некорректный JSON/);
  assert.equal(context._dbgBreakpoints.module[10], 'Старое');
});

test('two valid edits reach the server in user order', async () => {
  reset({module: {11: 'A'}});
  const calls = [];
  let releaseFirst;
  let serverCondition = 'До';
  context.fetch = (_url, options) => {
    const request = JSON.parse(options.body);
    calls.push(request);
    if (calls.length === 1) {
      return new Promise((resolve) => {
        releaseFirst = () => {
          serverCondition = request.condition;
          resolve(response(200, {id: 'bp-11', file: 'module', line: 11}));
        };
      });
    }
    serverCondition = request.condition;
    return Promise.resolve(response(200, {id: 'bp-11', file: 'module', line: 11}));
  };

  const first = context.dbgSendBP('module', 11, 'set', 'A', {had: true, value: 'До'});
  context._dbgBreakpoints.module[11] = 'B';
  const second = context.dbgSendBP('module', 11, 'set', 'B', {had: true, value: 'A'});

  await Promise.resolve();
  assert.equal(calls.length, 1, 'second request started before the first response');
  releaseFirst();
  await Promise.all([first, second]);

  assert.deepEqual(calls.map((call) => call.condition), ['A', 'B']);
  assert.equal(serverCondition, 'B');
  assert.equal(context._dbgBreakpoints.module[11], 'B');
});

test('two rejected edits restore the original confirmed condition', async () => {
  reset({module: {12: 'Первое неверное('}});
  context.fetch = async () => response(400, {error: 'bad condition'});

  const first = context.dbgSendBP('module', 12, 'set', 'Первое неверное(', {
    had: true,
    value: 'Сч = 4',
  });
  context._dbgBreakpoints.module[12] = 'Второе неверное(';
  const second = context.dbgSendBP('module', 12, 'set', 'Второе неверное(', {
    had: true,
    value: 'Первое неверное(',
  });

  await Promise.all([first, second]);
  assert.equal(context._dbgBreakpoints.module[12], 'Сч = 4');
});

test('two rapid gutter toggles use explicit set then remove', async () => {
  reset({});
  const calls = [];
  let serverHasBreakpoint = false;
  context.fetch = async (_url, options) => {
    const request = JSON.parse(options.body);
    calls.push(request);
    if (request.action === 'set') {
      serverHasBreakpoint = true;
      return response(200, {id: 'bp-13', file: 'module', line: 13});
    }
    serverHasBreakpoint = false;
    return response(200, {status: 'removed'});
  };

  const first = context.dbgToggleBreakpoint('module', 13);
  const second = context.dbgToggleBreakpoint('module', 13);
  await Promise.all([first, second]);

  assert.deepEqual(calls.map((call) => call.action), ['set', 'remove']);
  assert.equal(serverHasBreakpoint, false);
  assert.equal(Object.hasOwn(context._dbgBreakpoints, 'module'), false);
});
