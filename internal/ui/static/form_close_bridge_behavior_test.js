'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const ui = fs.readFileSync('static/ui.js', 'utf8');
const marker = '// Same-origin request/decision protocol shared by shell tabs and reference';
const start = ui.indexOf(marker);
const end = ui.indexOf('if (window.__obEmbedded)', start);
assert.ok(start >= 0 && end > start, 'form close bridge slice not found');
const bridge = ui.slice(start, end);
const shellCloseStart = ui.indexOf('  window.obCloseInShell = function (reason) {', end);
const shellCloseEnd = ui.indexOf("  document.addEventListener('click'", shellCloseStart);
assert.ok(shellCloseStart >= 0 && shellCloseEnd > shellCloseStart, 'embedded shell close slice not found');
const shellClose = ui.slice(shellCloseStart, shellCloseEnd);

function runtime(managed = false, readyState = 'complete') {
  const listeners = [];
  const documentListeners = new Map();
  let uuid = 0;
  let managedPage = managed;
  const document = {
    readyState,
    getElementById(id) { return managedPage && id === 'ob-managed-config' ? {} : null; },
    addEventListener(type, fn, options) {
      if (!documentListeners.has(type)) documentListeners.set(type, []);
      documentListeners.get(type).push({fn, once: !!(options && options.once)});
    },
    dispatch(type, event = {}) {
      for (const entry of (documentListeners.get(type) || []).slice()) {
        entry.fn(event);
        if (entry.once) {
          const current = documentListeners.get(type) || [];
          const index = current.indexOf(entry);
          if (index >= 0) current.splice(index, 1);
        }
        if (event.immediatePropagationStopped) break;
      }
    },
  };
  let alerts = 0;
  let confirms = 0;
  let confirmResult = true;
  const context = {
    Promise,
    Math,
    Date,
    location: {origin: 'http://onebase.test'},
    document,
    crypto: {randomUUID() { uuid++; return `id-${uuid}`; }},
    setTimeout() { return 1; },
    clearTimeout() {},
    alert() { alerts++; },
    confirm() { confirms++; return confirmResult; },
    obUIMessage(name, fallback) { return fallback; },
    addEventListener(type, fn) { if (type === 'message') listeners.push(fn); },
  };
  context.window = context;
  vm.runInNewContext(bridge, context, {filename: 'form-close-bridge.js'});
  return {
    context,
    document,
    message(ev) { for (const listener of listeners) listener(ev); },
    setManaged(value) { managedPage = value; },
    alerts() { return alerts; },
    confirms() { return confirms; },
    setConfirmResult(value) { confirmResult = value; },
    ready() {
      document.readyState = 'interactive';
      document.dispatch('DOMContentLoaded');
    },
  };
}

function closeFrame() {
  const sent = [];
  const loadListeners = [];
  let finalized = 0;
  let document = {
    readyState: 'complete',
    getElementById(id) { return id === 'ob-managed-config' ? {} : null; },
  };
  const child = {
    document,
    obFrameCloseDocumentToken: 'child-document-1',
    postMessage(data, origin) { sent.push({data, origin}); },
    obRequestFormClose() {},
    obFinalizeFormClose() { finalized++; return true; },
  };
  const frame = {
    contentWindow: child,
    contentDocument: document,
    addEventListener(type, listener) { if (type === 'load') loadListeners.push(listener); },
  };
  return {
    frame,
    child,
    sent,
    finalized() { return finalized; },
    loadSameDocument() { for (const listener of loadListeners.slice()) listener(); },
    navigate() {
      document = {
        readyState: 'complete',
        getElementById(id) { return id === 'ob-managed-config' ? {} : null; },
      };
      frame.contentDocument = document;
      child.document = document;
      child.obFrameCloseDocumentToken = 'child-document-2';
      child.obFinalizeFormClose = function () { finalized++; return true; };
      for (const listener of loadListeners.slice()) listener();
    },
  };
}

test('embedded header close starts the managed handoff before posting to the shell', () => {
  const posted = [];
  let began = 0;
  let cancelled = 0;
  const parent = {
    obOpenTab() {},
    postMessage(data, origin) { posted.push({data, origin}); },
  };
  const context = {
    parent,
    location: {origin: 'http://onebase.test'},
    obBeginManagedCloseHandoff() { began++; },
    obCancelManagedCloseHandoff() { cancelled++; },
  };
  context.window = context;
  vm.runInNewContext(shellClose, context, {filename: 'embedded-shell-close.js'});

  assert.equal(context.obCloseInShell('cross'), true);
  assert.equal(began, 1);
  assert.equal(cancelled, 0);
  assert.equal(posted.length, 1);
  assert.equal(posted[0].data.source, 'obCloseTab');
});

