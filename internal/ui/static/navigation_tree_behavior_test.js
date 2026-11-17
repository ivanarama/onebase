const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');
const source = fs.readFileSync(path.join(__dirname, 'navigation-tree.js'), 'utf8');

function openFolders(query, formVariant) {
  const saved = new Map([['full', '0'], ['total', '0'], ['base', '0']]);
  const folders = [...saved.keys()].map(key => {
    const summary = {setAttribute() {}, addEventListener() {}};
    return {dataset: {navKey: key}, open: true, parentElement: null,
      querySelector: () => summary, addEventListener() {}, matches: () => true};
  });
  const links = folders.map((folder, index) => ({parentElement: folder,
    href: 'http://menu.test/ui/report/work?subsystem=OTK' + (index < 2 ? '&__variant=' + encodeURIComponent(index ? 'Итоговый' : 'Полный') : '')}));
  const root = {dataset: {}, querySelectorAll: selector => selector === 'details.navtree' ? folders : links, addEventListener() {}};
  const document = {readyState: 'complete', documentElement: {},
    getElementById: () => root, querySelector: () => formVariant === undefined ? null : {value: formVariant},
    createElement: () => ({}), head: {appendChild() {}}};
  vm.runInNewContext(source, {window: {}, document, location: {href: 'http://menu.test/ui/report/work?subsystem=OTK' + query}, URL,
    localStorage: {getItem: key => saved.get(key), setItem: (key, value) => saved.set(key, value)},
    MutationObserver: class {observe() {}}, setTimeout});
  assert.deepEqual([...saved.values()], ['0', '0', '0'], 'opening active ancestors must not overwrite saved choices');
  return folders.filter(folder => folder.open).map(folder => folder.dataset.navKey);
}

test('menu report variant expands only its own branch', () => {
  assert.deepEqual(openFolders('&__variant=' + encodeURIComponent('Полный')), ['full']);
});
test('POST result uses the rendered variant when the URL has no query selection', () => {
  assert.deepEqual(openFolders('', 'Итоговый'), ['total']);
});
test('explicit base selection keeps other variants collapsed', () => {
  assert.deepEqual(openFolders('&__variant=', 'Итоговый'), ['base']);
});
