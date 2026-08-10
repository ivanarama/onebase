package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestKeyboardShortcutsBehavior runs the real ui.js keydown handler in a
// small deterministic DOM harness. The assertions are behavioral on purpose:
// string-presence tests used to stay green while Ctrl+Enter sent an AI message
// and submitted the underlying form at the same time.
func TestKeyboardShortcutsBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the browser-side shortcut regression test")
	}

	dir := t.TempDir()
	uiPath := filepath.Join(dir, "ui.js")
	if err := os.WriteFile(uiPath, uiJS, 0o600); err != nil {
		t.Fatal(err)
	}

	const harness = `
const fs = require('fs');
const source = fs.readFileSync(process.argv[1], 'utf8');
const start = source.indexOf('function obFormActionButton');
const end = source.indexOf('\nfunction initTreeToggle', start);
if (start < 0 || end < 0) throw new Error('shortcut runtime slice not found');

function makeElement(tag, options = {}) {
  const attrs = Object.assign({}, options.attrs || {});
  return {
    nodeType: 1,
    tagName: String(tag || 'DIV').toUpperCase(),
    style: Object.assign({display: '', visibility: ''}, options.style || {}),
    parentElement: options.parentElement || null,
    disabled: !!options.disabled,
    isContentEditable: !!options.contentEditable,
    focusCount: 0,
    selectCount: 0,
    scrollCount: 0,
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    setAttribute(name, value) { attrs[name] = String(value); },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name); },
    querySelectorAll() { return []; },
    querySelector() { return null; },
    contains(other) { return other === this; },
    closest(selector) {
      if (selector.includes('table[data-ob-dom-table]')) return options.table || null;
      if (options.interactive && /a\[href\]|button|input|textarea|select|summary|contenteditable|role=/.test(selector)) return this;
      if (options.contentEditable && selector.includes('contenteditable')) return this;
      return null;
    },
    focus() { this.focusCount++; document.activeElement = this; },
    select() { this.selectCount++; },
    scrollIntoView() { this.scrollCount++; }
  };
}

const body = makeElement('body');
let rows = [];
let keydown = null;
let configPresent = false;
let modalID = '';
let saveClicks = 0;
let postCloseClicks = 0;
let domAddButton = null;
const search = makeElement('input');
const save = {disabled: false, click() { saveClicks++; }};
const postClose = {disabled: false, click() { postCloseClicks++; }};

global.window = {
  getComputedStyle(el) { return el.style; },
  _obActiveDOMTable: null
};
global.document = {
  body,
  activeElement: body,
  addEventListener(type, fn) { if (type === 'keydown') keydown = fn; },
  contains(el) { return !!el && !el.detached; },
  getElementById(id) {
    if (id === modalID) return {};
    if (id === 'ob-list-config' && configPresent) return {};
    return null;
  },
  querySelector(selector) {
    if (selector.includes('post_and_close')) return postClose;
    if (selector === 'button[name="_action"][value=""]') return save;
    if (selector === 'input[name="q"]') return search;
    return null;
  },
  querySelectorAll(selector) {
    if (selector === '[data-ob-list-row]') return rows;
    if (selector === '[data-ob-add-tp-row],[data-ob-add-tp]' && domAddButton) return [domAddButton];
    return [];
  }
};

let selected = null;
let activated = 0;
function listSel() { return selected; }
function listSetSel(row, options) { selected = row; if (options && options.focus) row.focus(); }
function listActivateRow() { activated++; }
function listOpen() { activated++; }
function recalcTpTotals() {}

eval(source.slice(start, end));
obInitKeyboardShortcuts();
if (typeof keydown !== 'function') throw new Error('keydown handler was not installed');

function fire(values = {}) {
  const event = Object.assign({
    key: '', code: '', ctrlKey: false, shiftKey: false, altKey: false,
    metaKey: false, defaultPrevented: false, target: body,
    prevented: false, stopped: false,
    preventDefault() { this.defaultPrevented = true; this.prevented = true; },
    stopPropagation() { this.stopped = true; }
  }, values);
  keydown(event);
  return event;
}
function assert(condition, message) { if (!condition) throw new Error(message); }

fire({key: 'Enter', ctrlKey: true, defaultPrevented: true});
assert(postCloseClicks === 0, 'defaultPrevented Ctrl+Enter submitted the form');
fire({key: 'Enter', ctrlKey: true});
assert(postCloseClicks === 1, 'exact Ctrl+Enter did not submit once');
fire({key: 'Enter', ctrlKey: true, shiftKey: true});
assert(postCloseClicks === 1, 'Ctrl+Shift+Enter was accepted as Ctrl+Enter');

modalID = '_ref-create-modal';
fire({code: 'KeyS', ctrlKey: true});
assert(saveClicks === 0, 'shortcut escaped the create-reference modal');
modalID = '';

selected = makeElement('tr');
fire({key: 'Enter', target: makeElement('a', {interactive: true})});
fire({key: 'Enter', target: makeElement('span', {contentEditable: true})});
fire({key: 'Enter', target: makeElement('button', {interactive: true})});
assert(activated === 0, 'list Enter hijacked an interactive target');

const first = makeElement('tr');
const hidden = makeElement('tr', {style: {display: 'none', visibility: ''}});
const last = makeElement('tr');
rows = [first, hidden, last];
selected = first;
fire({key: 'ArrowDown'});
assert(selected === last, 'ArrowDown selected a hidden tree descendant');
assert(last.focusCount === 1, 'keyboard selection did not move DOM focus');

configPresent = false;
fire({code: 'KeyF', ctrlKey: true});
assert(search.focusCount === 0, 'Ctrl+F hijacked a non-list page');
configPresent = true;
fire({code: 'KeyF', ctrlKey: true});
assert(search.focusCount === 1 && search.selectCount === 1, 'Ctrl+F did not focus list search');

// Exercise the real DOM-table implementation: it must commit/validate the
// active editor, copy values, and rewrite server-side row indexes after moves.
const tbody = {
  rows: [],
  appendChild(row) { this.rows.push(row); row.body = this; },
  insertBefore(row, before) {
    const old = this.rows.indexOf(row);
    if (old >= 0) this.rows.splice(old, 1);
    const at = before ? this.rows.indexOf(before) : -1;
    if (at < 0) this.rows.push(row); else this.rows.splice(at, 0, row);
    row.body = this;
  },
  querySelectorAll() { return []; }
};
const tableAttrs = {
  'data-ob-dom-table': 'Lines', 'data-ob-readonly': '0',
  'data-ob-element': 'LinesElement', 'data-ob-rowadd': '1', 'data-ob-rowdel': '1'
};
const rowEvents = [];
window.obFire = function(element, eventName, params) {
  rowEvents.push({element, eventName, params, values: tbody.rows.map(row => row.control.value)});
};
const table = {
  nodeType: 1,
  tBodies: [tbody],
  _obCurrentRow: null,
  getAttribute(name) { return Object.prototype.hasOwnProperty.call(tableAttrs, name) ? tableAttrs[name] : null; },
  contains(node) { return node === this || (node && node.table === this); },
  querySelector(selector) {
    if (selector === 'tbody ._tp-sel:checked' || selector.includes('[data-tp-num]')) return null;
    return null;
  }
};
function makeDOMRow(value, index) {
  const attrs = {};
  const row = {
    nodeType: 1, tagName: 'TR', table, body: tbody, style: {display: '', visibility: ''}, parentElement: table,
    get sectionRowIndex() { return tbody.rows.indexOf(this); },
    get nextSibling() { const at = tbody.rows.indexOf(this); return at >= 0 ? (tbody.rows[at + 1] || null) : null; },
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    setAttribute(name, val) { attrs[name] = String(val); },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name); },
    focus() { document.activeElement = this; },
    closest(selector) {
      if (selector.includes('table[data-ob-dom-table]')) return table;
      if (selector === 'tr') return this;
      return null;
    },
    remove() { const at = tbody.rows.indexOf(this); if (at >= 0) tbody.rows.splice(at, 1); },
    querySelectorAll(selector) {
      if (selector === '[name]' || selector.includes('input[name]')) return [this.control];
      if (selector.includes('input,select,textarea,button')) return [this.control, this.removeButton];
      return [];
    },
    querySelector(selector) {
      if (selector.includes('[data-ob-remove-row]') || selector.includes('.del-btn')) return this.removeButton;
      if (selector.startsWith('input:not')) return this.control;
      return null;
    }
  };
  const controlAttrs = {name: 'tp.Lines.' + index + '.Name'};
  row.control = {
    nodeType: 1, tagName: 'INPUT', type: 'text', value, disabled: false, table, row,
    valid: true, reports: 0,
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(controlAttrs, name) ? controlAttrs[name] : null; },
    setAttribute(name, val) { controlAttrs[name] = String(val); },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(controlAttrs, name); },
    checkValidity() { return this.valid; },
    reportValidity() { this.reports++; },
    blur() { document.activeElement = body; },
    focus() { document.activeElement = this; },
    closest(selector) {
      if (selector.includes('table[data-ob-dom-table]')) return table;
      if (selector === 'tr') return row;
      if (/a\[href\]|button|summary|contenteditable/.test(selector)) return null;
      if (/input|textarea|select/.test(selector)) return this;
      return null;
    }
  };
  row.removeButton = {
    tagName: 'BUTTON', disabled: false, table,
    click() { row.remove(); },
    setAttribute() {}
  };
  return row;
}
const rowA = makeDOMRow('A', 0);
const rowB = makeDOMRow('B', 1);
tbody.appendChild(rowA);
tbody.appendChild(rowB);
domAddButton = {
  disabled: false,
  getAttribute(name) {
    if (name === 'data-tp-name') return 'Lines';
    if (name === 'data-ob-add-tp') return null;
    return null;
  },
  click() { tbody.appendChild(makeDOMRow('', tbody.rows.length)); }
};

table._obCurrentRow = rowA;
window._obActiveDOMTable = table;
document.activeElement = rowA.control;
fire({key: 'ArrowDown', ctrlKey: true, target: rowA.control});
assert(tbody.rows[0] === rowB && tbody.rows[1] === rowA, 'Ctrl+Down did not move the current DOM row');
assert(rowB.control.getAttribute('name') === 'tp.Lines.0.Name' && rowA.control.getAttribute('name') === 'tp.Lines.1.Name', 'move did not reindex submitted field names');

fire({key: 'F9', target: rowA});
assert(tbody.rows.length === 3 && tbody.rows[2].control.value === 'A', 'F9 did not copy the current DOM row');
assert(tbody.rows[2].control.getAttribute('name') === 'tp.Lines.2.Name', 'copy did not assign contiguous submitted indexes');
assert(rowEvents.length === 1 && rowEvents[0].eventName === 'ПриДобавленииСтроки', 'F9 did not emit exactly one row-add event');
assert(rowEvents[0].values.join(',') === 'B,A,A', 'row-add event fired before copied values and order were committed');

const beforeInvalidInsert = tbody.rows.length;
document.activeElement = rowB.control;
rowB.control.valid = false;
fire({key: 'Insert', target: rowB.control});
assert(tbody.rows.length === beforeInvalidInsert && rowB.control.reports === 1, 'invalid active editor did not block Insert');
rowB.control.valid = true;

tableAttrs['data-ob-readonly'] = '1';
fire({key: 'Insert', target: rowB.control});
assert(tbody.rows.length === beforeInvalidInsert, 'readonly DOM table accepted Insert');
`

	cmd := exec.Command(node, "-e", harness, uiPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node shortcut behavior harness: %v\n%s", err, out)
	}
}

func TestManagedGridShortcutBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the browser-side shortcut regression test")
	}
	dir := t.TempDir()
	managedPath := filepath.Join(dir, "managed.js")
	if err := os.WriteFile(managedPath, managedJS, 0o600); err != nil {
		t.Fatal(err)
	}

	const harness = `
const fs = require('fs');
const source = fs.readFileSync(process.argv[1], 'utf8');
const start = source.indexOf('function gridNameFromTarget');
const end = source.indexOf('// SlickGrid-aware applyTableParts', start);
if (start < 0 || end < 0) throw new Error('managed shortcut runtime slice not found');

let keydown = null;
let modal = false;
let hotkeyButton = null;
const host = {getAttribute(name) { return name === 'data-sg-tp' ? 'Lines' : ''; }};
const gridTarget = {closest(selector) {
  if (selector === '.ob-grid[data-sg-tp]') return host;
  return null;
}};
const body = {tagName: 'BODY', closest() { return null; }};
const link = {tagName: 'A', closest(selector) {
  if (selector === '.ob-grid[data-sg-tp]') return null;
  if (selector.includes('a[href]')) return this;
  return null;
}};
global.window = {
  _obGrids: {Lines: {div: host, readOnly: false}},
  _obActiveGridName: '',
  addCount: 0, copyCount: 0, moves: [],
  obGridAddRow() { this.addCount++; },
  obGridCopyRow() { this.copyCount++; },
  obGridMoveRow(tp, delta) { this.moves.push([tp, delta]); }
};
global.document = {
  activeElement: body,
  addEventListener(type, fn, capture) { if (type === 'keydown' && capture === true) keydown = fn; },
  contains() { return true; },
  getElementById() { return null; },
  querySelectorAll(selector) { return selector === '[data-ob-hotkey]' && hotkeyButton ? [hotkeyButton] : []; }
};
global.obHasBlockingModal = () => modal;
eval(source.slice(start, end));
if (typeof keydown !== 'function') throw new Error('managed capture handler was not installed');

function fire(values = {}) {
  const event = Object.assign({
    key: '', ctrlKey: false, shiftKey: false, altKey: false, metaKey: false,
    defaultPrevented: false, target: gridTarget,
    preventDefault() { this.defaultPrevented = true; }, stopPropagation() { this.stopped = true; }
  }, values);
  keydown(event);
  return event;
}
function assert(condition, message) { if (!condition) throw new Error(message); }

fire({key: 'Insert'});
assert(window.addCount === 1, 'exact Insert did not add a SlickGrid row');
fire({key: 'Insert', shiftKey: true});
assert(window.addCount === 1, 'Shift+Insert was accepted as Insert');
fire({key: 'Insert', defaultPrevented: true});
assert(window.addCount === 1, 'defaultPrevented Insert reached SlickGrid');

modal = true;
fire({key: 'Insert'});
assert(window.addCount === 1, 'Insert escaped a blocking modal');
modal = false;

hotkeyButton = {
  disabled: false,
  getAttribute(name) { if (name === 'data-ob-hotkey') return ' F9 '; return null; }
};
fire({key: 'F9'});
assert(window.copyCount === 0, 'built-in F9 ignored a whitespace-padded explicit form hotkey');
hotkeyButton.disabled = true;
fire({key: 'F9'});
assert(window.copyCount === 1, 'disabled explicit hotkey incorrectly blocked built-in F9');

fire({key: 'ArrowDown', ctrlKey: true, shiftKey: true});
assert(window.moves.length === 0, 'Ctrl+Shift+Down was accepted as Ctrl+Down');
fire({key: 'ArrowDown', ctrlKey: true});
assert(window.moves.length === 1 && window.moves[0][1] === 1, 'exact Ctrl+Down did not move the row');

fire({key: 'Insert', target: link});
assert(window.addCount === 1, 'remembered grid hijacked a focused link');
window._obGrids.Lines.readOnly = true;
fire({key: 'Insert'});
assert(window.addCount === 1, 'readonly SlickGrid accepted Insert');
`

	cmd := exec.Command(node, "-e", harness, managedPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node managed shortcut behavior harness: %v\n%s", err, out)
	}
}