test('parent accepts a close decision only from the requested same-origin frame', async () => {
  const app = runtime();
  const sent = [];
  const childDocument = {readyState: 'complete', getElementById() { return null; }};
  const child = {document: childDocument, postMessage(data, origin) { sent.push({data, origin}); }};
  const frame = {contentWindow: child, contentDocument: childDocument};
  const decisionPromise = app.context.obRequestFrameClose(frame, 'escape');
  assert.equal(sent.length, 1);
  assert.equal(sent[0].origin, 'http://onebase.test');
  assert.equal(sent[0].data.reason, 'escape');
  const correlation = sent[0].data.correlation;

  app.message({origin: 'https://evil.test', source: child, data: {
    source: 'obFormCloseDecision', correlation, allowed: true,
  }});
  app.message({origin: 'http://onebase.test', source: {}, data: {
    source: 'obFormCloseDecision', correlation, allowed: true,
  }});
  let settled = false;
  decisionPromise.then(() => { settled = true; });
  await Promise.resolve();
  assert.equal(settled, false, 'spoofed decision resolved the request');

  app.message({origin: 'http://onebase.test', source: child, data: {
    source: 'obFormCloseDecision', correlation, allowed: false, intentId: 'server-id', error: 'cancelled',
  }});
  const decision = await decisionPromise;
  assert.equal(decision.allowed, false);
  assert.equal(decision.intentId, 'server-id');
  assert.equal(decision.error, 'cancelled');
});

test('same-document initial load keeps the pending frame decision valid', async () => {
  const app = runtime();
  const target = closeFrame();
  const decisionPromise = app.context.obRequestFrameClose(target.frame, 'cross');
  const correlation = target.sent[0].data.correlation;

  target.loadSameDocument();
  app.message({origin: 'http://onebase.test', source: target.child, data: {
    source: 'obFormCloseDecision', correlation, allowed: true, intentId: 'same-document',
  }});

  const decision = await decisionPromise;
  assert.equal(decision.allowed, true);
  assert.equal(app.context.obFinalizeFrameClose(target.frame, decision), true);
  assert.equal(target.finalized(), 1);
});

test('same WindowProxy response is rejected after the iframe Document changes', async () => {
  const app = runtime();
  const target = closeFrame();
  const sourceWindowProxy = target.child;
  const decisionPromise = app.context.obRequestFrameClose(target.frame, 'cross');
  const correlation = target.sent[0].data.correlation;

  target.navigate();
  assert.equal(target.frame.contentWindow, sourceWindowProxy, 'test replaced WindowProxy');
  app.message({origin: 'http://onebase.test', source: sourceWindowProxy, data: {
    source: 'obFormCloseDecision', correlation, allowed: true, intentId: 'stale-intent',
  }});

  const decision = await decisionPromise;
  assert.equal(decision.allowed, false);
  assert.equal(decision.error, 'frame-navigated');
  assert.equal(target.finalized(), 0);
});

test('an accepted decision cannot finalize a later Document in the same iframe', async () => {
  const app = runtime();
  const target = closeFrame();
  const decisionPromise = app.context.obRequestFrameClose(target.frame, 'cross');
  const correlation = target.sent[0].data.correlation;
  app.message({origin: 'http://onebase.test', source: target.child, data: {
    source: 'obFormCloseDecision', correlation, allowed: true, intentId: 'old-document',
  }});
  const decision = await decisionPromise;

  target.navigate();
  assert.equal(app.context.obFinalizeFrameClose(target.frame, decision), false);
  assert.equal(target.finalized(), 0);
});

test('child delegates to the managed controller and replies to the exact requester origin', async () => {
  const app = runtime(true);
  app.context.obRequestFormClose = async ({reason}) => ({allowed: reason === 'cross', intentId: 'intent-1'});
  const replies = [];
  const parent = {postMessage(data, origin) { replies.push({data, origin}); }};
  app.message({origin: 'http://onebase.test', source: parent, data: {
    source: 'obRequestFormClose', correlation: 'corr-1', reason: 'cross',
  }});
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(replies.length, 1);
  assert.equal(replies[0].origin, 'http://onebase.test');
  assert.equal(replies[0].data.source, 'obFormCloseDecision');
  assert.equal(replies[0].data.correlation, 'corr-1');
  assert.equal(replies[0].data.allowed, true);
  assert.equal(replies[0].data.intentId, 'intent-1');
});

test('managed page without a ready controller fails closed', async () => {
  const app = runtime(true);
  const replies = [];
  const parent = {postMessage(data, origin) { replies.push({data, origin}); }};
  app.message({origin: 'http://onebase.test', source: parent, data: {
    source: 'obRequestFormClose', correlation: 'corr-2', reason: 'cross',
  }});
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(replies[0].data.allowed, false);
  assert.equal(replies[0].data.error, 'controller-not-ready');
});

test('non-managed child owns the dirty confirmation when the shell requests close', async () => {
  const app = runtime(false);
  app.context._obFormDirty = true;
  app.setConfirmResult(false);
  const replies = [];
  const parent = {postMessage(data, origin) { replies.push({data, origin}); }};
  app.message({origin: 'http://onebase.test', source: parent, data: {
    source: 'obRequestFormClose', correlation: 'dirty-autogen', reason: 'cross',
  }});
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(app.confirms(), 1);
  assert.equal(replies[0].data.allowed, false);
  assert.equal(replies[0].data.error, 'user-cancelled');
});

