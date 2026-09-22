const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');

const source = fs.readFileSync('static/ui.js', 'utf8');
const managedSource = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('function openRefCreate(');
const end = source.indexOf('// onebaseDevice', start);
if (start < 0 || end < 0) throw new Error('ref-create modal slice not found');
const modalSource = source.slice(start, end);
const bridgeMarker = '// Same-origin request/decision protocol shared by shell tabs and reference';
const bridgeStart = source.indexOf(bridgeMarker);
const bridgeEnd = source.indexOf('if (window.__obEmbedded)', bridgeStart);
if (bridgeStart < 0 || bridgeEnd < 0) throw new Error('form-close bridge slice not found');
const bridgeSource = source.slice(bridgeStart, bridgeEnd);
const managedEscapeComment = managedSource.indexOf('// Esc — отмена незаконченного ввода');
const managedEscapeStart = managedSource.indexOf('  function consumeManagedEscape', managedEscapeComment);
const managedEscapeEnd = managedSource.indexOf('  }, true);', managedEscapeStart);
if (managedEscapeComment < 0 || managedEscapeStart < 0 || managedEscapeEnd < 0) {
  throw new Error('managed Escape handler slice not found');
}
const managedEscapeSource = managedSource.slice(managedEscapeStart, managedEscapeEnd + '  }, true);'.length);
const managedCancelStart = managedSource.indexOf('var obManagedFrameDocumentToken =');
const managedCancelEnd = managedSource.indexOf('\nfunction obManagedInitDelegates(', managedCancelStart);
if (managedCancelStart < 0 || managedCancelEnd < 0) throw new Error('managed popup cancel sender not found');
const managedCancelSource = managedSource.slice(managedCancelStart, managedCancelEnd);

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
        if (event && event.immediatePropagationStopped) break;
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
  if (el.tagName === 'IFRAME') {
    el._posted = [];
    el.contentDocument = eventTarget({
      readyState: 'complete',
      getElementById() { return null; },
    });
    el.contentWindow = {
      document: el.contentDocument,
      postMessage(data, origin) { el._posted.push({data, origin}); },
    };
  }
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
    readyState: 'complete',
    createElement: element,
    getElementById(id) { return walk(body, (candidate) => candidate.id === id); },
    querySelector(selector) { return selector === 'a.btn-cancel' ? parentCancel : null; },
  });
  let finalized = 0;
  let finalizedWhileOpen = false;
  let alerts = 0;
  let uuid = 0;
  const window = eventTarget({
    location: {origin: 'http://onebase.test'},
    crypto: {randomUUID() { uuid++; return `popup-${uuid}`; }},
    confirm() { throw new Error('window.confirm must not be used'); },
    alert() { alerts++; },
    obUIMessage(name, fallback) { return fallback; },
  });
  global.document = document;
  global.window = window;
  if (options.actualBridge) {
    new Function(bridgeSource)();
  } else if (!options.noBridge) {
    window.obRequestFrameClose = async () => ({allowed: true, intentId: 'managed-intent'});
  }
  if (!options.actualBridge && !options.noFinalizer) {
    window.obFinalizeFrameClose = () => {
      finalized++;
      finalizedWhileOpen = !!document.getElementById('_ref-create-modal');
      return true;
    };
  }
  if (options.managedEscape) new Function(managedEscapeSource)();
  const api = new Function(modalSource + '\nreturn {openRefCreate};')();
  return {
    document, window, parentCancel, openRefCreate: api.openRefCreate,
    finalized() { return finalized; }, finalizedWhileOpen() { return finalizedWhileOpen; },
    alerts() { return alerts; },
  };
}

function escapeEvent() {
  return {
    key: 'Escape',
    keyCode: 27,
    preventDefault() { this.defaultPrevented = true; },
    stopPropagation() { this.propagationStopped = true; },
    stopImmediatePropagation() { this.immediatePropagationStopped = true; },
  };
}

function closeConfirmation(document) {
  return document.getElementById('_ref-create-close-confirm');
}

function confirmationButtons(confirm) {
  return confirm.children[0].children[1].children;
}

function installManagedDocument(iframe, token, onFinalize) {
  const document = eventTarget({
    readyState: 'complete',
    getElementById(id) { return id === 'ob-managed-config' ? {} : null; },
  });
  iframe.contentDocument = document;
  iframe.contentWindow.document = document;
  iframe.contentWindow.obFrameCloseDocumentToken = token;
  iframe.contentWindow.obRequestFormClose = function () {};
  iframe.contentWindow.obFinalizeFormClose = function () {
    if (onFinalize) onFinalize();
    return true;
  };
  return document;
}

