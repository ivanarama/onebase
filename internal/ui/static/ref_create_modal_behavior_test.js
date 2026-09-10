const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');

const source = fs.readFileSync('static/ui.js', 'utf8');
const managedSource = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('function openRefCreate(');
const end = source.indexOf('// onebaseDevice', start);
if (start < 0 || end < 0) throw new Error('ref-create modal slice not found');
const modalSource = source.slice(start, end);
const managedEscapeComment = managedSource.indexOf('// Esc — отмена незаконченного ввода');
const managedEscapeStart = managedSource.indexOf("  document.addEventListener('keydown'", managedEscapeComment);
const managedEscapeEnd = managedSource.indexOf('  }, true);', managedEscapeStart);
if (managedEscapeComment < 0 || managedEscapeStart < 0 || managedEscapeEnd < 0) {
  throw new Error('managed Escape handler slice not found');
}
const managedEscapeSource = managedSource.slice(managedEscapeStart, managedEscapeEnd + '  }, true);'.length);

function eventTarget(base) {
  const listeners = Object.create(null);
  return Object.assign(base || {}, {
    addEventListener(type, fn) { (listeners[type] || (listeners[type] = [])).push(fn); },
    removeEventListener(type, fn) {
      listeners[type] = (listeners[type] || []).filter((candidate) => candidate !== fn);
    },
    dispatch(type, event) {
      for (const fn of (listeners[type] || []).slice()) {
        fn(event);
        if (event && event.propagationStopped) break;
      }
    },
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

function setup(options) {
  options = options || {};
  const body = element('body');
  const parentCancel = {clicks: 0, click() { this.clicks++; }};
  const document = eventTarget({
    body,
    createElement: element,
    getElementById(id) { return walk(body, (candidate) => candidate.id === id); },
    querySelector(selector) { return selector === 'a.btn-cancel' ? parentCancel : null; },
  });
  const window = eventTarget({confirm() { throw new Error('window.confirm must not be used'); }});
  global.document = document;
  global.window = window;
  if (options.managedEscape) new Function(managedEscapeSource)();
  const api = new Function(modalSource + '\nreturn {openRefCreate};')();
  return {document, window, parentCancel, openRefCreate: api.openRefCreate};
}

function escapeEvent() {
  return {
    key: 'Escape',
    keyCode: 27,
    preventDefault() { this.defaultPrevented = true; },
    stopPropagation() { this.propagationStopped = true; },
  };
}

function closeConfirmation(document) {
  return document.getElementById('_ref-create-close-confirm');
}

function confirmationButtons(confirm) {
  return confirm.children[0].children[1].children;
}

test('external Cancel and close button use an in-page confirmation', () => {
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

  cancel.dispatch('click', {});
  let confirm = closeConfirmation(env.document);
  assert.ok(confirm, 'external Cancel did not open the in-page confirmation');
  assert.equal(env.document.getElementById('_ref-create-modal'), modal);
  assert.equal(confirm.children[0].children[0].textContent, 'Данные были изменены и не записаны. Закрыть форму?');

  let [closeWithoutSave, stay] = confirmationButtons(confirm);
  assert.equal(closeWithoutSave.textContent, 'Закрыть');
  assert.equal(stay.textContent, 'Отмена');
  stay.dispatch('click', {});
  assert.equal(closeConfirmation(env.document), null);
  assert.equal(env.document.getElementById('_ref-create-modal'), modal, 'confirmation cancellation closed the create form');

  close.dispatch('click', {});
  confirm = closeConfirmation(env.document);
  assert.ok(confirm, 'close button bypassed the in-page confirmation');
  [closeWithoutSave] = confirmationButtons(confirm);
  closeWithoutSave.dispatch('click', {});
  assert.equal(env.document.getElementById('_ref-create-modal'), null);
  assert.equal(closeConfirmation(env.document), null);
  assert.equal(env.window.listenerCount('message'), 0, 'closed modal leaked its message handler');
});

test('Escape asks before closing from both parent document and same-origin error iframe', () => {
  const env = setup();
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  env.document.dispatch('keydown', escapeEvent());
  let confirm = closeConfirmation(env.document);
  assert.ok(confirm);
  confirmationButtons(confirm)[0].dispatch('click', {});
  assert.equal(env.document.getElementById('_ref-create-modal'), null);

  env.openRefCreate(select, 'Город');
  const modal = env.document.getElementById('_ref-create-modal');
  const iframe = modal.children[0].children[1];
  iframe.dispatch('load', {});
  iframe.contentDocument.dispatch('keydown', escapeEvent());
  confirm = closeConfirmation(env.document);
  assert.ok(confirm);
  confirmationButtons(confirm)[0].dispatch('click', {});
  assert.equal(env.document.getElementById('_ref-create-modal'), null);
});

test('managed capture handler opens confirmation before the parent form', () => {
  const env = setup({managedEscape: true});
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');

  env.document.dispatch('keydown', escapeEvent());

  assert.ok(closeConfirmation(env.document));
  assert.ok(env.document.getElementById('_ref-create-modal'));
  assert.equal(env.parentCancel.clicks, 0, 'Escape closed the managed parent form');
  env.document.dispatch('keydown', escapeEvent());
  assert.equal(closeConfirmation(env.document), null, 'second Escape did not cancel the confirmation');
  assert.ok(env.document.getElementById('_ref-create-modal'), 'cancelling the confirmation closed the create form');
  env.document.dispatch('keydown', escapeEvent());
  confirmationButtons(closeConfirmation(env.document))[0].dispatch('click', {});
  assert.equal(env.document.getElementById('_ref-create-modal'), null);
  assert.equal(env.window.listenerCount('message'), 0, 'managed Escape bypassed modal cleanup');
});

test('reopening cleans the previous modal before installing a new handler', () => {
  const env = setup();
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  const first = env.document.getElementById('_ref-create-modal');
  first._obClose();
  assert.ok(closeConfirmation(env.document));
  env.openRefCreate(select, 'Город');
  const second = env.document.getElementById('_ref-create-modal');
  assert.notEqual(second, first);
  assert.equal(first.parentElement, null);
  assert.equal(closeConfirmation(env.document), null, 'reopening leaked the old confirmation');
  assert.equal(env.window.listenerCount('message'), 1);
});
