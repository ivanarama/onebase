const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const itemsStart = source.indexOf('function listBasedOnItems');
const itemsEnd = source.indexOf('\nfunction showListMenu', itemsStart);
const treeStart = source.indexOf('function makeTreeRow');
const treeEnd = source.indexOf('\nfunction listBasedOnItems', treeStart);
const menuStart = source.indexOf('function showListMenu');
const menuEnd = source.indexOf('\nfunction listCtxMenu', menuStart);
if (itemsStart < 0 || itemsEnd < 0 || treeStart < 0 || treeEnd < 0 || menuStart < 0 || menuEnd < 0) {
  throw new Error('based-on production slices not found');
}

let config = {};
let opened = null;
global.window = {location: {href: ''}};
global.obListConfig = () => config;
global.listOpen = (url, title) => { opened = {url, title}; };
global.listSubmit = () => {};
vm.runInThisContext(source.slice(itemsStart, itemsEnd), {filename: 'ui-list-based-on-items.js'});
vm.runInThisContext(source.slice(treeStart, treeEnd), {filename: 'ui-list-based-on-tree.js'});

function row(id = '11111111-1111-1111-1111-111111111111') {
  return {dataset: {obEntityId: id, openUrl: '/ui/document/order/' + id}};
}

test('selected row builds one safe based-on submenu from server config', () => {
  config = {
    labels: {basedOn: 'Ввести на основании'},
    basedOn: [
      {label: 'Счёт покупателю', url: '/ui/document/invoice/new?based_on=Order'},
      {label: 'Внешний адрес', url: 'https://example.test/new?based_on=Order'},
      {label: '', url: '/ui/document/empty/new?based_on=Order'}
    ]
  };
  opened = null;
  const items = listMenuItems(row('id with / and ?'));
  const submenu = items.find((item) => item.items);
  assert.ok(submenu, 'based-on submenu is missing');
  assert.equal(submenu.label, 'Ввести на основании');
  assert.equal(submenu.items.length, 1, 'malformed/non-relative commands were not filtered');
  submenu.items[0].fn();
  assert.deepEqual(opened, {
    url: '/ui/document/invoice/new?based_on=Order&based_on_id=id%20with%20%2F%20and%20%3F',
    title: 'Счёт покупателю'
  });
});

test('missing row ID or authorized receivers yields no based-on command', () => {
  config = {labels: {basedOn: 'Ввести на основании'}, basedOn: []};
  assert.equal(listMenuItems(row()).some((item) => item.items), false);

  config.basedOn = [{label: 'Счёт', url: '/ui/document/invoice/new?based_on=Order'}];
  assert.equal(listMenuItems(row('')).some((item) => item.items), false);
});

test('lazy tree rows preserve the entity ID used by based-on actions', () => {
  function element() {
    const attributes = new Map();
    return {
      children: [],
      dataset: {},
      style: {},
      appendChild(child) { this.children.push(child); return child; },
      setAttribute(name, value) { attributes.set(name, String(value)); }
    };
  }
  global.document = {createElement() { return element(); }};
  global.obListLabel = (_name, fallback) => fallback;
  config = {canDelete: false};
  const built = makeTreeRow({
    id: '44444444-4444-4444-4444-444444444444',
    cells: [],
    open_url: '/ui/catalog/project/44444444-4444-4444-4444-444444444444'
  });
  assert.equal(built.dataset.obEntityId, '44444444-4444-4444-4444-444444444444');
});

test('menu renderer exposes submenu children and invokes the selected command', () => {
  function element() {
    return {
      children: [],
      style: {},
      removed: false,
      appendChild(child) { this.children.push(child); return child; },
      remove() { this.removed = true; }
    };
  }
  let root = null;
  global.document = {
    getElementById() { return null; },
    createElement() { return element(); },
    body: {appendChild(node) { root = node; }},
    addEventListener() {}
  };
  vm.runInThisContext(source.slice(menuStart, menuEnd), {filename: 'ui-list-based-on-menu.js'});

  let calls = 0;
  showListMenu([{label: 'Ввести на основании', items: [{label: 'Счёт', fn() { calls++; }}]}], 10, 20);
  assert.ok(root);
  const parent = root.children[0];
  const submenu = parent.children[0];
  assert.equal(submenu.style.cssText.includes('display:none'), true);
  parent.onmouseenter();
  assert.equal(submenu.style.display, 'block');
  submenu.children[0].onclick({preventDefault() {}});
  assert.equal(calls, 1);
  assert.equal(root.removed, true);
});

test('toolbar and context menu share listMenuItems as their command source', () => {
  assert.match(source, /showListMenu\(listMenuItems\(tr\), e\.clientX, e\.clientY\)/);
  assert.match(source, /showListMenu\(sel \? listMenuItems\(sel\) : listMenuNoSel\(\), r\.left, r\.bottom\)/);
});
