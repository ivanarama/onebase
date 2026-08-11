const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const uiSource = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const managedSource = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');
const resolverStart = uiSource.indexOf('function obElementVisible');
const resolverEnd = uiSource.indexOf('\nfunction obListRows', resolverStart);
const domStart = uiSource.indexOf('function obHasActionableHotkey');
const domEnd = uiSource.indexOf('\nfunction obInitDOMTables', domStart);
const delegatesStart = managedSource.indexOf('function obManagedInitDelegates');
const delegatesEnd = managedSource.indexOf('\nobManagedReady(obManagedInitDelegates);', delegatesStart);
if (resolverStart < 0 || resolverEnd < 0 || domStart < 0 || domEnd < 0 || delegatesStart < 0 || delegatesEnd < 0) {
  throw new Error('hotkey production slices not found');
}

const listeners = new Map();
let mainForm = null;
let candidates = [];
let modal = false;

function element(tag, attrs = {}, options = {}) {
  const attributes = new Map(Object.entries(attrs).map(([name, value]) => [name, String(value)]));
  const node = {
    nodeType: 1,
    tagName: String(tag || 'div').toUpperCase(),
    parentElement: options.parent || null,
    style: Object.assign({display: '', visibility: ''}, options.style || {}),
    computedStyle: Object.assign({display: '', visibility: ''}, options.computedStyle || {}),
    hidden: options.hidden === true,
    disabled: options.disabled === true,
    inert: options.inert === true,
    clickCount: 0,
    getAttribute(name) { return attributes.has(name) ? attributes.get(name) : null; },
    setAttribute(name, value) { attributes.set(String(name), String(value)); },
    hasAttribute(name) { return attributes.has(String(name)); },
    contains(other) {
      for (let current = other; current; current = current.parentElement) {
        if (current === this) return true;
      }
      return false;
    },
    matches(selector) {
      if (selector !== ':disabled') return false;
      if (this.disabled) return true;
      for (let current = this.parentElement; current; current = current.parentElement) {
        if (current.tagName === 'FIELDSET' && (current.disabled || current.hasAttribute('disabled'))) return true;
      }
      return false;
    },
    querySelectorAll(selector) { return selector === '[data-ob-hotkey]' ? candidates : []; },
    closest() { return null; },
    click() { this.clickCount++; }
  };
  return node;
}

const body = element('body');
global.window = {
  _obActiveDOMTable: null,
  _obActiveGridName: '',
  getComputedStyle(node) { return node.computedStyle || node.style || {}; }
};
global.document = {
  body,
  addEventListener(type, listener) {
    if (!listeners.has(type)) listeners.set(type, []);
    listeners.get(type).push(listener);
  },
  contains(node) { return body.contains(node); },
  getElementById(id) {
    if (id === 'main-form') return mainForm;
    if (modal && (id === '_ref-picker-modal' || id === '_item-picker-modal' || id === '_ref-create-modal')) return {};
    return null;
  }
};
global.obHasBlockingModal = () => modal;
global.obIsInteractiveTarget = () => false;
global.obIsTypingTarget = () => false;

vm.runInThisContext(uiSource.slice(resolverStart, resolverEnd), {filename: 'ui-hotkey-slice.js'});
vm.runInThisContext(uiSource.slice(domStart, domEnd), {filename: 'ui-dom-hotkey-slice.js'});
vm.runInThisContext(managedSource.slice(delegatesStart, delegatesEnd), {filename: 'managed-hotkey-slice.js'});
obManagedInitDelegates();

function reset() {
  mainForm = element('form', {id: 'main-form'}, {parent: body});
  candidates = [];
  modal = false;
}

function button(options = {}) {
  const parent = options.parent === undefined ? mainForm : options.parent;
  const attrs = {'data-ob-hotkey': options.hotkey || ' F9 ', ...(options.attrs || {})};
  if (options.action !== false) attrs['data-ob-fire-click'] = options.actionName || 'Run';
  return element('button', attrs, {
    parent,
    style: options.style,
    computedStyle: options.computedStyle,
    hidden: options.hidden,
    disabled: options.disabled,
    inert: options.inert
  });
}

function nonActionableCandidates() {
  const ariaHiddenParent = element('div', {'aria-hidden': 'true'}, {parent: mainForm});
  const displayParent = element('div', {}, {parent: mainForm, style: {display: 'none'}});
  const computedDisplayParent = element('div', {}, {parent: mainForm, computedStyle: {display: 'none'}});
  const visibilityParent = element('div', {}, {parent: mainForm, style: {visibility: 'hidden'}});
  const computedVisibilityParent = element('div', {}, {parent: mainForm, computedStyle: {visibility: 'hidden'}});
  const disabledFieldset = element('fieldset', {disabled: ''}, {parent: mainForm, disabled: true});
  const ariaDisabledParent = element('div', {'aria-disabled': 'true'}, {parent: mainForm});
  const inertParent = element('div', {inert: ''}, {parent: mainForm, inert: true});
  return [
    ['own hidden', button({hidden: true})],
    ['handlerless', button({action: false})],
    ['ancestor aria-hidden', button({parent: ariaHiddenParent})],
    ['ancestor inline display', button({parent: displayParent})],
    ['ancestor computed display', button({parent: computedDisplayParent})],
    ['ancestor inline visibility', button({parent: visibilityParent})],
    ['ancestor computed visibility', button({parent: computedVisibilityParent})],
    ['own disabled', button({disabled: true})],
    ['disabled fieldset', button({parent: disabledFieldset})],
    ['own aria-disabled', button({attrs: {'aria-disabled': 'true'}})],
    ['ancestor aria-disabled', button({parent: ariaDisabledParent})],
    ['own inert', button({attrs: {inert: ''}, inert: true})],
    ['ancestor inert', button({parent: inertParent})],
    ['detached', button({parent: null})],
    ['outside main form', button({parent: body})]
  ];
}

