'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const htmlPath = process.env.ONEBASE_TABS_HTML;
assert.ok(htmlPath, 'ONEBASE_TABS_HTML must point to the rendered app shell');
const html = fs.readFileSync(htmlPath, 'utf8');
const source = Array.from(html.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/gi), match => match[1])
  .find(script => script.includes("var STORE='obTabs'"));
assert.ok(source, 'rendered app shell must contain the tabs runtime');

class ClassList {
  constructor(element) {
    this.element = element;
    this.names = new Set();
  }

  reset(value) {
    this.names = new Set(String(value || '').split(/\s+/).filter(Boolean));
  }

  toggle(name, force) {
    const enabled = force === undefined ? !this.names.has(name) : Boolean(force);
    if (enabled) this.names.add(name);
    else this.names.delete(name);
    this.element._className = Array.from(this.names).join(' ');
    return enabled;
  }

  contains(name) {
    return this.names.has(name);
  }
}

class Element {
  constructor(tagName, id = '') {
    this.tagName = String(tagName).toUpperCase();
    this.id = id;
    this.children = [];
    this.parentElement = null;
    this.style = {};
    this.handlers = new Map();
    this.attributes = new Map();
    this._className = '';
    this.classList = new ClassList(this);
    this.textContent = '';
    this.title = '';
    this.src = '';
    if (this.tagName === 'IFRAME') this.contentWindow = {};
  }

  set className(value) {
    this._className = String(value);
    this.classList.reset(value);
  }

  get className() {
    return this._className;
  }

  appendChild(child) {
    child.parentElement = this;
    this.children.push(child);
    return child;
  }

  remove() {
    if (!this.parentElement) return;
    const index = this.parentElement.children.indexOf(this);
    if (index >= 0) this.parentElement.children.splice(index, 1);
    this.parentElement = null;
  }

  addEventListener(type, listener) {
    if (!this.handlers.has(type)) this.handlers.set(type, []);
    this.handlers.get(type).push(listener);
  }

  dispatch(type, values = {}) {
    const event = Object.assign({
      target: this,
      button: 0,
      defaultPrevented: false,
      preventDefault() { this.defaultPrevented = true; },
      stopPropagation() {}
    }, values);
    for (const listener of this.handlers.get(type) || []) listener(event);
    return event;
  }

  setAttribute(name, value) {
    this.attributes.set(String(name), String(value));
  }

  getAttribute(name) {
    return this.attributes.has(String(name)) ? this.attributes.get(String(name)) : null;
  }

  scrollIntoView() {}
}

class FakeStorage {
  constructor(entries = {}, faults = {}) {
    this.values = new Map(Object.entries(entries).map(([key, value]) => [key, String(value)]));
    this.faults = faults;
  }

  getItem(key) {
    if (this.faults.get) throw new Error('sessionStorage get blocked');
    return this.values.has(key) ? this.values.get(key) : null;
  }

  setItem(key, value) {
    if (this.faults.set) throw new Error('sessionStorage set blocked');
    this.values.set(String(key), String(value));
  }

  removeItem(key) {
    if (this.faults.remove) throw new Error('sessionStorage remove blocked');
    this.values.delete(String(key));
  }
}

function shell(storage, search = '') {
  const elements = {};
  for (const id of ['ob-tabstrip', 'ob-tabbody', 'ob-tabempty', 'ob-tabhome']) {
    elements[id] = new Element('div', id);
  }
  const documentListeners = new Map();
  const windowListeners = new Map();
  const document = {
    body: new Element('body'),
    getElementById(id) { return elements[id] || null; },
    createElement(tagName) { return new Element(tagName); },
    addEventListener(type, listener) {
      if (!documentListeners.has(type)) documentListeners.set(type, []);
      documentListeners.get(type).push(listener);
    }
  };
  let uuid = 0;
  const context = {
    document,
    sessionStorage: storage,
    location: {search, origin: 'http://127.0.0.1:8080', href: '/ui/app'},
    URL,
    URLSearchParams,
    crypto: {
      randomUUID() {
        uuid++;
        return '00000000-0000-4000-8000-' + String(uuid).padStart(12, '0');
      }
    },
    setTimeout() { return 1; },
    confirm() { return true; },
    addEventListener(type, listener) {
      if (!windowListeners.has(type)) windowListeners.set(type, []);
      windowListeners.get(type).push(listener);
    }
  };
  context.window = context;
  vm.runInNewContext(source, context, {filename: 'rendered-tabs-runtime.js'});

  const strip = elements['ob-tabstrip'];
  return {
    open(url, title, options) { return context.obOpenTab(url, title, options); },
    count() { return strip.children.length; },
    activeIndex() { return strip.children.findIndex(button => button.classList.contains('active')); },
    titles() { return strip.children.map(button => button.title); },
    click(index) { strip.children[index].dispatch('click'); },
    close(index) { strip.children[index].children[2].dispatch('click'); }
  };
}

function savedTabs(storage) {
  return JSON.parse(storage.getItem('obTabs'));
}

function savedActive(storage) {
  const raw = storage.getItem('obTabsActive');
  return raw ? JSON.parse(raw) : null;
}

function openThree(storage) {
  const app = shell(storage);
  app.open('/ui/a', 'A');
  app.open('/ui/b', 'B');
  app.open('/ui/c', 'C');
  return app;
}

test('F5 restores the selected middle tab without hydration overwriting it', () => {
  const storage = new FakeStorage();
  let app = openThree(storage);
  app.click(1);
  const before = savedActive(storage);
  assert.equal(before.url, '/ui/b');
  assert.equal(app.activeIndex(), 1);

  app = shell(storage);
  assert.equal(app.count(), 3);
  assert.equal(app.activeIndex(), 1);
  assert.deepEqual(savedActive(storage), before);
});

