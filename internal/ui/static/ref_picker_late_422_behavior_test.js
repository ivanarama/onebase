// Окно подбора ссылки (openRefPicker): поздний отказ поиска (422) старого
// запроса не стирает результат нового. Без AbortController старый запрос не
// отменяется: его тело ошибки дочитывается уже после свежего ответа, и раньше
// ветка 422 очищала список и писала «Search unavailable» до проверки
// актуальности. Исполняется настоящий loadServer из ui.js (срез), окружение —
// подставное: fetch с ручным ответом, поле и диалог.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');

const source = fs.readFileSync('static/ui.js', 'utf8');
const start = source.indexOf('  function loadServer(q) {');
const end = source.indexOf('  window._rpTarget = sel;', start);
assert.ok(start >= 0 && end > start, 'срез loadServer не найден');

function runtime({snapshot = null} = {}) {
  const requests = [];
  const fetch = (url, options) => new Promise((resolve) => requests.push({url, options, resolve}));
  const state = {rendered: [], status: {textContent: ''}, snapshot};
  const sel = {value: '', form: {}, getAttribute() { return null; }};
  const env = {
    refEntity: 'Улицы',
    window: {fetch, AbortController: undefined},
    fetch,
    sel,
    modal: {},
    status: state.status,
    renderLocal() { state.rendered.push('local'); },
    renderItems(items) { state.rendered.push(items.map((i) => i.label)); },
    rpReveal() {},
    rpActiveId() { return ''; },
    rpApplyPreviewLayout() {},
    obRefChoiceSnapshot() { return state.snapshot; },
    obRefChoiceQuery() { return ''; },
    obRefFilterParam() { return ''; },
    // Есть не во всех сборках (отметка помеченных, #2365); лишний параметр
    // фабрики безвреден.
    obRefMarkedParam() { return ''; },
    obRefFilterValues() { return {}; },
    obRefSourceValue() { return ''; },
    obChoiceSelectIsLive() { return true; },
  };
  const names = Object.keys(env);
  const factory = new Function(...names,
    'var requestSeq = 0, requestController = null, rpPreviewField = "";\n' +
    source.slice(start, end) + '\nreturn loadServer;');
  const loadServer = factory(...names.map((n) => env[n]));
  async function respond(index, status, data) {
    requests[index].resolve({ok: status === 200, status, json: async () => data});
    for (let i = 0; i < 5; i++) await new Promise((r) => setImmediate(r));
  }
  return {loadServer, requests, state, respond};
}

test('поздний 422 старого запроса не стирает новый результат', async () => {
  const r = runtime();
  r.loadServer('стар');
  r.loadServer('новая');
  assert.equal(r.requests.length, 2);
  await r.respond(1, 200, {items: [{id: 'n1', _label: 'New result'}], total: 1});
  assert.deepEqual(r.state.rendered, [['New result']]);
  assert.equal(r.state.status.textContent, '');
  await r.respond(0, 422, {error: 'Search unavailable', items: []});
  assert.deepEqual(r.state.rendered, [['New result']], 'поздний 422 перерисовал список');
  assert.equal(r.state.status.textContent, '', 'поздний 422 сменил статус');
});

test('поздний 422 после смены контекста выбора тоже игнорируется', async () => {
  const r = runtime({snapshot: {query: '&ctx=a', fingerprint: 'A'}});
  r.loadServer('стар');
  r.state.snapshot = {query: '&ctx=b', fingerprint: 'B'};
  await r.respond(0, 422, {error: 'Search unavailable', items: []});
  assert.deepEqual(r.state.rendered, [], 'отказ для прежнего контекста перерисовал список');
  assert.equal(r.state.status.textContent, 'Загрузка...');
});

test('актуальный 422 по-прежнему показывает отказ функции поиска', async () => {
  const r = runtime();
  r.loadServer('стар');
  await r.respond(0, 422, {error: 'Search unavailable', items: []});
  assert.deepEqual(r.state.rendered, [[]]);
  assert.equal(r.state.status.textContent, 'Search unavailable');
});
