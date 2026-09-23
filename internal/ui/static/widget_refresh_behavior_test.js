const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const uiSource = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const start = uiSource.indexOf('// BEGIN onebase-widget-refresh');
const end = uiSource.indexOf('// END onebase-widget-refresh', start);
if (start < 0 || end < 0) throw new Error('widget refresh source markers not found');
const refreshSource = uiSource.slice(start, end);

function classList() {
  const values = new Set();
  return {
    add(value) { values.add(value); },
    remove(value) { values.delete(value); },
    contains(value) { return values.has(value); },
  };
}

function element() {
  const attrs = new Map();
  const listeners = new Map();
  return {
    attrs,
    listeners,
    disabled: false,
    textContent: '',
    innerHTML: 'old body',
    classList: classList(),
    getAttribute(name) { return attrs.has(name) ? attrs.get(name) : null; },
    setAttribute(name, value) { attrs.set(name, String(value)); },
    removeAttribute(name) { attrs.delete(name); },
    addEventListener(name, fn) { listeners.set(name, fn); },
  };
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

function response(payload, opts = {}) {
  return {
    ok: opts.ok === undefined ? true : opts.ok,
    headers: { get() { return opts.contentType || 'application/json; charset=utf-8'; } },
    json() { return Promise.resolve(payload); },
  };
}

function app() {
  const body = element();
  const button = element();
  const status = element();
  const card = element();
  card.setAttribute('data-widget-url', '/ui/_widget/Test');
  card.setAttribute('data-refresh-error', 'refresh failed');
  card.querySelector = function (selector) {
    if (selector === '[data-ob-widget-body]') return body;
    if (selector === '[data-ob-widget-refresh]') return button;
    if (selector === '[data-ob-widget-status]') return status;
    return null;
  };
  const requests = [];
  class FakeAbortController {
    constructor() { this.signal = { aborted: false }; }
    abort() { this.signal.aborted = true; }
  }
  const sandbox = {
    Promise,
    AbortController: FakeAbortController,
    fetch(url, options) {
      const pending = deferred();
      requests.push({ url, options, pending });
      return pending.promise;
    },
    document: { querySelectorAll() { return [card]; } },
    console,
    obApplyValueAxisFormatter() {},
  };
  sandbox.window = {
    AbortController: FakeAbortController,
    addEventListener() {},
  };
  vm.createContext(sandbox);
  vm.runInContext(refreshSource, sandbox, { filename: 'ui-widget-refresh.js' });
  const controller = sandbox.obWidgetController(card);
  return { sandbox, controller, card, body, button, status, requests };
}

test('manual refresh replaces only the card body and clears busy state', async () => {
  const page = app();
  const done = page.controller.refresh();
  assert.equal(page.requests.length, 1);
  assert.equal(page.requests[0].url, '/ui/_widget/Test');
  assert.equal(page.button.disabled, true);
  assert.equal(page.card.classList.contains('ob-widget-loading'), true);

  page.requests[0].pending.resolve(response({ html: '<div>fresh</div>', chart: null }));
  assert.equal(await done, true);
  assert.equal(page.body.innerHTML, '<div>fresh</div>');
  assert.equal(page.button.disabled, false);
  assert.equal(page.card.classList.contains('ob-widget-loading'), false);
  assert.equal(page.status.textContent, '');
});

test('HTTP or non-JSON failure keeps the old body and reports inside the card', async () => {
  const page = app();
  const done = page.controller.refresh();
  page.requests[0].pending.resolve(response({ html: '<div>login</div>' }, { contentType: 'text/html' }));

  assert.equal(await done, false);
  assert.equal(page.body.innerHTML, 'old body');
  assert.equal(page.status.textContent, 'refresh failed');
  assert.equal(page.button.disabled, false);
});

test('a late old response cannot overwrite the newer refresh', async () => {
  const page = app();
  const oldDone = page.controller.refresh();
  const oldSignal = page.requests[0].options.signal;
  const newDone = page.controller.refresh();
  assert.equal(oldSignal.aborted, true, 'second refresh did not abort the old request');

  page.requests[1].pending.resolve(response({ html: '<div>new</div>', chart: null }));
  assert.equal(await newDone, true);
  assert.equal(page.body.innerHTML, '<div>new</div>');

  page.requests[0].pending.resolve(response({ html: '<div>stale</div>', chart: null }));
  assert.equal(await oldDone, false);
  assert.equal(page.body.innerHTML, '<div>new</div>');
  assert.equal(page.button.disabled, false, 'old completion changed current busy state');
});

test('chart refresh disposes the old instance and applies the returned option', () => {
  const page = app();
  const canvas = element();
  page.card.querySelector = function (selector) {
    if (selector === '.w-chart-canvas[data-widget]') return canvas;
    return null;
  };
  const calls = [];
  page.sandbox.window.echarts = {
    getInstanceByDom(node) {
      assert.equal(node, canvas);
      return { dispose() { calls.push('dispose'); } };
    },
    init(node) {
      assert.equal(node, canvas);
      return {
        setOption(option) { calls.push(['option', option]); },
        resize() {},
        isDisposed() { return false; },
      };
    },
  };
  const option = { series: [] };
  page.sandbox.obInitWidgetChart(page.card, option);
  assert.deepEqual(calls, ['dispose', ['option', option]]);
  assert.equal(option.animation, false);
  assert.equal(canvas.getAttribute('data-ob-init'), '1');
});
