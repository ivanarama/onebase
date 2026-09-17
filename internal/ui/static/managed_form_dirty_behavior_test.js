'use strict';

// The Go test supplies actual HTTP form-event responses and verifies persisted
// values. Run the entire browser runtime, including obFire and beforeunload.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');
const fixtures = JSON.parse(fs.readFileSync(process.env.ONEBASE_FORM_DIRTY_FIXTURES, 'utf8'));

function browser(fixture) {
  const docListeners = new Map();
  const winListeners = new Map();
  const controls = {};
  function element() {
    return {style: {}, children: [], remove() {}, appendChild(child) { this.children.push(child); return child; }};
  }
  const banner = element();
  const form = {
    id: 'main-form',
    getAttribute() { return null; },
    querySelectorAll() { return []; },
    querySelector(selector) {
      const match = selector.match(/^\[name="([^"]+)"\]$/);
      return match ? controls[match[1]] || null : null;
    },
    appendChild(el) { controls[el.name] = el; return el; }
  };
  for (const [name, value] of Object.entries({Наименование: 'saved', _id: fixture.id, _version: '1'})) {
    controls[name] = {
      name, value, tagName: 'INPUT', type: 'text', disabled: false,
      closest(selector) { return selector === '#main-form' ? form : null; },
      getAttribute() { return null; }
    };
  }
  function listen(map, type, fn) {
    if (!map.has(type)) map.set(type, []);
    map.get(type).push(fn);
  }
  const document = {
    readyState: 'loading', title: 'Обращение', activeElement: null,
    head: element(), body: element(),
    addEventListener(type, fn) { listen(docListeners, type, fn); },
    querySelector(selector) {
      if (selector.startsWith('#main-form ')) return form.querySelector(selector.slice(11));
      return null;
    },
    querySelectorAll() { return []; },
    createElement: element,
    getElementById(id) {
      if (id === 'main-form') return form;
      if (id === 'ob-fmevt-banner') return banner;
      if (id === 'ob-managed-config') return {textContent: JSON.stringify({url: '/form-event', docId: fixture.id})};
      return null;
    }
  };
  class FormData {
    constructor() { this.values = new Map(Object.values(controls).map(el => [el.name, el.value])); }
    set(name, value) { this.values.set(name, value); }
    forEach(fn) { this.values.forEach(fn); }
  }
  const requests = [];
  const location = {pathname: '/ui/catalog/Обращение/' + (fixture.id || 'new')};
  const context = {
    document, FormData, URLSearchParams, location,
    history: {replaceState(_state, _title, url) { location.pathname = url; }},
    CSS: {escape: value => value}, console,
    sessionStorage: {getItem() { return null; }, setItem() {}, removeItem() {}},
    setTimeout() {}, clearTimeout() {},
    addEventListener(type, fn) { listen(winListeners, type, fn); },
    async fetch(url, options) {
      requests.push({url, body: new URLSearchParams(options.body)});
      return {ok: true, async json() { return fixture.response; }};
    }
  };
  context.window = context;
  vm.runInNewContext(source, context, {filename: 'managed.js'});
  return {context, document, controls, requests, banner,
    input() { for (const fn of docListeners.get('input') || []) fn({target: controls.Наименование}); },
    unload() {
      const event = {prevented: false, preventDefault() { this.prevented = true; }};
      for (const fn of winListeners.get('beforeunload') || []) fn(event);
      return event.prevented;
    }
  };
}

for (const fixture of fixtures) {
  for (const initialDirty of [false, true]) {
    test(`${fixture.name}, initially ${initialDirty ? 'dirty' : 'clean'}`, async () => {
      const b = browser(fixture);
      if (initialDirty) b.input();
      assert.equal(b.context._obFormDirty, initialDirty);
      await b.context.obFire('КнопкаТест', 'Нажатие');
      assert.deepEqual(b.banner.children.map(el => el.children[0].textContent),
        [...(fixture.response.messages || []), ...(fixture.response.error ? [fixture.response.error] : [])],
        'the runtime must apply the response without a client-side error');
      assert.equal(b.requests.length, 1);
      assert.equal(b.requests[0].body.get('Наименование'), 'saved');
      assert.equal(b.controls.Наименование.value, fixture.value);
      const dirty = fixture.noWrite ? initialDirty : fixture.dirty;
      assert.equal(b.context._obFormDirty, dirty, 'unsaved state after the handler');
      assert.equal(b.document.title, dirty ? '● Обращение' : 'Обращение');
      assert.equal(b.unload(), dirty, 'closing the form must warn about unsaved changes');
      assert.equal(b.controls._version.value, fixture.noWrite ? '1' : String(fixture.response.version));
      assert.equal(b.controls._id.value, fixture.response.savedId || fixture.id);
      if (fixture.response.savedId) assert.ok(b.context.location.pathname.endsWith('/' + fixture.response.savedId));
    });
  }
}
