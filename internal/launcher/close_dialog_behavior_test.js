const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const htmlPath = process.env.ONEBASE_CLOSE_DIALOG_HTML;
if (!htmlPath) throw new Error('ONEBASE_CLOSE_DIALOG_HTML is not set');
const html = fs.readFileSync(htmlPath, 'utf8');
const markerStart = 'var _onebaseCloseDialogBegin = true;';
const markerEnd = 'var _onebaseCloseDialogEnd = true;';
const sourceStart = html.indexOf(markerStart);
const sourceEnd = html.indexOf(markerEnd, sourceStart + markerStart.length);
if (sourceStart < 0 || sourceEnd < 0) throw new Error('close dialog production JavaScript slice not found');
const closeSource = html.slice(sourceStart + markerStart.length, sourceEnd);

function response(status, body = {}) {
  const raw = typeof body === 'string' ? body : JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    text() { return Promise.resolve(raw); }
  };
}

function deferredResponse() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return {
    promise,
    resolve(status, body = {}) { resolve(response(status, body)); }
  };
}

function createHarness() {
  const elements = new Map();
  const listeners = new Map();
  const calls = [];
  const queue = [];
  let document;

  function element(id, tag = 'div', parent = null, display = '') {
    const attrs = new Map();
    const node = {
      id,
      tagName: tag.toUpperCase(),
      parentElement: parent,
      children: [],
      style: {display},
      hidden: false,
      disabled: false,
      checked: false,
      inert: false,
      value: '',
      textContent: '',
      className: '',
      setAttribute(name, value) { attrs.set(String(name), String(value)); },
      removeAttribute(name) { attrs.delete(String(name)); },
      getAttribute(name) { return attrs.has(String(name)) ? attrs.get(String(name)) : null; },
      hasAttribute(name) { return attrs.has(String(name)); },
      appendChild(child) {
        child.parentElement = this;
        this.children.push(child);
        return child;
      },
      contains(other) {
        for (let current = other; current; current = current.parentElement) {
          if (current === this) return true;
        }
        return false;
      },
      focus() { document.activeElement = this; },
      querySelectorAll() {
        const found = [];
        function walk(current) {
          for (const child of current.children) {
            const focusTag = ['BUTTON', 'INPUT', 'SELECT', 'A'].includes(child.tagName);
            const tabIndex = child.hasAttribute('tabindex') && child.getAttribute('tabindex') !== '-1';
            if (focusTag || tabIndex) found.push(child);
            walk(child);
          }
        }
        walk(this);
        return found;
      }
    };
    Object.defineProperty(node, 'innerHTML', {
      get() { return ''; },
      set() { node.children = []; }
    });
    if (id) elements.set(id, node);
    if (parent) parent.children.push(node);
    return node;
  }

  const body = element('body', 'body');
  const toolbar = element('launcher-toolbar', 'div', body);
  const trigger = element('quit-trigger', 'a', toolbar);
  const policySelect = element('close-policy-setting', 'select', toolbar);
  const policyStatus = element('close-policy-setting-status', 'span', toolbar);
  const content = element('launcher-content', 'div', body);
  const modal = element('close-modal', 'div', body, 'none');
  const card = element('close-modal-card', 'div', modal);
  card.setAttribute('tabindex', '-1');
  const ask = element('close-modal-ask', 'div', card);
  const list = element('close-modal-list', 'ul', ask);
  const remember = element('close-modal-remember', 'input', ask);
  const background = element('close-modal-background', 'button', ask);
  const stop = element('close-modal-stop', 'button', ask);
  const cancel = element('close-modal-cancel', 'button', ask);
  const busy = element('close-modal-busy', 'div', card, 'none');
  const progress = element('close-modal-progress', 'p', busy);
  const error = element('close-modal-error', 'div', card, 'none');
  const errorText = element('close-modal-error-text', 'p', error);
  const retry = element('close-modal-retry', 'button', error);
  const proceed = element('close-modal-continue', 'button', error, 'none');
  const errorCancel = element('close-modal-error-cancel', 'button', error);

  document = {
    body,
    activeElement: trigger,
    getElementById(id) { return elements.get(id) || null; },
    createElement(tag) { return element('', tag); },
    contains(node) { return body.contains(node); },
    addEventListener(type, listener) {
      if (!listeners.has(type)) listeners.set(type, []);
      listeners.get(type).push(listener);
    }
  };

  const state = {
    confirmResult: true,
    confirmCalls: 0,
    postCalls: 0,
    closeCalls: 0,
    alerts: []
  };
  const context = {
    document,
    window: {close() { state.closeCalls++; }},
    fetch(url, options = {}) {
      calls.push({url, options});
      if (!queue.length) return Promise.reject(new Error('unexpected fetch ' + url));
      const next = queue.shift();
      return next && typeof next.then === 'function' ? next : Promise.resolve(next);
    },
    confirm() { state.confirmCalls++; return state.confirmResult; },
    alert(message) { state.alerts.push(String(message)); },
    doPost() { state.postCalls++; return false; },
    setTimeout(fn) { fn(); return 1; },
    clearTimeout() {},
    console,
    URLSearchParams,
    encodeURIComponent,
    decodeURIComponent
  };
  vm.createContext(context);
  vm.runInContext(closeSource, context, {filename: 'launcher-close-dialog.js'});

  return {
    context,
    state,
    calls,
    elements: {toolbar, content, modal, card, ask, list, remember, background, stop, cancel,
      busy, progress, error, errorText, retry, proceed, errorCancel, trigger, policySelect, policyStatus},
    enqueue(status, body) { queue.push(response(status, body)); },
    defer() { const item = deferredResponse(); queue.push(item.promise); return item; },
    fireKey(key, values = {}) {
      const event = Object.assign({
        key,
        shiftKey: false,
        defaultPrevented: false,
        target: document.activeElement,
        preventDefault() { this.defaultPrevented = true; },
        stopPropagation() { this.stopped = true; }
      }, values);
      for (const listener of listeners.get('keydown') || []) listener(event);
      return event;
    }
  };
}