test('close request received while parsing waits for the managed controller', async () => {
  const app = runtime(false, 'loading');
  const replies = [];
  const parent = {postMessage(data, origin) { replies.push({data, origin}); }};
  app.message({origin: 'http://onebase.test', source: parent, data: {
    source: 'obRequestFormClose', correlation: 'corr-loading', reason: 'cross',
  }});
  await Promise.resolve();
  assert.equal(replies.length, 0, 'loading page was classified as non-managed');

  app.setManaged(true);
  app.context.obRequestFormClose = async () => ({allowed: true, intentId: 'loaded-intent'});
  app.ready();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(replies.length, 1);
  assert.equal(replies[0].data.allowed, true);
  assert.equal(replies[0].data.intentId, 'loaded-intent');
});

test('managed close link clicked while parsing is held until delegates are ready', () => {
  const app = runtime(false, 'loading');
  let replayed = 0;
  const link = {
    isConnected: true,
    closest(selector) { return selector === '[data-ob-close-tab]' ? this : null; },
    click() { replayed++; },
  };
  const event = {
    target: link,
    preventDefault() { this.defaultPrevented = true; },
    stopImmediatePropagation() { this.immediatePropagationStopped = true; },
  };
  app.document.dispatch('click', event);
  assert.equal(event.defaultPrevented, true);
  assert.equal(event.immediatePropagationStopped, true);
  assert.equal(replayed, 0);
  app.ready();
  assert.equal(replayed, 1);
});

test('loaded managed page without its controller blocks direct close navigation', () => {
  const app = runtime(true, 'complete');
  const link = {closest(selector) { return selector === '[data-ob-close-tab]' ? this : null; }};
  const event = {
    target: link,
    preventDefault() { this.defaultPrevented = true; },
    stopImmediatePropagation() { this.immediatePropagationStopped = true; },
  };
  app.document.dispatch('click', event);
  assert.equal(event.defaultPrevented, true);
  assert.equal(event.immediatePropagationStopped, true);
  assert.equal(app.alerts(), 1);
});

test('parent finalizes the exact managed frame before removal is allowed', () => {
  const app = runtime();
  let finalized = 0;
  const child = {
    document: {readyState: 'complete', getElementById(id) { return id === 'ob-managed-config' ? {} : null; }},
    obFrameCloseDocumentToken: 'managed-document',
    obRequestFormClose() {},
    obFinalizeFormClose() { finalized++; return true; },
  };
  const frame = {contentWindow: child, contentDocument: child.document};
  assert.equal(app.context.obFinalizeFrameClose(frame, {
    allowed: true,
    intentId: 'unbound-managed-intent',
  }), false, 'managed decision without a document binding was accepted');
  assert.equal(finalized, 0);
  const bound = app.context.obBindFrameCloseDecision(frame, {
    allowed: true,
    intentId: 'managed-intent',
  }, 'managed-document');
  assert.ok(bound);
  assert.equal(app.context.obFinalizeFrameClose(frame, bound), true);
  assert.equal(finalized, 1);

  const missingFinalizeDocument = {readyState: 'complete', getElementById() { return {}; }};
  const missingFinalizeChild = {document: missingFinalizeDocument, obFrameCloseDocumentToken: 'missing-finalizer'};
  const missingFinalizeFrame = {contentWindow: missingFinalizeChild, contentDocument: missingFinalizeDocument};
  const missingFinalizeDecision = app.context.obBindFrameCloseDecision(missingFinalizeFrame, {
    allowed: true,
    intentId: 'managed-intent',
  }, 'missing-finalizer');
  assert.equal(app.context.obFinalizeFrameClose(missingFinalizeFrame, missingFinalizeDecision), false,
    'managed decision without a finalizer was fail-open');
	let autoDirty = true;
	const autoDocument = {readyState: 'complete', getElementById() { return null; }};
	const autoChild = {
	  document: autoDocument,
	  obFrameCloseDocumentToken: 'auto-document',
	  obSetAutoFormDirty(value) { autoDirty = !!value; },
	};
	const autoFrame = {contentWindow: autoChild, contentDocument: autoDocument};
	const deniedAuto = app.context.obBindFrameCloseDecision(autoFrame, {
	  allowed: false, intentId: '',
	}, 'auto-document');
	assert.equal(app.context.obFinalizeFrameClose(autoFrame, deniedAuto), false);
	assert.equal(autoDirty, true, 'denied decision cleared autogenerated dirty');
	const autoDecision = app.context.obBindFrameCloseDecision(autoFrame, {
	  allowed: true, intentId: '',
	}, 'auto-document');
	assert.equal(app.context.obFinalizeFrameClose(autoFrame, autoDecision), true,
	  'exact non-managed decision unexpectedly failed finalization');
	assert.equal(autoDirty, false, 'non-managed finalization left beforeunload dirty');
  assert.equal(app.context.obFinalizeFrameClose({contentWindow: {
    document: {readyState: 'complete', getElementById() { return {}; }},
  }}, {allowed: true, intentId: ''}), false, 'managed frame accepted a decision without an intent');
});
