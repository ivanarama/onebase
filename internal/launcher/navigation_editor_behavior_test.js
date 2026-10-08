const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const test = require('node:test');
const crypto = require('node:crypto');
const html = fs.readFileSync(process.env.ONEBASE_NAVIGATION_EDITOR_HTML, 'utf8');
const bootstrap = html.match(/<script>(window\.OB_MENU_EDITOR=[\s\S]*?)<\/script>/)[1];
const emptyHTML = fs.readFileSync(process.env.ONEBASE_NAVIGATION_EDITOR_EMPTY_HTML, 'utf8');
const emptyBootstrap = emptyHTML.match(/<script>(window\.OB_MENU_EDITOR=[\s\S]*?)<\/script>/)[1];
const source = fs.readFileSync('static/navigation-editor.js', 'utf8');

function harness(pageBootstrap = bootstrap) {
  const elements = new Map(), calls = [], pending = [], events = new Map();
  const document = {readyState: 'complete', activeElement: null};
  function element(tag = 'div') {
    const listeners = new Map();
    const node = {
      tagName: tag, children: [], dataset: {}, value: '', textContent: '', className: '', disabled: false,
      classList: {add() {}, remove() {}},
      replaceChildren() { this.children = []; },
      appendChild(child) { this.children.push(child); child.parentElement = this; return child; },
      setAttribute(name, value) { this[name] = value; },
      focus() { document.activeElement = this; },
      querySelector(selector) {
        const action = selector.match(/data-action="([^"]+)"/);
        return this.children.find(child => action ? child.dataset.action === action[1] : child.tagName === 'button');
      },
      addEventListener(name, listener) { listeners.set(name, listener); },
      fire(name, values = {}) { return listeners.get(name)?.({preventDefault() {}, ...values}); },
      click() { if (!this.disabled) return this.fire('click'); },
    };
    Object.defineProperty(node, 'innerHTML', {set() { throw new Error('untrusted markup must stay text'); }});
    return node;
  }
  document.createElement = element;
  document.createElementNS = (_, tag) => element(tag);
  document.getElementById = id => elements.get(id);
  for (const id of ['editor', 'tree', 'properties', 'palette', 'preview', 'status', 'live', 'save', 'add-section', 'add-group', 'import-legacy', 'import-tree', 'filter', 'lang']) elements.set('menu-' + id, element());
  const window = {
    crypto, confirm: () => true,
    addEventListener(name, listener) { events.set(name, listener); },
    fetch(url, options) {
      calls.push({url, options});
      if (pending.length) return pending.shift()(url, options);
      const body = {palette: window.OB_MENU_EDITOR.palette, preview: [{id: 'cfg:other', title: 'Other', items: [{id: 'safe', label: '<script>alert(1)</script>'}]}]};
      return Promise.resolve(response(body));
    },
  };
  const context = vm.createContext({window, document, URLSearchParams, console});
  vm.runInContext(pageBootstrap, context, {filename: 'production-bootstrap.js'});
  vm.runInContext(source, context, {filename: 'production-navigation-editor.js'});
  const get = id => elements.get('menu-' + id);
  const row = id => get('tree').children.find(node => node.dataset.id === id);
  return {window, document, calls, pending, events, get, row};
}
function response(body, ok = true, type = 'application/json') {
  return {ok, headers: {get: () => type}, json: () => Promise.resolve(body)};
}
async function settle() { await new Promise(resolve => setImmediate(resolve)); }
function transferred(id) { return {setData() {}, getData() { return id; }}; }

test('pointer reorders production nodes; Alt keyboard moves into/out of a folder and preserves focus', async () => {
  const h = harness(), menu = h.window.OB_MENU_EDITOR.menu;
  h.row('a').fire('dragstart', {dataTransfer: transferred('a')});
  h.row('order').fire('dragover');
  h.row('a').fire('drop', {dataTransfer: transferred('order')});
  assert.deepEqual(Array.from(menu.sections[0].items, node => node.id), ['order', 'a']);
  assert.equal(h.get('tree').children[1].dataset.id, 'order');
  assert.equal(h.window.OB_MENU_EDITOR.menu.sections[0].items[1].target, 'catalog:A');
  h.row('a').fire('keydown', {altKey: true, key: 'ArrowUp'});
  assert.deepEqual(Array.from(menu.sections[0].items, node => node.id), ['a', 'order']);
  assert.equal(h.get('tree').children[1].dataset.id, 'a');
  assert.equal(h.document.activeElement, h.row('a').querySelector('[data-action="select"]'));
  h.row('a').fire('keydown', {altKey: true, key: 'ArrowRight'});
  assert.deepEqual(Array.from(menu.sections[0].groups[0].items, node => node.id), ['b', 'a']);
  h.row('a').fire('keydown', {altKey: true, key: 'ArrowLeft'});
  assert.deepEqual(Array.from(menu.sections[0].items, node => node.id), ['order', 'a']);
  assert.equal(h.row('a').querySelector('[data-action="out"]').disabled, true);
  await settle();
  assert(h.calls.every(call => call.url.endsWith('/preview')));
  assert.equal(h.get('preview').children[0].dataset.id, 'cfg:other');
  assert.equal(h.get('preview').children[0].children[1].children[0].textContent, '<script>alert(1)</script>');
});