async function drain() {
  for (let i = 0; i < 12; i++) await Promise.resolve();
  await new Promise((resolve) => setImmediate(resolve));
}

function urls(harness) {
  return harness.calls.map((call) => call.url);
}

test('remembered stop is single-flight and orders policy before verified close-stop', async () => {
  const h = createHarness();
  h.enqueue(200, {running: [{name: 'Main', port: 8080}], policy: 'ask'});
  const policy = h.defer();
  const stopping = h.defer();

  h.context.quitLauncher();
  await drain();
  assert.equal(h.context._closeFlow.state, 'prompt');
  h.elements.remember.checked = true;
  h.context.closeChoice('stop');
  h.context.closeChoice('background');
  const escape = h.fireKey('Escape');

  assert.equal(h.context._closeFlow.state, 'saving');
  assert.equal(h.elements.ask.style.display, 'none');
  assert.equal(h.elements.stop.disabled, true);
  assert.equal(escape.defaultPrevented, false, 'committed action was treated as cancellable');
  assert.deepEqual(urls(h), ['/close-info', '/close-policy']);

  policy.resolve(200, {ok: true});
  await drain();
  assert.equal(h.context._closeFlow.state, 'stopping');
  assert.deepEqual(urls(h), ['/close-info', '/close-policy', '/close-stop']);
  assert.equal(urls(h).includes('/quit'), false);

  stopping.resolve(200, {ok: true, remaining: []});
  await drain();
  assert.equal(h.context._closeFlow.state, 'done');
  assert.equal(h.state.closeCalls, 1);
  assert.deepEqual(urls(h), ['/close-info', '/close-policy', '/close-stop']);
});

test('policy error is visible and continue without remembering compensates before stop', async () => {
  const h = createHarness();
  h.enqueue(200, {running: [{name: 'Main', port: 8080}], policy: 'ask'});
  h.enqueue(500, {error: 'disk is read-only'});
  h.enqueue(200, {ok: true}); // restore ask after an uncertain failed write
  h.enqueue(200, {ok: true, remaining: []});

  h.context.quitLauncher();
  await drain();
  h.elements.remember.checked = true;
  h.context.closeChoice('stop');
  await drain();

  assert.equal(h.context._closeFlow.state, 'error');
  assert.equal(h.elements.error.style.display, 'block');
  assert.match(h.elements.errorText.textContent, /disk is read-only/);
  assert.deepEqual(urls(h), ['/close-info', '/close-policy']);

  h.context.continueCloseWithoutRemembering();
  await drain();
  assert.deepEqual(urls(h), ['/close-info', '/close-policy', '/close-policy', '/close-stop']);
  assert.match(h.calls[2].options.body, /policy=ask/);
  assert.equal(urls(h).includes('/quit'), false);
  assert.equal(h.state.closeCalls, 1);
});

