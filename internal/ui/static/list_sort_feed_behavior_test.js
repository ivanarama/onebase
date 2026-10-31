const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const start = source.indexOf('function obInitFeed(');
const end = source.indexOf('\nobReady(', start);
assert.ok(start >= 0 && end > start, 'production feed initializer is missing');

test('feed keeps its rendered URL for every page even when another tab changes location', async () => {
  const attrs = {'data-next': '2', 'data-pages': '3', 'data-container': '#list-body', 'data-item': 'tr'};
  const link = {href: 'https://onebase.test/ui/catalog/client?sort=Name&dir=desc&limit=2&q=alpha&lm=feed&page=2'};
  let click;
  const more = {
    isConnected: true,
    getAttribute: (name) => attrs[name],
    setAttribute: (name, value) => { attrs[name] = String(value); },
    querySelector: () => link,
    addEventListener: (_name, handler) => { click = handler; },
    getBoundingClientRect: () => ({top: 10000})
  };
  const container = {isConnected: true, children: [], appendChild(item) { this.children.push(item); }};
  const root = {querySelector: (sel) => sel === '#feed-more' ? more : sel === '#list-body' ? container : null};
  const requests = [];
  const location = new URL('https://onebase.test/ui/catalog/client?limit=2&lm=feed');
  const context = {
    URL, URLSearchParams,
    window: {location, innerHeight: 800},
    document: {documentElement: {}, importNode: (item) => item},
    fetch: async (url) => { requests.push(url); return {text: async () => '<tr></tr>'}; },
    DOMParser: class { parseFromString() { return {querySelectorAll: () => [{}]}; } }
  };
  vm.createContext(context);
  vm.runInContext(source.slice(start, end), context);
  context.obInitFeed(root);
  // The active browser tab no longer describes the captured list instance.
  location.pathname = '/ui/catalog/other';
  location.search = '?sort=Name&dir=asc&lm=feed';
  for (let page = 2; page <= 3; page++) {
    click({target: {closest: () => link}, preventDefault() {}});
    await new Promise(setImmediate);
    const requested = new URL(requests[page - 2], location);
    assert.equal(requested.pathname, '/ui/catalog/client');
    assert.equal(requested.searchParams.get('sort'), 'Name');
    assert.equal(requested.searchParams.get('dir'), 'desc');
    assert.equal(requested.searchParams.get('limit'), '2');
    assert.equal(requested.searchParams.get('q'), 'alpha');
    assert.equal(requested.searchParams.get('page'), String(page));
  }
  assert.equal(container.children.length, 2);
});
