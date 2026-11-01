const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const start = source.indexOf('function toggleTreeNode(');
const end = source.indexOf('\nfunction treeSetVisible(', start);
assert.ok(start >= 0 && end > start, 'tree production function is missing');

test('tree fetch preserves the filter toggle from the rendered list configuration', async () => {
  for (const off of ['', '0', '0,2']) {
    let requested;
    let inserted;
    const attributes = new Map();
    const tr = {dataset: {treeDepth: '2'}};
    const btn = {
      dataset: {folderId: 'folder id'},
      closest: () => tr,
      getAttribute: (name) => attributes.get(name),
      setAttribute: (name, value) => attributes.set(name, value)
    };
    const sandbox = {
      encodeURIComponent,
      obListConfig: () => ({treeEntity: 'Заявка', subsystem: 'Работа', listFormOff: off}),
      insertTreeRows: (parent, rows) => { inserted = {parent, rows}; },
      fetch: async (url, options) => {
        requested = {url: new URL(url, 'https://onebase.test'), options};
        return {ok: true, json: async () => ({rows: [{id: 'child'}]})};
      }
    };
    vm.createContext(sandbox);
    vm.runInContext(source.slice(start, end), sandbox);
    sandbox.toggleTreeNode(btn);
    // Drain the fetch/json/render promise chain, including finally.
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(requested.url.pathname, '/ui/_tree-children/' + encodeURIComponent('Заявка'));
    assert.equal(requested.url.searchParams.get('parent'), 'folder id');
    assert.equal(requested.url.searchParams.get('depth'), '2');
    assert.equal(requested.url.searchParams.get('subsystem'), 'Работа');
    assert.equal(requested.url.searchParams.get('lf_off'), off || null);
    assert.equal(requested.options.credentials, 'same-origin');
    assert.equal(inserted.parent, tr);
    assert.equal(inserted.rows[0].id, 'child');
    assert.equal(attributes.get('data-loaded'), '1');
    assert.equal(btn.disabled, false);
  }
});