test('palette, rename, icon and explicit Save submit the edited tree with unchanged occurrence IDs', async () => {
  const h = harness();
  h.row('year').querySelector('button').click();
  h.get('palette').children[0].children[1].click();
  const node = h.window.OB_MENU_EDITOR.menu.sections[0].groups[0].items.at(-1);
  assert.match(node.id, /^i-[a-z0-9-]+$/);
  assert.equal(node.target, h.window.OB_MENU_EDITOR.palette[0].target);
  h.row('education').querySelector('button').click();
  const fields = h.get('properties').children;
  fields[0].children[0].value = '<b>Renamed</b>'; fields[0].children[0].fire('change');
  h.get('properties').children[1].children[0].value = 'school'; h.get('properties').children[1].children[0].fire('change');
  assert.equal(h.window.OB_MENU_EDITOR.menu.sections[0].titles.en, 'English education');
  await settle();
  assert.equal(h.calls.filter(call => call.url.endsWith('/save')).length, 0);
  await h.get('save').click();
  const body = JSON.parse(h.calls.at(-1).options.body);
  assert.equal(body.subsystem, 'School');
  assert.equal(body.menu.sections[0].id, 'education');
  assert.equal(body.menu.sections[0].title, '<b>Renamed</b>');
  assert.equal(body.menu.sections[0].icon, 'school');
  assert.equal(h.get('status').textContent, 'Menu saved');
  let prevented = false;
  h.events.get('beforeunload')({preventDefault() { prevented = true; }});
  assert.equal(prevented, false);
});

test('import is an explicit read-only draft; only Save writes and malformed responses retain dirty state', async () => {
  const h = harness();
  h.pending.push(() => Promise.resolve(response({menu: {sections: [{id: 'imported', title: 'Imported'}]}, palette: []})));
  h.get('import-tree').click(); await settle();
  assert(h.calls[0].url.includes('import=tree-order'));
  assert.equal(h.calls[0].options.method, undefined);
  assert.equal(h.calls.filter(call => call.url.endsWith('/save')).length, 0);
  assert(h.row('imported'));
  h.pending.push(() => Promise.resolve(response({}, true, 'text/html')));
  await h.get('save').click();
  assert(h.get('status').textContent.includes('Unexpected server response'));
  let prevented = false;
  h.events.get('beforeunload')({preventDefault() { prevented = true; }});
  assert.equal(prevented, true);
  assert.equal(h.get('save').disabled, false);
});

test('a delayed preview cannot overwrite a newer edit or language preview', async () => {
  const h = harness(); let resolve;
  h.pending.push(() => new Promise(done => { resolve = done; }));
  h.row('a').fire('keydown', {altKey: true, key: 'ArrowDown'});
  h.get('lang').value = 'ru'; h.get('lang').fire('change'); await settle();
  resolve(response({preview: [{id: 'stale', title: 'Stale'}]})); await settle();
  assert.equal(h.get('preview').children[0].dataset.id, 'cfg:other');
  assert.equal(JSON.parse(h.calls.at(-1).options.body).lang, 'ru');
});

test('icon preview uses the shipped sprite and whitelist; edits during Save stay dirty and cannot double-submit', async () => {
  const h = harness();
  h.pending.push(() => Promise.resolve(response({preview: [{id: 'icons', title: 'Icons', icon: 'home', items: [{id: 'bad', label: 'Safe', icon: 'https://evil/unsafe.svg'}]}]})));
  h.get('lang').fire('change'); await settle();
  const block = h.get('preview').children[0];
  assert(block.children[0].children[0].children[0].href.endsWith('#house'));
  assert(block.children[1].children[0].children[0].children[0].href.endsWith('#square'));
  let resolve;
  h.pending.push(() => new Promise(done => { resolve = done; }));
  const saving = h.get('save').click();
  h.get('add-section').click();
  assert.equal(h.get('save').disabled, true);
  h.get('save').click();
  assert.equal(h.calls.filter(call => call.url.endsWith('/save')).length, 1);
  resolve(response({preview: []})); await saving; await settle();
  let prevented = false;
  h.events.get('beforeunload')({preventDefault() { prevented = true; }});
  assert.equal(prevented, true);
  assert.equal(h.get('save').disabled, false);
});

test('a valid empty YAML menu can add its first section and save', async () => {
  const h = harness(emptyBootstrap), menu = h.window.OB_MENU_EDITOR.menu;
  assert(menu);
  assert.equal(menu.sections, null);
  assert.equal(h.get('tree').children.length, 0);
  h.get('add-section').click();
  assert.equal(menu.sections.length, 1);
  const section = menu.sections[0];
  assert.match(section.id, /^s-[a-z0-9-]+$/);
  assert.equal(section.title, 'New section');
  assert(h.row(section.id));
  assert.equal(h.document.activeElement, h.row(section.id).querySelector('button'));
  await settle();
  await h.get('save').click();
  const body = JSON.parse(h.calls.at(-1).options.body);
  assert.equal(body.menu.sections.length, 1);
  assert.equal(body.menu.sections[0].id, section.id);
  assert.equal(h.get('status').textContent, 'Menu saved');
});

test('self-drop of items, folders and sections leaves order and clean state unchanged', async () => {
  const h = harness();
  h.row('education').querySelector('button').click();
  h.get('add-group').click();
  h.get('add-section').click();
  await settle();
  await h.get('save').click();
  const before = JSON.stringify(h.window.OB_MENU_EDITOR.menu);
  const ids = h.get('tree').children.map(row => row.dataset.id);
  const calls = h.calls.length;
  for (const id of ids) {
    const row = h.row(id);
    row.fire('dragstart', {dataTransfer: transferred(id)});
    row.fire('drop', {dataTransfer: transferred(id)});
    assert.equal(JSON.stringify(h.window.OB_MENU_EDITOR.menu), before, id);
    assert.deepEqual(h.get('tree').children.map(row => row.dataset.id), ids, id);
    assert.equal(h.get('status').textContent, 'Menu saved', id);
    let prevented = false;
    h.events.get('beforeunload')({preventDefault() { prevented = true; }});
    assert.equal(prevented, false, id);
  }
  await settle();
  assert.equal(h.calls.length, calls);
});