test('failed close-stop never quits and remains retryable', async () => {
  const h = createHarness();
  h.enqueue(200, {running: [{name: 'Main', port: 8080}], policy: 'ask'});
  h.enqueue(503, {error: 'port 8080 is still busy', remaining: [{name: 'Main', port: 8080}]});
  h.enqueue(200, {ok: true, remaining: []});

  h.context.quitLauncher();
  await drain();
  h.context.closeChoice('stop');
  await drain();

  assert.equal(h.context._closeFlow.state, 'error');
  assert.match(h.elements.errorText.textContent, /port 8080 is still busy/);
  assert.equal(urls(h).includes('/quit'), false);
  assert.equal(h.state.closeCalls, 0);

  h.context.retryCloseAction();
  await drain();
  assert.deepEqual(urls(h), ['/close-info', '/close-stop', '/close-stop']);
  assert.equal(urls(h).includes('/quit'), false);
  assert.equal(h.state.closeCalls, 1);
});

test('non-2xx close-info stays visible until explicit background close', async () => {
  const h = createHarness();
  h.enqueue(500, {error: 'registry unavailable'});
  h.enqueue(200, {ok: true});

  h.context.quitLauncher();
  await drain();
  assert.equal(h.context._closeFlow.state, 'error');
  assert.match(h.elements.errorText.textContent, /registry unavailable/);
  assert.deepEqual(urls(h), ['/close-info']);
  assert.equal(h.state.closeCalls, 0);

  h.context.continueCloseWithoutRemembering();
  await drain();
  assert.deepEqual(urls(h), ['/close-info', '/quit']);
  assert.equal(h.state.closeCalls, 1);
});

test('remembered background orders policy before quit', async () => {
  const h = createHarness();
  h.enqueue(200, {running: [{name: 'Main', port: 8080}], policy: 'ask'});
  h.enqueue(200, {ok: true});
  h.enqueue(200, {ok: true});

  h.context.quitLauncher();
  await drain();
  h.elements.remember.checked = true;
  h.context.closeChoice('background');
  await drain();

  assert.deepEqual(urls(h), ['/close-info', '/close-policy', '/quit']);
  assert.equal(urls(h).includes('/close-stop'), false);
  assert.equal(h.state.closeCalls, 1);
});

test('remembered stop with an empty snapshot still closes the Start race through close-stop', async () => {
  const h = createHarness();
  h.enqueue(200, {running: [], policy: 'stop'});
  h.enqueue(200, {ok: true, remaining: []});

  h.context.quitLauncher();
  await drain();

  assert.deepEqual(urls(h), ['/close-info', '/close-stop']);
  assert.equal(h.calls[0].options.cache, 'no-store');
  assert.equal(urls(h).includes('/quit'), false);
  assert.equal(h.state.closeCalls, 1);
});

test('failed policy rollback can be dismissed without an infinite retry loop', async () => {
  const h = createHarness();
  h.enqueue(200, {running: [{name: 'Main', port: 8080}], policy: 'ask'});
  h.enqueue(500, {error: 'uncertain write'});
  h.enqueue(500, {error: 'rollback disk failure'});

  h.context.quitLauncher();
  await drain();
  h.elements.remember.checked = true;
  h.context.closeChoice('background');
  await drain();
  h.context.closeChoice('cancel');
  await drain();
  assert.equal(h.context._closeFlow.state, 'error');
  assert.match(h.elements.errorText.textContent, /rollback disk failure/);

  const before = urls(h).slice();
  h.context.closeChoice('cancel');
  await drain();
  assert.equal(h.context._closeFlow.state, 'idle');
  assert.equal(h.elements.modal.style.display, 'none');
  assert.deepEqual(urls(h), before);
});

// Порт, занятый неподтверждённым процессом, помечается, но остановку остальных
// баз больше не запрещает: раньше кнопка «Остановить все» выключалась, и одна
// такая база лишала пользователя остановки насовсем.
test('uncontrollable occupied port is labelled but stop stays available', async () => {
  const h = createHarness();
  h.enqueue(200, {
    running: [
      {name: 'Main', port: 8079, controllable: true},
      {name: 'Foreign', port: 8080, controllable: false},
    ],
    policy: 'ask',
  });

  h.context.quitLauncher();
  await drain();
  assert.equal(h.context._closeFlow.state, 'prompt');
  assert.equal(h.elements.stop.disabled, false);
  assert.match(h.elements.list.children[1].textContent, /8080/);
});

