const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');

const source = fs.readFileSync('static/ui.js', 'utf8');
const start = source.indexOf('function openRefCreate(');
const end = source.indexOf('// onebaseDevice', start);
if (start < 0 || end < 0) throw new Error('ref-create modal slice not found');
const modalSource = source.slice(start, end);

function eventTarget(base) {
  const listeners = Object.create(null);
  return Object.assign(base || {}, {
    addEventListener(type, fn) { (listeners[type] || (listeners[type] = [])).push(fn); },
    removeEventListener(type, fn) {
      listeners[type] = (listeners[type] || []).filter((candidate) => candidate !== fn);
    },
    dispatch(type, event) { (listeners[type] || []).slice().forEach((fn) => fn(event)); },
    listenerCount(type) { return (listeners[type] || []).length; },
  });
}

function element(tag) {
  const attrs = Object.create(null);
  const el = eventTarget({
    tagName: String(tag).toUpperCase(),
    id: '',
    type: '',
    style: {},
    children: [],
    parentElement: null,
    textContent: '',
    contentDocument: null,
    setAttribute(name, value) { attrs[name] = String(value); },
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    appendChild(child) { this.children.push(child); child.parentElement = this; return child; },
    removeChild(child) { this.children = this.children.filter((candidate) => candidate !== child); child.parentElement = null; },
    remove() { if (this.parentElement) this.parentElement.removeChild(this); },
  });
  if (el.tagName === 'IFRAME') el.contentDocument = eventTarget({});
  return el;
}

function walk(root, predicate) {
  if (!root) return null;
  if (predicate(root)) return root;
  for (const child of root.children || []) {
    const found = walk(child, predicate);
    if (found) return found;
  }
  return null;
}

function setup() {
  const body = element('body');
  const document = eventTarget({
    body,
    createElement: element,
    getElementById(id) { return walk(body, (candidate) => candidate.id === id); },
  });
  const window = eventTarget({confirm() { return true; }});
  global.document = document;
  global.window = window;
  const api = new Function(modalSource + '\nreturn {openRefCreate};')();
  return {document, window, openRefCreate: api.openRefCreate};
}

function escapeEvent() {
  return {key: 'Escape', keyCode: 27, preventDefault() {}, stopPropagation() {}};
}

test('external Cancel and close button safely release the modal', () => {
  const env = setup();
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  const modal = env.document.getElementById('_ref-create-modal');
  assert.ok(modal);
  const toolbar = modal.children[0].children[0];
  const cancel = toolbar.children[0];
  const close = toolbar.children[1];
  assert.equal(cancel.textContent, 'Отмена');
  assert.equal(close.textContent, '×');

  env.window.confirm = () => false;
  cancel.dispatch('click', {});
  assert.equal(env.document.getElementById('_ref-create-modal'), modal, 'cancel bypassed the unsaved-data confirmation');

  env.window.confirm = () => true;
  close.dispatch('click', {});
  assert.equal(env.document.getElementById('_ref-create-modal'), null);
  assert.equal(env.window.listenerCount('message'), 0, 'closed modal leaked its message handler');
});

test('Escape closes from both parent document and same-origin error iframe', () => {
  const env = setup();
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  env.document.dispatch('keydown', escapeEvent());
  assert.equal(env.document.getElementById('_ref-create-modal'), null);

  env.openRefCreate(select, 'Город');
  const modal = env.document.getElementById('_ref-create-modal');
  const iframe = modal.children[0].children[1];
  iframe.dispatch('load', {});
  iframe.contentDocument.dispatch('keydown', escapeEvent());
  assert.equal(env.document.getElementById('_ref-create-modal'), null);
});

test('reopening cleans the previous modal before installing a new handler', () => {
  const env = setup();
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  const first = env.document.getElementById('_ref-create-modal');
  env.openRefCreate(select, 'Город');
  const second = env.document.getElementById('_ref-create-modal');
  assert.notEqual(second, first);
  assert.equal(first.parentElement, null);
  assert.equal(env.window.listenerCount('message'), 1);
});