test('external Cancel and close button use an in-page confirmation', async () => {
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
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(env.document.getElementById('_ref-create-modal'), null);
  assert.equal(closeConfirmation(env.document), null);
  assert.equal(env.finalized(), 1, 'popup was removed without finalizing the child form');
  assert.equal(env.window.listenerCount('message'), 0, 'closed modal leaked its message handler');
});

test('Escape asks before closing from both parent document and same-origin error iframe', async () => {
  const env = setup();
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  env.document.dispatch('keydown', escapeEvent());
  let confirm = closeConfirmation(env.document);
  assert.ok(confirm);
  confirmationButtons(confirm)[0].dispatch('click', {});
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(env.document.getElementById('_ref-create-modal'), null);

  env.openRefCreate(select, 'Город');
  const modal = env.document.getElementById('_ref-create-modal');
  const iframe = modal.children[0].children[1];
  iframe.dispatch('load', {});
  iframe.contentDocument.dispatch('keydown', escapeEvent());
  confirm = closeConfirmation(env.document);
  assert.ok(confirm);
  confirmationButtons(confirm)[0].dispatch('click', {});
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(env.document.getElementById('_ref-create-modal'), null);
});

test('managed capture handler opens confirmation before the parent form', async () => {
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
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(env.document.getElementById('_ref-create-modal'), null);
  assert.equal(env.window.listenerCount('message'), 0, 'managed Escape bypassed modal cleanup');
});

test('Escape consumed by a managed child field does not reach the popup parent listener', () => {
  const env = setup();
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  const modal = env.document.getElementById('_ref-create-modal');
  const iframe = modal.children[0].children[1];
  const childDocument = iframe.contentDocument;
  let blurred = 0;
  childDocument.getElementById = () => null;
  childDocument.querySelector = () => null;
  childDocument.activeElement = {
    tagName: 'INPUT', type: 'text', readOnly: false,
    blur() { blurred++; },
  };
  const childWindow = {_obGrids: {}, _obRefDropdown: null};
  new Function('document', 'window', 'confirm', managedEscapeSource)(childDocument, childWindow, () => true);
  iframe.dispatch('load', {}); // parent listener is registered after the child one

  childDocument.dispatch('keydown', escapeEvent());

  assert.equal(blurred, 1);
  assert.equal(closeConfirmation(env.document), null, 'popup parent also handled the child Escape');
  assert.ok(env.document.getElementById('_ref-create-modal'));
});

test('popup remains open when the close bridge or finalizer is unavailable', async () => {
  for (const options of [{noBridge: true}, {noFinalizer: true}]) {
    const env = setup(options);
    const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
    env.openRefCreate(select, 'Город');
    const modal = env.document.getElementById('_ref-create-modal');
    const cancel = modal.children[0].children[0].children[0];
    cancel.dispatch('click', {});
    confirmationButtons(closeConfirmation(env.document))[0].dispatch('click', {});
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(env.document.getElementById('_ref-create-modal'), modal);
  }
});

test('managed child Cancel sends the allowed intent for parent-side finalization', () => {
  const posts = [];
  const parent = {postMessage(data, origin) { posts.push({data, origin}); }};
  const childWindow = {
    location: {origin: 'http://onebase.test'},
    obFrameCloseDocumentToken: 'document-token-old',
  };
  const send = new Function('parent', 'window', managedCancelSource + '\nreturn obManagedPostRefCancel;')(parent, childWindow);
  childWindow.obFrameCloseDocumentToken = 'document-token-new';
  assert.equal(send({allowed: true, intentId: ''}), false);
  assert.equal(posts.length, 0);
  assert.equal(send({allowed: true, intentId: 'intent-popup'}), true);
  assert.deepEqual(posts, [{
    data: {
      source: 'obRefCancel', allowed: true, intentId: 'intent-popup',
      documentToken: 'document-token-old', error: '',
    },
    origin: 'http://onebase.test',
  }]);
});