function fire(values = {}) {
  const event = Object.assign({
    key: 'F9', code: 'F9', ctrlKey: false, altKey: false, metaKey: false, shiftKey: false,
    defaultPrevented: false, target: body,
    preventDefault() { this.defaultPrevented = true; }
  }, values);
  for (const listener of listeners.get('keydown') || []) listener(event);
  return event;
}

function assertRejected(name, candidate) {
  candidates = [candidate];
  const event = fire();
  assert.equal(candidate.clickCount, 0, name + ': nonactionable candidate was clicked');
  assert.equal(event.defaultPrevented, false, name + ': nonactionable candidate consumed F9');
  assert.equal(window.obResolveActionableFormHotkey('F9'), null, name + ': resolver returned candidate');
}

test('generic bubble dispatch uses the shared actionable resolver', () => {
  reset();
  const visible = button();
  candidates = [visible];
  const event = fire();
  assert.equal(event.defaultPrevented, true);
  assert.equal(visible.clickCount, 1);

  for (const [name, candidate] of nonActionableCandidates()) assertRejected(name, candidate);

  const hiddenFirst = button({hidden: true});
  const visibleSecond = button();
  candidates = [hiddenFirst, visibleSecond];
  fire();
  assert.equal(hiddenFirst.clickCount, 0);
  assert.equal(visibleSecond.clickCount, 1, 'hidden first candidate blocked visible second candidate');
});

test('generic bubble keeps event, modal, target and main-form guards', () => {
  reset();
  const candidate = button();
  candidates = [candidate];

  fire({defaultPrevented: true});
  assert.equal(candidate.clickCount, 0, 'defaultPrevented event was dispatched');

  modal = true;
  fire();
  assert.equal(candidate.clickCount, 0, 'modal did not block dispatch');
  modal = false;

  const outsideTarget = element('input', {}, {parent: body});
  fire({target: outsideTarget});
  assert.equal(candidate.clickCount, 0, 'target outside main form dispatched a hotkey');

  mainForm = null;
  fire();
  assert.equal(candidate.clickCount, 0, 'missing main form dispatched a hotkey');
  assert.equal(window.obResolveActionableFormHotkey('F9'), null);
});

test('real no_grid handler and generic bubble give F9 to exactly one action', () => {
  reset();
  let copies = 0;
  obDOMCopyRow = function () { copies++; };
  const table = element('table', {'data-ob-dom-table': 'Lines', 'data-ob-readonly': '0'}, {parent: mainForm});
  const target = element('tr', {}, {parent: table});
  target.closest = function (selector) {
    if (selector === 'table[data-ob-dom-table]') return table;
    return null;
  };

  function dispatchDOM() {
    const event = {
      key: 'F9', code: 'F9', ctrlKey: false, altKey: false, metaKey: false, shiftKey: false,
      defaultPrevented: false, target,
      preventDefault() { this.defaultPrevented = true; },
      stopPropagation() { this.stopped = true; }
    };
    obHandleDOMTableShortcut(event);
    for (const listener of listeners.get('keydown') || []) listener(event);
    return event;
  }

  const visible = button();
  candidates = [visible];
  dispatchDOM();
  assert.equal(copies, 0, 'visible form hotkey did not suppress no_grid copy');
  assert.equal(visible.clickCount, 1, 'visible form hotkey was not clicked exactly once');

  for (const [name, candidate] of nonActionableCandidates()) {
    candidates = [candidate];
    const before = copies;
    dispatchDOM();
    assert.equal(copies, before + 1, name + ': no_grid F9 did not copy exactly once');
    assert.equal(candidate.clickCount, 0, name + ': no_grid fallback clicked a nonactionable candidate');
  }
  const copiesAfterFallbacks = copies;

  const hiddenFirst = button({hidden: true});
  const visibleSecond = button();
  candidates = [hiddenFirst, visibleSecond];
  dispatchDOM();
  assert.equal(copies, copiesAfterFallbacks, 'hidden-first/visible-second did not suppress no_grid copy');
  assert.equal(hiddenFirst.clickCount, 0);
  assert.equal(visibleSecond.clickCount, 1, 'visible second form hotkey was not clicked exactly once');

  const connectedForm = mainForm;
  mainForm = null;
  dispatchDOM();
  assert.equal(copies, copiesAfterFallbacks + 1, 'missing main form did not fall back to one no_grid copy');
  mainForm = connectedForm;
});
