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

// Строка списка в новом контракте несёт идентификатор, а ссылку собирает
// obRowUrl(). Здесь моделируем строку, пришедшую из JSON-подгрузки: у неё есть
// собственная ссылка в dataset, и она имеет приоритет над сборкой из контейнера.
function row(url) {
  return {
    dataset: { obDetailUrl: url },
    closest() { return null; },
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
    // Слайс зовёт общий сборщик ссылок; здесь строка моделирует запись из
    // JSON-подгрузки, у которой ссылка уже готова.
    obRowUrl(row) { return row && row.dataset ? (row.dataset.obDetailUrl || '') : ''; },
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