test('parent finalizes a managed child Cancel decision immediately before cleanup', () => {
  const env = setup();
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  const modal = env.document.getElementById('_ref-create-modal');
  const iframe = modal.children[0].children[1];
  iframe.contentDocument.getElementById = (id) => id === 'ob-managed-config' ? {} : null;

  env.window.dispatch('message', {
    origin: env.window.location.origin,
    source: iframe.contentWindow,
    data: {source: 'obRefCancel', allowed: true, intentId: 'intent-popup'},
  });

  assert.equal(env.finalized(), 1);
  assert.equal(env.finalizedWhileOpen(), true);
  assert.equal(env.document.getElementById('_ref-create-modal'), null);
});

test('parent keeps a managed popup open for an uncorrelated Cancel or missing finalizer', () => {
  for (const options of [{}, {noFinalizer: true}]) {
    const env = setup(options);
    const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
    env.openRefCreate(select, 'Город');
    const modal = env.document.getElementById('_ref-create-modal');
    const iframe = modal.children[0].children[1];
    iframe.contentDocument.getElementById = (id) => id === 'ob-managed-config' ? {} : null;
    env.window.dispatch('message', {
      origin: env.window.location.origin,
      source: iframe.contentWindow,
      data: {source: 'obRefCancel', allowed: true, intentId: options.noFinalizer ? 'intent-popup' : ''},
    });
    assert.equal(env.document.getElementById('_ref-create-modal'), modal);
    assert.equal(env.alerts(), 1);
  }
});

test('popup keeps the new Document when an old bridge response uses the same WindowProxy', async () => {
  const env = setup({actualBridge: true});
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  const modal = env.document.getElementById('_ref-create-modal');
  const iframe = modal.children[0].children[1];
  const sourceWindowProxy = iframe.contentWindow;
  let finalizedNewDocument = 0;
  installManagedDocument(iframe, 'popup-old-document');

  modal.children[0].children[0].children[0].dispatch('click', {});
  confirmationButtons(closeConfirmation(env.document))[0].dispatch('click', {});
  assert.equal(iframe._posted.length, 1);
  const correlation = iframe._posted[0].data.correlation;

  installManagedDocument(iframe, 'popup-new-document', () => { finalizedNewDocument++; });
  iframe.dispatch('load', {});
  assert.equal(iframe.contentWindow, sourceWindowProxy, 'test replaced WindowProxy');
  env.window.dispatch('message', {
    origin: env.window.location.origin,
    source: sourceWindowProxy,
    data: {source: 'obFormCloseDecision', correlation, allowed: true, intentId: 'stale-popup'},
  });
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(env.document.getElementById('_ref-create-modal'), modal);
  assert.equal(finalizedNewDocument, 0);
});

test('popup rejects stale child Cancel after same-WindowProxy navigation', () => {
  const env = setup({actualBridge: true});
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  const modal = env.document.getElementById('_ref-create-modal');
  const iframe = modal.children[0].children[1];
  const sourceWindowProxy = iframe.contentWindow;
  let finalizedNewDocument = 0;
  installManagedDocument(iframe, 'popup-old-document');
  installManagedDocument(iframe, 'popup-new-document', () => { finalizedNewDocument++; });
  iframe.dispatch('load', {});

  env.window.dispatch('message', {
    origin: env.window.location.origin,
    source: sourceWindowProxy,
    data: {
      source: 'obRefCancel', allowed: true, intentId: 'stale-popup',
      documentToken: 'popup-old-document',
    },
  });
  assert.equal(env.document.getElementById('_ref-create-modal'), modal);
  assert.equal(finalizedNewDocument, 0);

  env.window.dispatch('message', {
    origin: env.window.location.origin,
    source: sourceWindowProxy,
    data: {
      source: 'obRefCancel', allowed: true, intentId: 'current-popup',
      documentToken: 'popup-new-document',
    },
  });
  assert.equal(finalizedNewDocument, 1);
  assert.equal(env.document.getElementById('_ref-create-modal'), null);
});

test('reopening keeps the existing popup instead of bypassing its close intent', () => {
  const env = setup();
  const select = {options: [], value: '', appendChild() {}, dispatchEvent() {}};
  env.openRefCreate(select, 'Город');
  const first = env.document.getElementById('_ref-create-modal');
  first._obClose();
  assert.ok(closeConfirmation(env.document));
  env.openRefCreate(select, 'Город');
  const second = env.document.getElementById('_ref-create-modal');
  assert.equal(second, first);
  assert.notEqual(first.parentElement, null);
  assert.notEqual(closeConfirmation(env.document), null);
  assert.equal(env.window.listenerCount('message'), 1);
});
