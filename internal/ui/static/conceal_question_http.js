'use strict';

// Drive the production event queue, value renderer and question modal with
// HTTP responses supplied by the Go matrix. No grant is simulated here.
const fs = require('node:fs');
const vm = require('node:vm');
const readline = require('node:readline');
const assert = require('node:assert/strict');
const setup = fs.readFileSync('static/managed_close_intent_behavior_test.js', 'utf8');
const boundary = setup.indexOf("\ntest(");
assert.ok(boundary > 0);
const helpers = {require, module: {exports: {}}, URLSearchParams, AbortController, setTimeout, clearTimeout};
vm.runInNewContext(setup.slice(0, boundary) + '\nmodule.exports = {runtime, response};', helpers);
const {runtime, response} = helpers.module.exports;
const input = readline.createInterface({input: process.stdin})[Symbol.asyncIterator]();
const read = async () => JSON.parse((await input.next()).value);
const send = (value) => process.stdout.write(JSON.stringify(value) + '\n');

(async () => {
  const config = await read();
  let calls = 0;
  const app = runtime(async (_url, options) => {
    send({event: Object.fromEntries(options.body)});
    const data = await read();
    calls++;
    return response(data);
  }, {docId: config.id}, true);
  const form = app.form;
  form.values.set('Секрет', '');
  form.values.set('Причина', 'проверена');
  form.getAttribute = () => config.token;
  const field = form.control('Секрет');
  field.disabled = true;
  field.readOnly = config.readonly;
  field.tagName = config.multiline ? 'TEXTAREA' : 'INPUT';
  field.getAttribute = (name) => name === 'name' ? 'Секрет' : null;
  const query = form.querySelectorAll.bind(form);
  form.querySelectorAll = (selector) => selector === '[data-ob-concealed]' ? [field] : query(selector);
  // Native FormData omits disabled controls, including a concealed input.
  app.context.FormData = class {
    constructor(f) {
      this.values = new Map(f ? [...f.values].filter(([name]) => !f.control(name).disabled) : []);
    }
    set(k, v) { this.values.set(k, String(v)); }
    append(k, v) { this.values.set(k, String(v)); }
    forEach(fn) { this.values.forEach(fn); }
  };
  const managed = fs.readFileSync('static/managed.js', 'utf8');
  const start = managed.indexOf('function obApplyConcealed(');
  const end = managed.indexOf('window.obApplyConcealed = obApplyConcealed;', start);
  vm.runInNewContext(managed.slice(start, end) + '\nwindow.obApplyConcealed = obApplyConcealed;', app.context);
  const ui = fs.readFileSync('static/ui.js', 'utf8');
  vm.runInNewContext(ui.slice(ui.indexOf('// BEGIN onebase-question-modal'), ui.indexOf('// END onebase-question-modal')), app.context);
  await app.context.obFire(config.question ? 'РаскрытьСВопросом' : 'Показать', 'Нажатие');
  assert.equal(field.disabled, false, 'disclosed value must become submittable');
  assert.equal(field.value, config.initial, 'apply value before any question exit');
  const modal = app.document.getElementById('_question-modal');
  assert.equal(!!modal, config.question);
  if (modal) {
    if (config.action === 'answer') {
      const fire = app.context.obFire;
      let answer;
      app.context.obFire = (...args) => { answer = fire(...args); return answer; };
      const box = modal.children[0];
      box.children.at(-1).children[0].dispatch('click', {});
      assert.ok(answer, 'answer must use the event queue');
      await answer;
      assert.equal(calls, 2);
    } else {
      modal.dispatch('click', {target: modal});
      assert.equal(app.document.getElementById('_question-modal'), null);
      assert.equal(calls, 1, 'backdrop must not send an answer');
    }
  }
  if (config.action === 'edit' || config.action === 'clear') {
    field.value = config.action === 'clear' ? '' : '56-78';
  }
  if (['revoke', 'expired', 'error'].includes(config.action)) {
    await app.context.obFire(config.action === 'revoke' ? 'Отозвать' : config.action === 'error' ? 'Ошибка' : '', config.action === 'expired' ? 'ПриОткрытии' : 'Нажатие');
    assert.equal(field.disabled, true);
    assert.equal(field.value, '');
  }
  assert.equal(field.readOnly, config.readonly);
  const data = new app.context.FormData(form);
  const body = {};
  data.forEach((value, name) => { body[name] = value; });
  send({submit: body, calls});
})().catch((error) => { console.error(error); process.exitCode = 1; }).finally(() => process.stdin.destroy());
