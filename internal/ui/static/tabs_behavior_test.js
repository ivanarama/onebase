'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const htmlPath = process.env.ONEBASE_TABS_HTML;
assert.ok(htmlPath, 'ONEBASE_TABS_HTML must point to the rendered app shell');
const html = fs.readFileSync(htmlPath, 'utf8');
function tabsSource(html) { return Array.from(html.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/gi), match => match[1])
  .find(script => script.includes("var STORE='obTabs'")); }
const source = tabsSource(html);
assert.ok(source, 'rendered app shell must contain the tabs runtime');
const uiSource = fs.readFileSync('static/ui.js', 'utf8');
const bridgeMarker = '// Same-origin request/decision protocol shared by shell tabs and reference';
const bridgeStart = uiSource.indexOf(bridgeMarker);
const bridgeEnd = uiSource.indexOf('if (window.__obEmbedded)', bridgeStart);
assert.ok(bridgeStart >= 0 && bridgeEnd > bridgeStart, 'form-close bridge slice not found');
const bridgeSource = uiSource.slice(bridgeStart, bridgeEnd);

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
    if (this.tagName === 'IFRAME') {
      this._posted = [];
      this.contentDocument = {
        readyState: 'complete',
        getElementById() { return null; },
      };
      // По умолчанию фрейм — обычная страница приложения: ui.js в ней
      // отвечает на запрос закрытия (#1684). markPlainPage моделирует страницу
      // вне протокола — текстовую ошибку 404.
      this.contentWindow = {
        document: this.contentDocument,
        obAnswersFrameClose: true,
        postMessage: (data, origin) => { this._posted.push({data, origin}); },
      };
    }
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