test('duplicate URLs keep separate stable IDs and restore the active copy', () => {
  const storage = new FakeStorage();
  let app = shell(storage);
  app.open('/ui/document/new', 'first');
  app.open('/ui/document/new', 'second', {allowDup: true});

  const beforeTabs = savedTabs(storage);
  const beforeActive = savedActive(storage);
  assert.equal(app.count(), 2);
  assert.equal(app.activeIndex(), 1);
  assert.equal(new Set(beforeTabs.map(tab => tab.id)).size, 2);
  assert.equal(beforeActive.id, beforeTabs[1].id);

  app = shell(storage);
  assert.equal(app.count(), 2);
  assert.equal(app.activeIndex(), 1);
  assert.deepEqual(app.titles(), ['first', 'second']);
  assert.deepEqual(savedTabs(storage).map(tab => tab.id), beforeTabs.map(tab => tab.id));
  assert.deepEqual(savedActive(storage), beforeActive);
});

test('home view preserves the previous active tab across navigation', () => {
  const storage = new FakeStorage();
  let app = openThree(storage);
  app.click(1);
  const before = storage.getItem('obTabsActive');

  app = shell(storage, '?home=1');
  assert.equal(app.activeIndex(), -1);
  assert.equal(storage.getItem('obTabsActive'), before);

  app = shell(storage);
  assert.equal(app.activeIndex(), 1);
  assert.equal(storage.getItem('obTabsActive'), before);
});

test('missing active ID falls back to the first tab', () => {
  const storage = new FakeStorage({
    obTabs: JSON.stringify([
      {id: 'tab:a', url: '/ui/a', title: 'A'},
      {id: 'tab:b', url: '/ui/b', title: 'B'},
      {id: 'tab:c', url: '/ui/c', title: 'C'}
    ]),
    // The stale new-format ID is authoritative. Its URL deliberately matches
    // the second tab and must not select that different instance.
    obTabsActive: JSON.stringify({id: 'tab:closed', url: '/ui/b'})
  });

  const app = shell(storage);
  assert.equal(app.activeIndex(), 0);
  assert.deepEqual(savedActive(storage), {id: 'tab:a', url: '/ui/a'});
});

test('legacy URL-only state is upgraded and remains restorable', () => {
  const storage = new FakeStorage({
    obTabs: JSON.stringify([
      {url: '/ui/a', title: 'A'},
      {url: '/ui/b', title: 'B'},
      {url: '/ui/c', title: 'C'}
    ]),
    obTabsActive: '/ui/b'
  });

  let app = shell(storage);
  assert.equal(app.activeIndex(), 1);
  const upgraded = savedTabs(storage);
  assert.equal(upgraded.every(tab => typeof tab.id === 'string' && tab.id.length > 0), true);
  assert.equal(new Set(upgraded.map(tab => tab.id)).size, 3);
  assert.equal(savedActive(storage).id, upgraded[1].id);
  assert.equal(savedActive(storage).url, '/ui/b');

  app = shell(storage);
  assert.equal(app.activeIndex(), 1);
  assert.deepEqual(savedTabs(storage).map(tab => tab.id), upgraded.map(tab => tab.id));
});

test('closing the active tab persists its neighbor and closing the last clears active state', () => {
  const storage = new FakeStorage();
  let app = openThree(storage);
  app.click(1);
  app.close(1);
  assert.equal(app.count(), 2);
  assert.equal(app.activeIndex(), 1);
  assert.equal(savedActive(storage).url, '/ui/c');

  app = shell(storage);
  assert.equal(app.count(), 2);
  assert.equal(app.activeIndex(), 1);
  app.close(1);
  app.close(0);
  assert.equal(app.count(), 0);
  assert.equal(storage.getItem('obTabsActive'), null);
});

test('duplicate or corrupt stored IDs cannot collapse tabs or crash startup', () => {
  const duplicateIDs = new FakeStorage({
    obTabs: JSON.stringify([
      {id: 'tab:same', url: '/ui/a', title: 'A'},
      {id: 'tab:same', url: '/ui/a', title: 'A copy'}
    ]),
    obTabsActive: JSON.stringify({id: 'tab:same', url: '/ui/a'})
  });
  const duplicates = shell(duplicateIDs);
  assert.equal(duplicates.count(), 2);
  assert.equal(duplicates.activeIndex(), 0);
  assert.equal(new Set(savedTabs(duplicateIDs).map(tab => tab.id)).size, 2);

  const malformed = new FakeStorage({obTabs: '{', obTabsActive: 'not-a-tab'});
  assert.doesNotThrow(() => shell(malformed));
  assert.equal(shell(malformed).count(), 0);

  const unavailable = new FakeStorage({}, {get: true, set: true, remove: true});
  assert.doesNotThrow(() => shell(unavailable));
});

test('independent sessionStorage areas do not leak tab state', () => {
  const firstBase = new FakeStorage();
  const secondBase = new FakeStorage();
  const first = shell(firstBase);
  const second = shell(secondBase);
  first.open('/ui/base-one', 'One');
  second.open('/ui/base-two', 'Two');

  assert.equal(savedActive(firstBase).url, '/ui/base-one');
  assert.equal(savedActive(secondBase).url, '/ui/base-two');
  assert.equal(shell(firstBase).titles()[0], 'One');
  assert.equal(shell(secondBase).titles()[0], 'Two');
});