// Если останавливать нечего — вопрос stop/background бессмыслен, но занятый
// неподтверждённый порт всё равно должен быть показан до закрытия.
test('unverified-only ask path warns without showing the choice prompt', async () => {
  const h = createHarness();
  h.enqueue(200, {running: [{name: 'Foreign', port: 8080, controllable: false}], policy: 'ask'});
  h.enqueue(200, {ok: true, warning: 'Foreign (порт 8080)'});
  h.enqueue(200, {ok: true});

  h.context.quitLauncher();
  await drain();
  assert.equal(h.context._closeFlow.state, 'done');
  assert.deepEqual(urls(h), ['/close-info', '/close-stop', '/quit']);
  assert.match(h.state.alerts.join('\n'), /8080/);
});

// Оставшиеся базы показываются пользователю до закрытия окна: другого места
// узнать о них уже не будет.
test('close-stop warning about skipped bases is shown before the window closes', async () => {
  const h = createHarness();
  h.enqueue(200, {running: [{name: 'Main', port: 8079, controllable: true}], policy: 'stop'});
  h.enqueue(200, {ok: true, warning: 'Foreign (порт 8080)'});
  h.enqueue(200, {ok: true});
  const recordAlert = h.context.alert;
  h.context.alert = function(message) {
    assert.deepEqual(urls(h), ['/close-info', '/close-stop'], '/quit raced ahead of warning acknowledgement');
    recordAlert(message);
  };

  h.context.quitLauncher();
  await drain();
  assert.equal(h.context._closeFlow.state, 'done');
  assert.match(h.state.alerts.join('\n'), /8080/);
  assert.deepEqual(urls(h), ['/close-info', '/close-stop', '/quit']);
});

test('Stop all confirmation never trusts stale rendered running count', () => {
  const h = createHarness();
  h.context._runningCount = 0;
  h.state.confirmResult = false;
  assert.equal(h.context.confirmKillAll({}), false);
  assert.equal(h.state.confirmCalls, 1);
  assert.equal(h.state.postCalls, 0);

  h.state.confirmResult = true;
  assert.equal(h.context.confirmKillAll({}), false);
  assert.equal(h.state.confirmCalls, 2);
  assert.equal(h.state.postCalls, 1);
});

test('toolbar policy rejects non-2xx and restores the prior value', async () => {
  const h = createHarness();
  h.enqueue(500, {error: 'cannot write settings'});
  h.elements.policySelect.value = 'background';
  h.context.setClosePolicyFromToolbar(h.elements.policySelect);
  await drain();

  assert.equal(h.context._closePolicy, 'ask');
  assert.equal(h.elements.policySelect.value, 'ask');
  assert.match(h.elements.policyStatus.textContent, /cannot write settings/);

  h.enqueue(200, {ok: true});
  h.elements.policySelect.value = 'background';
  h.context.setClosePolicyFromToolbar(h.elements.policySelect);
  await drain();
  assert.equal(h.context._closePolicy, 'background');
  assert.equal(h.elements.policySelect.value, 'background');
});

test('long list is bounded and prompt cancel restores focus and background', async () => {
  const h = createHarness();
  const running = Array.from({length: 15}, (_, i) => ({name: 'Base ' + i, port: 8000 + i}));
  h.enqueue(200, {running, policy: 'ask'});

  h.context.quitLauncher();
  await drain();
  assert.equal(h.elements.list.children.length, 11);
  assert.match(h.elements.list.children[10].textContent, /5/);
  assert.equal(h.elements.toolbar.inert, true);
  assert.equal(h.elements.content.inert, true);
  assert.equal(h.context.document.activeElement, h.elements.background);

  const escape = h.fireKey('Escape');
  assert.equal(escape.defaultPrevented, true);
  assert.equal(h.context._closeFlow.state, 'idle');
  assert.equal(h.elements.modal.style.display, 'none');
  assert.equal(h.elements.toolbar.inert, false);
  assert.equal(h.elements.content.inert, false);
  assert.equal(h.context.document.activeElement, h.elements.trigger);
});
