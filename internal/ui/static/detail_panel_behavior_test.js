'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const begin = source.indexOf('// BEGIN onebase-detail-fetch');
const end = source.indexOf('// END onebase-detail-fetch');
assert.ok(begin >= 0 && end > begin, 'detail fetch markers must exist');

function settle() {
  return new Promise((resolve) => setImmediate(resolve));
}

function row(url) {
  return {
    getAttribute(name) { return name === 'data-ob-detail-url' ? url : ''; }
  };
}

function harness() {
  const fields = { textContent: '' };
  const empty = { hidden: false };
  const panel = {
    querySelector(selector) {
      if (selector === '[data-ob-detail-fields]') return fields;
      if (selector === '[data-ob-detail-empty]') return empty;
      return null;
    }
  };
  const requests = [];
  let selected = null;
  let renders = 0;

  class FakeAbortController {
    constructor() { this.signal = { aborted: false }; }
    abort() { this.signal.aborted = true; }
  }

  const context = {
    AbortController: FakeAbortController,
    obDetailEl() { return panel; },
    listSel() { return selected; },
    obDetailRender() { renders++; },
    fetch(url, options) {
      let resolve;
      let reject;
      const promise = new Promise((ok, fail) => { resolve = ok; reject = fail; });
      requests.push({ url, options, resolve, reject });
      return promise;
    }
  };
  vm.runInNewContext(source.slice(begin, end) + `
    this.api = {
      fetch: obDetailFetch,
      invalidate: obDetailInvalidate,
      cache: function () { return obDetailCache; },
      pending: function () { return obDetailPending; }
    };`, context, { filename: 'ui-detail-fetch-slice.js' });
  return {
    api: context.api,
    fields,
    requests,
    select(value) { selected = value; },
    renders() { return renders; }
  };
}

function response(body) {
  return { ok: true, text() { return Promise.resolve(body); } };
}

test('a stale success cannot replace the current row cache', async () => {
  const h = harness();
  const a = row('/a');
  const b = row('/b');
  h.select(a);
  h.api.fetch(a, '/a');
  h.select(b);
  h.api.fetch(b, '/b');
  assert.equal(h.requests.length, 2);
  assert.equal(h.requests[0].options.signal.aborted, true);

  h.requests[1].resolve(response('{"title":"B","tabs":[]}'));
  await settle();
  await settle();
  assert.equal(h.api.cache().url, '/b');
  assert.equal(h.api.cache().body, '{"title":"B","tabs":[]}');
  assert.equal(h.renders(), 1);

  h.requests[0].resolve(response('{"title":"A","tabs":[]}'));
  await settle();
  await settle();
  assert.equal(h.api.cache().url, '/b');
  assert.equal(h.api.cache().body, '{"title":"B","tabs":[]}');
  assert.equal(h.renders(), 1);
});

test('a stale failure cannot erase a newer row or show its error', async () => {
  const h = harness();
  const a = row('/a');
  const b = row('/b');
  h.select(a);
  h.api.fetch(a, '/a');
  h.select(b);
  h.api.fetch(b, '/b');
  h.requests[1].resolve(response('{"title":"B","tabs":[]}'));
  await settle();
  await settle();

  h.requests[0].reject(new Error('old request failed'));
  await settle();
  await settle();
  assert.equal(h.api.cache().url, '/b');
  assert.equal(h.api.cache().body, '{"title":"B","tabs":[]}');
  assert.doesNotMatch(h.fields.textContent, /old request failed/);
});

test('duplicate pending reads are deduplicated and invalidation aborts them', () => {
  const h = harness();
  const a = row('/a');
  h.select(a);
  h.api.fetch(a, '/a');
  h.api.fetch(a, '/a');
  assert.equal(h.requests.length, 1);
  h.api.invalidate();
  assert.equal(h.requests[0].options.signal.aborted, true);
  assert.equal(h.api.cache().url, '');
  assert.equal(h.api.cache().body, '');
  assert.equal(h.api.pending().url, '');
});

// Панель деталей — <aside> с белым фоном, и общее оформление меню
// (тёмный фон, color:#fff) раньше действовало на оба <aside> разом: значения
// рисовались белым по белому (#1670). Харнесс без CSS-движка, поэтому
// регрессия держится структурно: в обслуживаемых исходниках стилей не должно
// остаться голого селектора aside, а правило панели не задаёт белый цвет.
test('detail panel values do not inherit the nav white-on-dark aside styling', () => {
  const templates = fs.readFileSync(path.join(__dirname, '..', 'templates.go'), 'utf8');
  // Навигация сохраняет своё оформление — тёмный фон и белые ссылки, но
  // адресованные точно: меню #ob-nav, а не все <aside> страницы.
  assert.match(templates, /#ob-nav\{width:210px;background:#1e293b;color:#fff;/);
  assert.equal((templates.match(/(^|[^-\w])aside\{/g) || []).length, 0,
    'голый селектор aside{ возвращает общий стиль панели меню обеим панелям');
  assert.equal((templates.match(/nav-open aside/g) || []).length, 0,
    'мобильная шторка обязана адресовать #ob-nav, а не оба <aside>');
  assert.match(templates, /#ob-nav\{position:fixed;left:0;top:0;bottom:0;/,
    'на узком окне фиксированной шторкой остаётся только меню');

  const uiSource = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
  const panelRule = uiSource.match(/\.ob-detail\{[^}]*\}/);
  assert.ok(panelRule, 'ui.js должен задавать правило .ob-detail');
  assert.match(panelRule[0], /background:#fff/);
  assert.doesNotMatch(panelRule[0], /color:#fff/);
});