function shell(storage, search = '', confirmClose = () => true, requestFrameClose = null, finalizeFrameClose = null,
  actualBridge = false, runtimeSource = source) {
  const elements = {};
  for (const id of ['ob-tabstrip', 'ob-tabbody', 'ob-tabempty', 'ob-tabhome']) {
    elements[id] = new Element('div', id);
  }
  const documentListeners = new Map();
  const windowListeners = new Map();
  const document = {
    body: new Element('body'),
    readyState: 'complete',
    getElementById(id) { return elements[id] || null; },
    createElement(tagName) { return new Element(tagName); },
    addEventListener(type, listener) {
      if (!documentListeners.has(type)) documentListeners.set(type, []);
      documentListeners.get(type).push(listener);
    }
  };
  let uuid = 0;
  let confirms = 0;
  let alerts = 0;
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
    clearTimeout() {},
    confirm() { confirms++; return confirmClose(); },
    alert() { alerts++; },
    obUIMessage(name, fallback) { return fallback; },
    addEventListener(type, listener) {
      if (!windowListeners.has(type)) windowListeners.set(type, []);
      windowListeners.get(type).push(listener);
    }
  };
  context.window = context;
  if (actualBridge) vm.runInNewContext(bridgeSource, context, {filename: 'form-close-bridge.js'});
  else {
    if (requestFrameClose) context.obRequestFrameClose = requestFrameClose;
    if (finalizeFrameClose) context.obFinalizeFrameClose = finalizeFrameClose;
  }
  vm.runInNewContext(runtimeSource, context, {filename: 'rendered-tabs-runtime.js'});

  const strip = elements['ob-tabstrip'];
  const frames = () => elements['ob-tabbody'].children.filter(element => element.tagName === 'IFRAME');
  return {
    open(url, title, options) { return context.obOpenTab(url, title, options); },
    closeByURL(url) { return context.obCloseTabByURL(url); },
    confirms() { return confirms; },
    alerts() { return alerts; },
    post(data, frameIndex) {
      const source = frameIndex === undefined ? context : frames()[frameIndex].contentWindow;
      for (const listener of windowListeners.get('message') || []) {
        listener({origin: context.location.origin, source, data});
      }
    },
    markDirty(index, dirty = true) {
      const frame = frames()[index];
      for (const listener of windowListeners.get('message') || []) {
        listener({origin: context.location.origin, source: frame.contentWindow, data: {source: 'obDirty', dirty}});
      }
    },
    markManaged(index) {
      const frame = frames()[index];
      frame.contentDocument = {
        readyState: 'complete',
        getElementById(id) { return id === 'ob-managed-config' ? {} : null; }
      };
      frame.contentWindow.document = frame.contentDocument;
    },
    markPlainPage(index) {
      delete frames()[index].contentWindow.obAnswersFrameClose;
    },
    markLoading(index) {
      const frame = frames()[index];
      frame.contentDocument = {
        readyState: 'loading',
        getElementById() { return null; },
      };
      frame.contentWindow.document = frame.contentDocument;
    },
    installManagedDocument(index, token, onFinalize, dispatchLoad = false) {
      const frame = frames()[index];
      const child = frame.contentWindow;
      frame.contentDocument = {
        readyState: 'complete',
        getElementById(id) { return id === 'ob-managed-config' ? {} : null; },
      };
      child.document = frame.contentDocument;
      child.obFrameCloseDocumentToken = token;
      child.obRequestFormClose = function () {};
      child.obFinalizeFormClose = function () {
        if (onFinalize) onFinalize();
        return true;
      };
      if (dispatchLoad) frame.dispatch('load');
      return child;
    },
    posted(index) { return frames()[index]._posted.slice(); },
    frameWindow(index) { return frames()[index].contentWindow; },
    count() { return strip.children.length; },
    activeIndex() { return strip.children.findIndex(button => button.classList.contains('active')); },
    titles() { return strip.children.map(button => button.title); },
    click(index) { strip.children[index].dispatch('click'); },
    close(index) { strip.children[index].children[2].dispatch('click'); },
    duplicate(index) { strip.children[index].children[1].dispatch('click'); },
    // Адрес сменился без перезагрузки фрейма (history.replaceState после записи
    // нового объекта): location новый, события load нет.
    silentNavigate(index, href) {
      frames()[index].contentWindow.location = {href};
    },
    navigate(index, href) {
      const frame = frames()[index];
      frame.contentWindow.location = {href};
      frame.dispatch('load');
    },
    replaceWithoutLoad(index, href) {
      const frame = frames()[index];
      frame.contentWindow.location = {href};
      for (const listener of windowListeners.get('message') || []) {
        listener({
          origin: context.location.origin,
          source: frame.contentWindow,
          data: {source: 'obFrameURLChanged', url: href}
        });
      }
    },
    denyLocation(index) {
      const frame = frames()[index];
      Object.defineProperty(frame.contentWindow, 'location', {
        configurable: true,
        get() { throw new Error('cross-origin location blocked'); }
      });
      frame.dispatch('load');
    }
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

test('same-origin iframe navigation refreshes persistence, restore and URL deduplication', () => {
  const storage = new FakeStorage();
  const createURL = '/ui/document/purchase/new?based_on=sale&based_on_id=42';
  const listURL = '/ui/document/purchase?view=compact#selected';
  let app = shell(storage);
  app.open(createURL, 'Purchase');

  app.navigate(0, 'http://127.0.0.1:8080' + listURL);
  assert.equal(savedTabs(storage)[0].url, listURL);
  assert.equal(savedActive(storage).url, listURL);

  app = shell(storage);
  assert.equal(app.count(), 1);
  assert.equal(app.activeIndex(), 0);
  assert.equal(savedTabs(storage)[0].url, listURL);

  app.open(createURL, 'Purchase again');
  assert.equal(app.count(), 2);
  assert.deepEqual(savedTabs(storage).map(tab => tab.url), [listURL, createURL]);
});

test('history replacement refreshes persistence, restore and URL deduplication without iframe load', () => {
  const storage = new FakeStorage();
  const createURL = '/ui/document/purchase/new';
  const cardURL = '/ui/document/purchase/42';
  let app = shell(storage);
  app.open(createURL, 'Purchase');

  app.replaceWithoutLoad(0, cardURL);
  assert.equal(savedTabs(storage)[0].url, cardURL);
  assert.equal(savedActive(storage).url, cardURL);

  app = shell(storage);
  assert.equal(app.count(), 1);
  assert.equal(app.activeIndex(), 0);
  assert.equal(savedTabs(storage)[0].url, cardURL);

  app.open(createURL, 'Purchase again');
  assert.equal(app.count(), 2);
  assert.deepEqual(savedTabs(storage).map(tab => tab.url), [cardURL, createURL]);
});

test('duplicate uses the refreshed URL and unsafe frame locations are ignored', () => {
  const storage = new FakeStorage();
  const createURL = '/ui/document/purchase/new';
  const cardURL = '/ui/document/purchase/42';
  const app = shell(storage);
  app.open(createURL, 'Purchase');
  app.navigate(0, 'http://127.0.0.1:8080' + cardURL);

  app.duplicate(0);
  assert.deepEqual(savedTabs(storage).map(tab => tab.url), [cardURL, cardURL]);

  const before = storage.getItem('obTabs');
  app.navigate(0, 'https://example.invalid/ui/document/purchase/99');
  assert.equal(storage.getItem('obTabs'), before);
  app.navigate(0, 'http://127.0.0.1:8080/ui/login');
  assert.equal(storage.getItem('obTabs'), before);
  assert.doesNotThrow(() => app.denyLocation(0));
  assert.equal(storage.getItem('obTabs'), before);
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
    obTabsScope: scopeOf(source),
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

test('legacy state without a proven session owner is discarded before reading titles', () => {
  const storage = new FakeStorage({
    obTabs: JSON.stringify([{url:'/ui/a',title:'Foreign secret'}]),
    obTabsActive: '/ui/a', unrelated: 'keep'
  });
  const app = shell(storage);
  assert.equal(app.count(), 0);
  assert.equal(app.activeIndex(), -1);
  assert.deepEqual(savedTabs(storage), []);
  assert.equal(storage.getItem('obTabsActive'), null);
  assert.equal(storage.getItem('unrelated'), 'keep');
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
    obTabsScope: scopeOf(source),
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

test('a form tab is closed by its address, not by whichever tab is active', () => {
  const storage = new FakeStorage();
  const app = shell(storage);
  app.open('/ui/document/обращение/1', 'Обращение');
  // «Открой заявку, закрой звонок»: к моменту закрытия активна уже заявка,
  // и закрытие «активной» унесло бы именно её.
  app.open('/ui/document/заявка/2', 'Заявка');
  assert.equal(app.activeIndex(), 1);

  assert.equal(app.closeByURL('/ui/document/обращение/1'), 1);
  assert.deepEqual(app.titles(), ['Заявка']);
  assert.equal(app.activeIndex(), 0);
  assert.equal(savedTabs(storage).length, 1);
  // Повтор и неизвестный адрес безвредны: закрывать нечего.
  assert.equal(app.closeByURL('/ui/document/обращение/1'), 0);
  assert.equal(app.closeByURL(''), 0);
  assert.equal(app.count(), 1);
});

test('missing bridge keeps legacy dirty protection for non-managed forms', () => {
  const storage = new FakeStorage();
  const app = shell(storage);
  app.open('/ui/document/обращение/1', 'Обращение');
  app.markDirty(0);
  // Адрес не доказывает, что именно этот экземпляр формы уже записан.
  assert.equal(app.closeByURL('/ui/document/обращение/1'), 1);
  assert.equal(app.count(), 0);
  assert.equal(app.confirms(), 1);

  app.open('/ui/document/заявка/2', 'Заявка');
  app.markDirty(0);
  app.close(0);
  assert.equal(app.confirms(), 2);
});

test('a frame may close a tab by address, and without one still closes itself', () => {
  const storage = new FakeStorage();
  const app = shell(storage);
  app.open('/ui/document/обращение/1', 'Обращение');
  app.open('/ui/document/заявка/2', 'Заявка');

  app.post({source: 'obCloseTab', url: '/ui/document/обращение/1'}, 1);
  assert.deepEqual(app.titles(), ['Заявка']);

  // Прежний контракт: без адреса закрывается вкладка-отправитель (крестик внутри формы).
  app.post({source: 'obCloseTab'}, 0);
  assert.equal(app.count(), 0);
});

test('a form that has just saved a new object is closed by its new address', () => {
  const storage = new FakeStorage();
  const app = shell(storage);
  app.open('/ui/document/обращение/new', 'Обращение');
  // Запись нового документа подменяет адрес через replaceState — load не приходит.
  app.silentNavigate(0, 'http://127.0.0.1:8080/ui/document/обращение/42');

  assert.equal(app.closeByURL('/ui/document/обращение/42'), 1);
  assert.equal(app.count(), 0);
});

test('the same document written differently is still the same tab', () => {
  const storage = new FakeStorage();
  const app = shell(storage);
  // Так вкладку открывает ссылка из списка: имя сущности как в метаданных.
  app.open('/ui/document/%d0%9e%d0%b1%d1%80%d0%b0%d1%89%d0%b5%d0%bd%d0%b8%d0%b5/7', 'Обращение');
  // А так адрес строит formURL команды: encodeURIComponent от нижнего регистра.
  assert.equal(app.closeByURL('/ui/document/%D0%BE%D0%B1%D1%80%D0%B0%D1%89%D0%B5%D0%BD%D0%B8%D0%B5/7'), 1);
  assert.equal(app.count(), 0);
});

test('subsystem context does not prevent address-driven close', () => {
  const storage = new FakeStorage();
  const app = shell(storage);
  app.open('/ui/document/обращение/42?subsystem=%D0%9f%D1%80%D0%BE%D0%B4%D0%B0%D0%B6%D0%B8', 'Обращение');
  assert.equal(app.closeByURL('/ui/document/обращение/42'), 1);
  assert.equal(app.count(), 0);
});

test('missing bridge keeps dirty protection for another non-managed duplicate', () => {
  const storage = new FakeStorage();
  const app = shell(storage);
  app.open('/ui/document/обращение/1', 'Обращение');
  app.duplicate(0);
  app.markDirty(1);
  assert.equal(app.closeByURL('/ui/document/обращение/1'), 2);
  assert.equal(app.count(), 0);
  assert.equal(app.confirms(), 1);
});

test('repeated fallback close cannot bypass a rejected non-managed confirmation', () => {
  const storage = new FakeStorage();
  const app = shell(storage, '', () => false);
  app.open('/ui/document/обращение/1', 'Обращение');
  app.duplicate(0);
  app.markDirty(1);

  assert.equal(app.closeByURL('/ui/document/обращение/1'), 1);
  assert.equal(app.count(), 1);
  assert.equal(app.confirms(), 1);

  assert.equal(app.closeByURL('/ui/document/обращение/1'), 0);
  assert.equal(app.count(), 1);
  assert.equal(app.confirms(), 2);
});

test('shell removes a form only after the correlated close decision allows it', async () => {
  const storage = new FakeStorage();
  const requests = [];
  const finalizations = [];
  let finish;
  const app = shell(storage, '', () => true, (frame, reason) => {
    requests.push({frame, reason});
    return new Promise((resolve) => { finish = resolve; });
  }, (frame, decision) => { finalizations.push({frame, decision}); return true; });
  app.open('/ui/document/обращение/1', 'Обращение');
  app.markDirty(0);

  app.close(0);
  app.close(0);
  assert.equal(app.count(), 1, 'tab was destroyed before the server decision');
  assert.equal(requests.length, 1, 'double click started a second close intent');
  assert.equal(app.confirms(), 0, 'shell displayed a second, non-authoritative dirty confirmation');
  assert.equal(requests[0].reason, 'cross');

  finish({allowed: false, intentId: 'denied'});
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(app.count(), 1, 'denied close removed the tab');

  app.post({source: 'obCloseTab', reason: 'escape'}, 0);
  assert.equal(requests.length, 2);
  assert.equal(app.confirms(), 0);
  assert.equal(requests[1].reason, 'escape');
  finish({allowed: true, intentId: 'allowed'});
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(finalizations.length, 1, 'allowed close was not finalized');
  assert.equal(finalizations[0].decision.intentId, 'allowed');
  assert.equal(app.count(), 0, 'allowed close kept the tab');
});

test('shell keeps a new Document when an old decision uses the same WindowProxy', async () => {
  const app = shell(new FakeStorage(), '', () => true, null, null, true);
  app.open('/ui/document/обращение/old', 'Обращение');
  let finalizedNewDocument = 0;
  app.installManagedDocument(0, 'shell-old-document');
  const sourceWindowProxy = app.frameWindow(0);

  app.close(0);
  const request = app.posted(0)[0];
  assert.ok(request);
  const correlation = request.data.correlation;

  app.installManagedDocument(0, 'shell-new-document', () => { finalizedNewDocument++; }, true);
  assert.equal(app.frameWindow(0), sourceWindowProxy, 'test replaced WindowProxy');
  app.post({
    source: 'obFormCloseDecision', correlation, allowed: true, intentId: 'stale-shell',
  }, 0);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(app.count(), 1, 'stale decision removed the new iframe document');
  assert.equal(finalizedNewDocument, 0, 'stale decision finalized the new iframe document');
});

test('managed tab without a bridge or finalizer remains open', async () => {
  const noBridge = shell(new FakeStorage());
  noBridge.open('/ui/document/обращение/1', 'Обращение');
  noBridge.markManaged(0);
  noBridge.close(0);
  assert.equal(noBridge.count(), 1, 'managed tab was removed without a server bridge');
  assert.equal(noBridge.alerts(), 1);

  const noFinalize = shell(new FakeStorage(), '', () => true,
    async () => ({allowed: true, intentId: 'managed-intent'}),
    () => false);
  noFinalize.open('/ui/document/обращение/2', 'Обращение');
  noFinalize.close(0);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(noFinalize.count(), 1, 'managed tab was removed without dirty finalization');
  assert.equal(noFinalize.alerts(), 1);
});

// #1684: страница вне протокола закрытия (текстовая 404) не ответит никогда —
// вкладка закрывается сразу, без запроса и без подтверждения. Несохранённое
// по-прежнему спрашивается, а загружающаяся и managed-страница без маркера
// идут обычным путём запроса к фрейму.
test('a page outside the close protocol closes without a frame request', async () => {
  const requests = [];
  const request = (frame, reason) => { requests.push(reason); return new Promise(() => {}); };
  const plain = shell(new FakeStorage(), '', () => true, request, () => true);
  plain.open('/ui/catalog/_users/1', 'Ошибка');
  plain.markPlainPage(0);
  plain.close(0);
  assert.equal(plain.count(), 0, 'silent page kept the tab');
  assert.deepEqual(requests, [], 'shell waited for a page that cannot answer');
  assert.equal(plain.confirms(), 0);
  assert.equal(plain.alerts(), 0);

  let answer = false;
  const dirty = shell(new FakeStorage(), '', () => answer, request, () => true);
  dirty.open('/ui/catalog/_users/2', 'Ошибка');
  dirty.markDirty(0);
  dirty.markPlainPage(0);
  dirty.close(0);
  assert.equal(dirty.count(), 1, 'declined confirmation still removed a dirty tab');
  assert.equal(dirty.confirms(), 1);
  answer = true;
  dirty.close(0);
  assert.equal(dirty.count(), 0);
  assert.deepEqual(requests, []);

  const loading = shell(new FakeStorage(), '', () => true, request, () => true);
  loading.open('/ui/document/обращение/4', 'Обращение');
  loading.markPlainPage(0);
  loading.markLoading(0);
  loading.close(0);
  assert.equal(loading.count(), 1, 'loading page was closed as silent');

  const managed = shell(new FakeStorage(), '', () => true, request, () => true);
  managed.open('/ui/document/обращение/5', 'Обращение');
  managed.markPlainPage(0);
  managed.markManaged(0);
  managed.close(0);
  assert.equal(managed.count(), 1, 'managed page without the marker was closed as silent');
  assert.deepEqual(requests, ['cross', 'cross'], 'loading and managed pages must still be asked');
});

test('loading tab without a bridge is treated as managed and remains open', () => {
  const app = shell(new FakeStorage());
  app.open('/ui/document/обращение/3', 'Обращение');
  app.markLoading(0);
  app.close(0);
  assert.equal(app.count(), 1, 'loading tab was classified as legacy and removed');
  assert.equal(app.alerts(), 1);
});

function scopeOf(script) {
  const match = script.match(/var STORAGE_SCOPE=([^;]+);/);
  assert.ok(match, 'public shell must supply a session scope');
  return JSON.parse(match[1]);
}

const shellB = tabsSource(fs.readFileSync(process.env.ONEBASE_TABS_B_HTML, 'utf8'));
const shellANew = tabsSource(fs.readFileSync(process.env.ONEBASE_TABS_A_NEW_HTML, 'utf8'));
const shellAAgain = tabsSource(fs.readFileSync(process.env.ONEBASE_TABS_A_AGAIN_HTML, 'utf8'));
const loginHTML = fs.readFileSync(process.env.ONEBASE_LOGIN_HTML, 'utf8');

test('public login sessions isolate A to B, B to A and re-login of the same account', () => {
  const scopes = [source,shellB,shellANew,shellAAgain].map(scopeOf);
  assert.equal(new Set(scopes).size, 4);
  const storage = new FakeStorage({unrelated:'keep'});
  const stale = shell(storage);
  stale.open('/ui/private/operator', 'Operator secret');
  let app = shell(storage, '', undefined, null, null, false, shellB);
  assert.equal(app.count(), 0);
  assert.equal(storage.getItem('obTabsActive'), null);
  app.open('/ui/private/admin', 'Admin secret');
  const before = storage.getItem('obTabs');
  stale.open('/ui/stale', 'Stale operator');
  assert.equal(storage.getItem('obTabs'), before, 'stale shell overwrote the new session');
  app = shell(storage, '', undefined, null, null, false, shellANew);
  assert.equal(app.count(), 0);
  app.open('/ui/private/operator-new', 'New operator');
  app = shell(storage, '', undefined, null, null, false, shellAAgain);
  assert.equal(app.count(), 0);
  assert.equal(storage.getItem('unrelated'),'keep');
});

test('POST logout redirect to the public login page clears only shell state', () => {
  const storage = new FakeStorage({unrelated:'keep'});
  const app = shell(storage);
  app.open('/ui/private/a', 'Private title');
  const loginScripts = Array.from(loginHTML.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/gi), match=>match[1]);
  assert.ok(loginScripts.length, 'login page must clear shell state');
  for (const script of loginScripts) vm.runInNewContext(script, {sessionStorage:storage});
  for (const key of ['obTabs','obTabsActive','obTabsScope']) assert.equal(storage.getItem(key),null);
  assert.equal(storage.getItem('unrelated'),'keep');
  assert.equal(shell(storage).count(),0);
  for (const script of loginScripts) assert.doesNotThrow(()=>vm.runInNewContext(script,{sessionStorage:new FakeStorage({}, {remove:true})}));
});

test('failure to remove obsolete storage never restores another session snapshot', () => {
  const storage = new FakeStorage({obTabsScope:scopeOf(shellB), obTabs:JSON.stringify([{url:'/ui/private',title:'Foreign'}]), obTabsActive:'/ui/private'}, {remove:true});
  const app = shell(storage);
  assert.equal(app.count(),0);
  app.open('/ui/current','Current');
  assert.equal(JSON.parse(storage.getItem('obTabs'))[0].title,'Foreign', 'failed storage initialization must disable persistence');
});

test('open-access deployment preserves F5 and is discarded on the first authenticated login', () => {
  const openSource = tabsSource(fs.readFileSync(process.env.ONEBASE_TABS_OPEN_HTML,'utf8'));
  const storage = new FakeStorage();
  let app = shell(storage, '', undefined, null, null, false, openSource);
  app.open('/ui/open','Anonymous');
  app = shell(storage, '', undefined, null, null, false, openSource);
  assert.equal(app.count(),1);
  assert.equal(app.activeIndex(),0);
  app = shell(storage);
  assert.equal(app.count(),0);
});
