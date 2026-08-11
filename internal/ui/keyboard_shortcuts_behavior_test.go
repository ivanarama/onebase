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
	managedPath := filepath.Join(dir, "managed.js")
	if err := os.WriteFile(managedPath, managedJS, 0o600); err != nil {
		t.Fatal(err)
	}

	const harness = `
const fs = require('fs');
const source = fs.readFileSync(process.argv[1], 'utf8');
const managedSource = fs.readFileSync(process.argv[2], 'utf8');
const start = source.indexOf('var _listSel = null;');
const end = source.indexOf('\nfunction obInitFeed', start);
const dirtyStart = source.indexOf('function obInitFormDirty()');
const dirtyEnd = source.indexOf('\nobReady(obInitFormDirty);', dirtyStart);
const addTpStart = source.indexOf('function addTpRow');
const addTpEnd = source.indexOf('\nfunction recalcTpRow', addTpStart);
const managedMutationStart = managedSource.indexOf('window.obFireRowEventChain = function');
const managedKeysStart = managedSource.indexOf('function gridNameFromTarget');
const managedKeysEnd = managedSource.indexOf('// SlickGrid-aware applyTableParts', managedKeysStart);
const managedBodiesStart = managedSource.indexOf('function obManagedTableReadOnly');
const managedBodiesEnd = managedSource.indexOf('// Отправляет текущие form-values', managedBodiesStart);
const managedApplyStart = managedSource.indexOf('function applyTableParts');
const managedApplyExport = managedSource.indexOf('window.applyTableParts = applyTableParts;', managedApplyStart);
if (start < 0 || end < 0 || dirtyStart < 0 || dirtyEnd < 0 || addTpStart < 0 || addTpEnd < 0 ||
    managedMutationStart < 0 || managedKeysStart < 0 || managedKeysEnd < 0 ||
    managedBodiesStart < 0 || managedBodiesEnd < 0 || managedApplyStart < 0 || managedApplyExport < 0) throw new Error('shortcut runtime slice not found');

function makeElement(tag, options = {}) {
  const attrs = Object.assign({}, options.attrs || {});
  const classes = new Set(options.classes || []);
  const element = {
    nodeType: 1,
    tagName: String(tag || 'DIV').toUpperCase(),
    style: Object.assign({display: '', visibility: ''}, options.style || {}),
    parentElement: options.parentElement || null,
    dataset: Object.assign({}, options.dataset || {}),
    disabled: !!options.disabled,
    isContentEditable: !!options.contentEditable,
    focusCount: 0,
    selectCount: 0,
    scrollCount: 0,
    children: [],
    classList: {
      toggle(name, on) { if (on) classes.add(name); else classes.delete(name); },
      contains(name) { return classes.has(name); }
    },
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    setAttribute(name, value) { attrs[name] = String(value); },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name); },
    querySelectorAll() { return []; },
    querySelector() { return null; },
    appendChild(child) { this.children.push(child); child.parentElement = this; return child; },
    contains(other) { return other === this; },
    closest(selector) {
      if (selector.includes('table[data-ob-dom-table]')) return options.table || null;
      if (selector === '.ob-grid[data-sg-tp]') return options.slickGrid || null;
      if (selector === '[data-ob-list-row]' && options.listRow) return this;
      if (options.interactive && /a\[href\]|button|input|textarea|select|summary|contenteditable|role=/.test(selector)) return this;
      if (options.contentEditable && selector.includes('contenteditable')) return this;
      return null;
    },
    focus() { this.focusCount++; document.activeElement = this; dispatch('focusin', {target: this}); },
    select() { this.selectCount++; },
    scrollIntoView() { this.scrollCount++; }
  };
  return element;
}

const body = makeElement('body');
let rows = [];
const listeners = {};
let configPresent = false;
let modalID = '';
let saveClicks = 0;
let postCloseClicks = 0;
let domAddButtons = [];
let dynamicTbody = null;
let domTableForQuery = null;
let dirtyFormEnabled = false;
let tablePartJSONInputs = [];
let closeClicks = 0;
let confirmResult = true;
let activated = 0;
let confirmCalls = 0;
const submittedForms = [];
const listConfig = {labels: {}, canDelete: false};
const globalSearch = makeElement('input');
const listSearch = makeElement('input');
const save = {disabled: false, click() { saveClicks++; }};
const postClose = {disabled: false, click() { postCloseClicks++; }};
const dirtyForm = {addEventListener() {}};
const closeForm = {click() { closeClicks++; }};

function dispatch(type, event) {
  (listeners[type] || []).slice().forEach(fn => fn(event));
}
body.appendChild = function(node) { node.parentElement = body; };

global.window = {
  getComputedStyle(el) { return el && el.style ? el.style : {}; },
  _obActiveDOMTable: null,
  _obActiveGridName: '',
  _obGrids: {},
  location: {href: ''},
  obOpenInShell() { activated++; return true; },
  addEventListener(type, fn) { (listeners[type] || (listeners[type] = [])).push(fn); }
};
global.document = {
  body,
  activeElement: body,
  title: 'Form',
  addEventListener(type, fn) { (listeners[type] || (listeners[type] = [])).push(fn); },
  contains(el) { return !!el && !el.detached; },
  getElementsByName(name) { return name === 'tp_json.ReadonlyLines' ? tablePartJSONInputs : []; },
  getElementById(id) {
    if (id === modalID) return {};
    if (id === 'ob-list-config' && configPresent) return {};
    if (id === 'ob-list-search') return listSearch;
    if (id === 'tp-body-Dynamic') return dynamicTbody;
    return null;
  },
  querySelector(selector) {
    if (selector.includes('post_and_close')) return postClose;
    if (selector === 'button[name="_action"][value=""]') return save;
    if (selector === 'input[name="q"]') return globalSearch;
    if (selector === '#main-form[data-ob-dirty-watch="1"]' && dirtyFormEnabled) return dirtyForm;
    if (selector === '[data-ob-popup-cancel], [data-ob-close-tab], a.btn-cancel') return closeForm;
    return null;
  },
  querySelectorAll(selector) {
    if (selector === '[data-ob-list-row]') return rows;
    if (selector === '[data-ob-add-tp-row],[data-ob-add-tp]') return domAddButtons;
    if (selector === 'table[data-ob-dom-table]' && domTableForQuery) return [domTableForQuery];
    return [];
  },
  createElement(tag) {
    if (String(tag).toLowerCase() === 'form') {
      return {method: '', action: '', parentElement: null, submit() { submittedForms.push({method: this.method, action: this.action}); }};
    }
    return makeElement(tag);
  }
};

global.confirm = function() { confirmCalls++; return confirmResult; };
function obReadJSONScript(id, fallback) { return id === 'ob-list-config' && configPresent ? listConfig : fallback; }
function recalcTpTotals() {}
function updateTotals() {}
function obFireRowEvent() {}
window.obFireRowEvent = obFireRowEvent;

eval(source.slice(start, end));
eval(managedSource.slice(managedBodiesStart, managedBodiesEnd));
eval(managedSource.slice(managedApplyStart, managedApplyExport + 'window.applyTableParts = applyTableParts;'.length));
// managed.js is loaded at the bottom of a managed form and installs its
// capture listener before ui.js's DOM-ready shortcut listener.
eval(managedSource.slice(managedMutationStart, managedKeysStart));
eval(managedSource.slice(managedKeysStart, managedKeysEnd));
obInitKeyboardShortcuts();
if (!listeners.keydown || listeners.keydown.length < 2) throw new Error('keydown handlers were not installed');

function fire(values = {}) {
  const event = Object.assign({
    key: '', code: '', ctrlKey: false, shiftKey: false, altKey: false,
    metaKey: false, defaultPrevented: false, target: body,
    prevented: false, stopped: false,
    preventDefault() { this.defaultPrevented = true; this.prevented = true; },
    stopPropagation() { this.stopped = true; }
  }, values);
  dispatch('keydown', event);
  return event;
}
function assert(condition, message) { if (!condition) throw new Error(message); }

tablePartJSONInputs = [{value: 'old'}, {value: 'stale-duplicate'}];
window.applyTableParts({ReadonlyLines: [{Name: 'event-row'}]});
assert(tablePartJSONInputs.every(input => input.value === '[{"Name":"event-row"}]'),
  'form-event response did not synchronize every readonly no-grid JSON mirror');

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

const interactiveRow = makeElement('tr', {listRow: true, dataset: {openUrl: '/row'}});
rows = [interactiveRow];
listSetSel(interactiveRow);
fire({key: 'Enter', target: makeElement('a', {interactive: true})});
fire({key: 'Enter', target: makeElement('span', {contentEditable: true})});
fire({key: 'Enter', target: makeElement('button', {interactive: true})});
assert(activated === 0, 'list Enter hijacked an interactive target');

const first = makeElement('tr', {listRow: true, dataset: {openUrl: '/first'}});
const hidden = makeElement('tr', {listRow: true, style: {display: 'none', visibility: ''}, dataset: {openUrl: '/hidden'}});
const last = makeElement('tr', {listRow: true, dataset: {openUrl: '/last'}});
rows = [first, hidden, last];
listSetSel(first);
fire({key: 'ArrowDown'});
assert(listSel() === last, 'ArrowDown selected a hidden tree descendant');
assert(last.focusCount === 1, 'keyboard selection did not move DOM focus');

// Browser Tab focuses the first tabindex=0 row. focusin must synchronize the
// selection before Enter/F2 and establish ArrowDown's starting point.
const tabFirst = makeElement('tr', {listRow: true, dataset: {openUrl: '/tab-first'}});
const tabSecond = makeElement('tr', {listRow: true, dataset: {openUrl: '/tab-second'}});
rows = [tabFirst, tabSecond];
listSetSel(null);
tabFirst.focus();
assert(listSel() === tabFirst && tabFirst.getAttribute('aria-selected') === 'true', 'Tab focus did not synchronize list selection');
const beforeEnter = activated;
fire({key: 'Enter', target: tabFirst});
fire({key: 'F2', target: tabFirst});
assert(activated === beforeEnter + 2, 'Tab -> Enter/F2 did not open the focused row');
fire({key: 'ArrowDown', target: tabFirst});
assert(listSel() === tabSecond, 'ArrowDown repeated the focused first row instead of moving to the second');

configPresent = false;
fire({code: 'KeyF', ctrlKey: true});
assert(globalSearch.focusCount === 0 && listSearch.focusCount === 0, 'Ctrl+F hijacked a non-list page');
configPresent = true;
fire({code: 'KeyF', ctrlKey: true});
assert(listSearch.focusCount === 1 && listSearch.selectCount === 1, 'Ctrl+F did not focus list search');
assert(globalSearch.focusCount === 0 && globalSearch.selectCount === 0, 'Ctrl+F focused the global q input instead of list search');

listConfig.canDelete = true;
const predefined = makeElement('tr', {listRow: true, dataset: {predefined: '1', markUrl: '/mark-predefined'}});
rows = [predefined];
listSetSel(null);
predefined.focus();
const predefinedDelete = fire({key: 'Delete', target: predefined});
assert(!predefinedDelete.prevented && confirmCalls === 0 && submittedForms.length === 0, 'predefined Delete reached prevent/confirm/network');
const missingURL = makeElement('tr', {listRow: true, dataset: {predefined: '', markUrl: ''}});
rows = [missingURL];
missingURL.focus();
fire({key: 'Delete', target: missingURL});
assert(confirmCalls === 0 && submittedForms.length === 0, 'Delete without endpoint reached confirm/network');
const regular = makeElement('tr', {listRow: true, dataset: {predefined: '', markUrl: '/mark-regular'}});
rows = [regular];
regular.focus();
fire({key: 'Delete', target: regular});
assert(confirmCalls === 1 && submittedForms.length === 1 && submittedForms[0].action === '/mark-regular', 'regular Delete did not submit exactly once');

// Exercise the real DOM-table implementation: it must commit/validate the
// active editor, copy values, and rewrite server-side row indexes after moves.
const tbody = {
  rows: [],
  appendChild(row) { this.rows.push(row); row.body = this; row.parentElement = this; },
  insertBefore(row, before) {
    const old = this.rows.indexOf(row);
    if (old >= 0) this.rows.splice(old, 1);
    const at = before ? this.rows.indexOf(before) : -1;
    if (at < 0) this.rows.push(row); else this.rows.splice(at, 0, row);
    row.body = this;
    row.parentElement = this;
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
  style: {display: '', visibility: ''},
  parentElement: body,
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
    nodeType: 1, tagName: 'TR', table, body: tbody, style: {display: '', visibility: ''}, parentElement: tbody,
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
    remove() { const at = tbody.rows.indexOf(this); if (at >= 0) tbody.rows.splice(at, 1); this.parentElement = null; },
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
domTableForQuery = table;
obInitDOMTables();
const domAddButton = {
  disabled: false,
  getAttribute(name) {
    if (name === 'data-tp-name') return 'Lines';
    if (name === 'data-ob-add-tp') return null;
    return null;
  },
  click() { tbody.appendChild(makeDOMRow('', tbody.rows.length)); }
};
const readonlyDuplicateAddButton = {
  disabled: true,
  getAttribute(name) {
    if (name === 'data-tp-name') return 'Lines';
    if (name === 'data-ob-add-tp') return null;
    return null;
  },
  click() { throw new Error('readonly duplicate add button was clicked'); }
};

// A readonly NoGrid duplicate must not shadow the writable add control. Both
// Insert and F9 must work regardless of the two placements' DOM order.
for (const order of ['readonly-first', 'writable-first']) {
  domAddButtons = order === 'readonly-first'
    ? [readonlyDuplicateAddButton, domAddButton]
    : [domAddButton, readonlyDuplicateAddButton];
  table._obCurrentRow = rowA;
  document.activeElement = rowA.control;
  const beforeInsert = tbody.rows.length;
  fire({key: 'Insert', target: rowA});
  assert(tbody.rows.length === beforeInsert + 1, 'Insert failed with duplicate add buttons (' + order + ')');
  let inserted = tbody.rows.find(row => row !== rowA && row !== rowB);
  inserted.parentElement = null;
  tbody.rows.splice(tbody.rows.indexOf(inserted), 1);
  obDOMReindex(table);

  table._obCurrentRow = rowA;
  document.activeElement = rowA.control;
  const beforeCopy = tbody.rows.length;
  fire({key: 'F9', target: rowA});
  assert(tbody.rows.length === beforeCopy + 1, 'F9 failed with duplicate add buttons (' + order + ')');
  const copied = tbody.rows.find(row => row !== rowA && row !== rowB);
  assert(copied && copied.control.value === 'A', 'F9 copied the wrong row with duplicate add buttons (' + order + ')');
  copied.parentElement = null;
  tbody.rows.splice(tbody.rows.indexOf(copied), 1);
  obDOMReindex(table);
}
rowEvents.length = 0;
domAddButtons = [domAddButton];

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

// A concrete SlickGrid target must never fall back to the remembered no-grid
// table. Exercise all structural keys against the real DOM mutation code.
let mixedItems = [{id: 0, _ord: 0, Name: 'slick-A'}];
const mixedDataView = {
  getItems() { return mixedItems; },
  getItem(index) { return mixedItems[index]; },
  addItem(item) { mixedItems.push(item); },
  setItems(items) { mixedItems = items; },
  deleteItem(id) { mixedItems = mixedItems.filter(item => item.id !== id); },
  getRowById(id) { const index = mixedItems.findIndex(item => item.id === id); return index < 0 ? undefined : index; }
};
const mixedGrid = {
  getEditorLock() { return {isActive() { return false; }}; },
  getActiveCell() { return {row: 0, cell: 0}; },
  setActiveCell() {}, scrollRowIntoView() {}, editActiveCell() {}, invalidate() {},
  getSelectionModel() { return null; }
};
const slickHost = {
  nodeType: 1, style: {display: '', visibility: ''}, parentElement: body,
  getAttribute(name) { return name === 'data-sg-tp' ? 'SlickLines' : null; }
};
window._obGrids.SlickLines = {
  grid: mixedGrid, dataView: mixedDataView, div: slickHost, readOnly: false,
  columns: [{id: 'Name'}], columnsMeta: [{id: 'Name'}]
};
const slickTarget = makeElement('div', {slickGrid: slickHost});
rows = [];
listSetSel(null);
listConfig.canDelete = false;
const beforeForeignGrid = tbody.rows.map(row => row.control.value).join(',');
const beforeForeignEvents = rowEvents.length;
fire({key: 'Insert', target: slickTarget});
fire({key: 'F9', target: slickTarget});
fire({key: 'Delete', target: slickTarget});
assert(tbody.rows.map(row => row.control.value).join(',') === beforeForeignGrid && rowEvents.length === beforeForeignEvents,
  'SlickGrid target mutated the remembered DOM table via Insert/F9/Delete');

// Real listener order on a mixed managed form: Slick A becomes active first,
// then a mousedown in DOM table B must retire A before focus falls into a
// neutral sink. Structural keys may keep operating on B, but never on stale A.
mixedItems = [{id: 0, _ord: 0, Name: 'slick-A'}];
fire({key: 'F8', target: slickTarget});
assert(window._obActiveGridName === 'SlickLines' && window._obActiveDOMTable === null,
  'direct Slick context did not become the exclusive active table');
document.activeElement = rowA;
dispatch('mousedown', {target: rowA});
assert(window._obActiveGridName === '' && window._obActiveDOMTable === table,
  'DOM activation left the stale Slick marker alive');
const slickBeforeNeutral = mixedItems.map(item => item.Name).join(',');
document.activeElement = body;
fire({key: 'Insert', target: body});
fire({key: 'F9', target: body});
fire({key: 'Delete', target: body});
assert(mixedItems.map(item => item.Name).join(',') === slickBeforeNeutral,
  'Slick A changed after Slick A -> DOM B -> neutral listener sequence');

// Header/footer rows are not data rows. Clicking either clears the current
// tbody row, so F9/Delete cannot copy or remove a stale body row.
function makeSectionRow(section) {
  return {
    nodeType: 1, tagName: 'TR', table, parentElement: section,
    closest(selector) {
      if (selector.includes('table[data-ob-dom-table]')) return table;
      if (selector === 'tr') return this;
      return null;
    }
  };
}
const headerRow = makeSectionRow({tagName: 'THEAD'});
const footerRow = makeSectionRow({tagName: 'TFOOT'});
for (const nonDataRow of [headerRow, footerRow]) {
  document.activeElement = nonDataRow;
  dispatch('mousedown', {target: nonDataRow});
  const before = tbody.rows.map(row => row.control.value).join(',');
  fire({key: 'F9', target: nonDataRow});
  fire({key: 'Delete', target: nonDataRow});
  assert(tbody.rows.map(row => row.control.value).join(',') === before,
    'thead/tfoot row became the current mutable DOM row');
}

// The same operations continue to work for a real tbody row.
document.activeElement = rowA;
dispatch('mousedown', {target: rowA});
const beforeBodyCopy = tbody.rows.length;
fire({key: 'F9', target: rowA});
assert(tbody.rows.length === beforeBodyCopy + 1, 'tbody F9 stopped copying rows');
const copiedBodyRow = table._obCurrentRow;
document.activeElement = copiedBodyRow;
fire({key: 'Delete', target: copiedBodyRow});
assert(tbody.rows.length === beforeBodyCopy, 'tbody Delete stopped removing rows');

const beforeInvalidInsert = tbody.rows.length;
document.activeElement = rowB.control;
rowB.control.valid = false;
fire({key: 'Insert', target: rowB.control});
assert(tbody.rows.length === beforeInvalidInsert && rowB.control.reports === 1, 'invalid active editor did not block Insert');
rowB.control.valid = true;

tableAttrs['data-ob-readonly'] = '1';
fire({key: 'Insert', target: rowB.control});
assert(tbody.rows.length === beforeInvalidInsert, 'readonly DOM table accepted Insert');

tableAttrs['data-ob-readonly'] = '0';
table.style.display = 'none';
window._obActiveDOMTable = table;
fire({key: 'Insert', target: body});
fire({key: 'Insert', target: rowB.control});
assert(tbody.rows.length === beforeInvalidInsert, 'hidden remembered/direct DOM table accepted Insert');
assert(window._obActiveDOMTable === null, 'hidden DOM table remained remembered');

// Live replacement always recreates a roving Tab stop. It restores focus only
// when focus actually lived inside the replaced selected list.
let liveRows = [];
let replacementRows = [];
const live = {
  contains(node) { return liveRows.includes(node); },
  querySelectorAll(selector) { return selector === '[data-ob-list-row]' ? liveRows : []; }
};
Object.defineProperty(live, 'innerHTML', {
  get() { return 'live-html'; },
  set() {
    liveRows.forEach(row => { row.detached = true; });
    liveRows = replacementRows;
    rows = liveRows;
  }
});
const fresh = {innerHTML: 'fresh-html'};

listSetSel(null);
document.activeElement = body;
liveRows = [makeElement('tr', {listRow: true, dataset: {openUrl: '/old'}})];
rows = liveRows;
replacementRows = [
  makeElement('tr', {listRow: true, dataset: {openUrl: '/new-first'}}),
  makeElement('tr', {listRow: true, dataset: {openUrl: '/new-second'}})
];
obReplaceLiveListContents(live, fresh);
assert(replacementRows[0].getAttribute('tabindex') === '0' && replacementRows[1].getAttribute('tabindex') === '-1',
  'refresh without selection lost the first roving tabindex');
assert(document.activeElement === body, 'refresh without selection stole DOM focus');

const oldSelected = makeElement('tr', {listRow: true, attrs: {'data-open-url': '/same'}, dataset: {openUrl: '/same'}});
const newSelected = makeElement('tr', {listRow: true, attrs: {'data-open-url': '/same'}, dataset: {openUrl: '/same'}});
liveRows = [oldSelected];
rows = liveRows;
replacementRows = [newSelected];
listSetSel(oldSelected);
oldSelected.focus();
obReplaceLiveListContents(live, fresh);
assert(listSel() === newSelected && newSelected.getAttribute('aria-selected') === 'true', 'refresh did not restore selected row');
assert(document.activeElement === newSelected && newSelected.focusCount === 1, 'refresh did not restore focus from the selected list');

const oldMissing = makeElement('tr', {listRow: true, attrs: {'data-open-url': '/gone'}, dataset: {openUrl: '/gone'}});
const newFirst = makeElement('tr', {listRow: true, attrs: {'data-open-url': '/fresh-first'}, dataset: {openUrl: '/fresh-first'}});
const newSecond = makeElement('tr', {listRow: true, attrs: {'data-open-url': '/fresh-second'}, dataset: {openUrl: '/fresh-second'}});
liveRows = [oldMissing];
rows = liveRows;
replacementRows = [newFirst, newSecond];
listSetSel(oldMissing);
oldMissing.focus();
obReplaceLiveListContents(live, fresh);
assert(document.activeElement === newFirst && newFirst.getAttribute('tabindex') === '0',
  'refresh with a vanished focused selection did not focus a valid fresh row');

const oldOutside = makeElement('tr', {listRow: true, attrs: {'data-open-url': '/outside'}, dataset: {openUrl: '/outside'}});
const newOutside = makeElement('tr', {listRow: true, attrs: {'data-open-url': '/outside'}, dataset: {openUrl: '/outside'}});
liveRows = [oldOutside];
rows = liveRows;
replacementRows = [newOutside];
listSetSel(oldOutside);
document.activeElement = body;
obReplaceLiveListContents(live, fresh);
assert(listSel() === newOutside && newOutside.focusCount === 0 && document.activeElement === body,
  'refresh stole outside focus while restoring selection');

// Autogen entity forms implement the documented Escape close contract with
// the same dirty confirmation and leave picker modals alone.
eval(source.slice(dirtyStart, dirtyEnd));
dirtyFormEnabled = true;
obInitFormDirty();
const closeBefore = closeClicks;
const confirmsBefore = confirmCalls;
window._obFormDirty = false;
fire({key: 'Escape', defaultPrevented: true});
assert(closeClicks === closeBefore, 'defaultPrevented Escape closed the autogen form');
fire({key: 'Escape'});
assert(closeClicks === closeBefore + 1 && confirmCalls === confirmsBefore, 'clean autogen Escape did not close directly');
window._obFormDirty = true;
confirmResult = false;
const denied = fire({key: 'Escape'});
assert(closeClicks === closeBefore + 1 && confirmCalls === confirmsBefore + 1 && denied.prevented,
  'dirty autogen Escape ignored a rejected confirmation');
confirmResult = true;
fire({key: 'Escape'});
assert(closeClicks === closeBefore + 2 && confirmCalls === confirmsBefore + 2,
  'dirty autogen Escape did not close after confirmation');
modalID = '_ref-picker-modal';
window._obFormDirty = false;
fire({key: 'Escape'});
assert(closeClicks === closeBefore + 2, 'autogen Escape closed the form behind a picker modal');
modalID = '';

// Dynamic auto-form rows use the same availability contract: a writable
// table advertises Delete, a readonly table does not expose a dead shortcut.
let dynamicReadOnly = '0';
const dynamicTable = {
  nodeType: 1, style: {display: '', visibility: ''}, parentElement: body,
  tBodies: [], _obCurrentRow: null,
  getAttribute(name) {
    if (name === 'data-ob-dom-table') return 'Dynamic';
    if (name === 'data-ob-readonly') return dynamicReadOnly;
    return null;
  },
  contains(node) { return node === this || (node && node.dynamicTable === this); },
  querySelector() { return null; }
};
dynamicTbody = {
  rows: [],
  getAttribute() { return null; },
  closest(selector) { return selector.includes('table[data-ob-dom-table]') ? dynamicTable : null; },
  appendChild(row) { this.rows.push(row); row.dynamicTable = dynamicTable; row.parentElement = this; }
};
dynamicTable.tBodies = [dynamicTbody];
function obTPRefOpts() { return {}; }
function obTPRefMeta() { return {}; }
eval(source.slice(addTpStart, addTpEnd));
addTpRow('Dynamic', ['Name'], [], 0);
let dynamicDelete = dynamicTbody.rows[0].children[1].children[0];
assert(dynamicDelete.title === 'Delete' && dynamicDelete.getAttribute('aria-keyshortcuts') === 'Delete', 'writable dynamic row lost Delete markers');
dynamicReadOnly = '1';
dynamicTbody.rows = [];
addTpRow('Dynamic', ['Name'], [], 0);
dynamicDelete = dynamicTbody.rows[0].children[1].children[0];
assert(!dynamicDelete.title && dynamicDelete.getAttribute('aria-keyshortcuts') === null, 'readonly dynamic row advertises unavailable Delete');
`

	cmd := exec.Command(node, "-e", harness, uiPath, managedPath) //nolint:gosec // test-only executable resolved by exec.LookPath
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
	uiPath := filepath.Join(dir, "ui.js")
	if err := os.WriteFile(uiPath, uiJS, 0o600); err != nil {
		t.Fatal(err)
	}

	const harness = `
const fs = require('fs');
const source = fs.readFileSync(process.argv[1], 'utf8');
const uiSource = fs.readFileSync(process.argv[2], 'utf8');
const mutationStart = source.indexOf('window.obFireRowEventChain = function');
const start = source.indexOf('function gridNameFromTarget');
const end = source.indexOf('// SlickGrid-aware applyTableParts', start);
const resolverStart = uiSource.indexOf('function obElementVisible');
const resolverEnd = uiSource.indexOf('\nfunction obListRows', resolverStart);
if (mutationStart < 0 || start < 0 || end < 0 || resolverStart < 0 || resolverEnd < 0) throw new Error('managed runtime slice not found');

let keydown = null;
let modal = false;
let hotkeyButtons = [];
let mainForm = null;
let items = [];
let activeCell = {row: 0, cell: 0};
let selectionModel = null;
let selectedRows = [];
let selectedRowsReads = 0;
let selectedRowsClears = 0;
let invalidates = 0;
const rowEvents = [];

const dataView = {
  getItems() { return items; },
  getItem(row) { return items[row]; },
  addItem(item) { items.push(item); },
  setItems(next) { items = next; },
  deleteItem(id) { items = items.filter(item => item.id !== id); },
  getRowById(id) { const row = items.findIndex(item => item.id === id); return row < 0 ? undefined : row; }
};
const editorLock = {active: false, commitOK: true, isActive() { return this.active; }, commitCurrentEdit() { return this.commitOK; }};
const grid = {
  getEditorLock() { return editorLock; },
  getActiveCell() { return activeCell; },
  setActiveCell(row, cell) { activeCell = {row, cell}; },
  getSelectionModel() { return selectionModel; },
  getSelectedRows() { selectedRowsReads++; if (!selectionModel) throw new Error('SlickGrid Selection model is not set'); return selectedRows.slice(); },
  setSelectedRows(rows) { if (!selectionModel) throw new Error('SlickGrid Selection model is not set'); selectedRowsClears++; selectedRows = rows.slice(); },
  invalidate() { invalidates++; },
  scrollRowIntoView() {}, editActiveCell() {}
};
const hostAttrs = {
  'data-sg-tp': 'Lines',
  'data-sg-rowadd': '1',
  'data-sg-rowdel': '1',
  'data-sg-rowafteradd': '1'
};
const host = {
  nodeType: 1, style: {display: '', visibility: ''}, parentElement: null,
  getAttribute(name) { return Object.prototype.hasOwnProperty.call(hostAttrs, name) ? hostAttrs[name] : null; }
};
const gridTarget = {closest(selector) {
  if (selector === '.ob-grid[data-sg-tp]') return host;
  return null;
}};
const unknownHost = {
  nodeType: 1, style: {display: '', visibility: ''}, parentElement: null,
  getAttribute(name) { return name === 'data-sg-tp' ? 'Unknown' : null; }
};
const unknownTarget = {closest(selector) { return selector === '.ob-grid[data-sg-tp]' ? unknownHost : null; }};
const domTable = {nodeType: 1, style: {display: '', visibility: ''}, parentElement: null};
const domTarget = {closest(selector) {
  if (selector === '.ob-grid[data-sg-tp]') return null;
  if (selector.includes('table[data-ob-dom-table]')) return domTable;
  return null;
}};
const body = {
  nodeType: 1, tagName: 'BODY', style: {display: '', visibility: ''}, parentElement: null,
  getAttribute() { return null; }, hasAttribute() { return false; }, closest() { return null; }
};
mainForm = {
  nodeType: 1, tagName: 'FORM', style: {display: '', visibility: ''}, parentElement: body,
  getAttribute() { return null; }, hasAttribute() { return false; },
  contains(node) { for (let cur = node; cur; cur = cur.parentElement) if (cur === this) return true; return false; },
  querySelectorAll(selector) { return selector === '[data-ob-hotkey]' ? hotkeyButtons : []; }
};
host.parentElement = mainForm;
unknownHost.parentElement = mainForm;
domTable.parentElement = mainForm;
const link = {tagName: 'A', closest(selector) {
  if (selector === '.ob-grid[data-sg-tp]') return null;
  if (selector.includes('a[href]')) return this;
  return null;
}};
const gridState = {
  grid, dataView, div: host, readOnly: false,
  columns: [{id: 'Name'}], columnsMeta: [{id: 'Name'}]
};
global.window = {
  _obGrids: {Lines: gridState},
  _obActiveGridName: '',
  _obActiveDOMTable: domTable,
  _obFormDirty: false,
  getComputedStyle(el) { return el && (el.computedStyle || el.style) ? (el.computedStyle || el.style) : {}; }
};
global.document = {
  activeElement: body,
  addEventListener(type, fn, capture) { if (type === 'keydown' && capture === true) keydown = fn; },
  contains(el) { for (let cur = el; cur; cur = cur.parentElement) if (cur === body) return !el.detached; return false; },
  getElementById(id) { return id === 'main-form' ? mainForm : null; },
  querySelectorAll() { return []; }
};
global.obHasBlockingModal = () => modal;
function updateTotals() {}
function obFireRowEvent(tpName, attr, eventName) {
  if (host.getAttribute(attr) !== '1') return;
  rowEvents.push({tpName, attr, eventName, values: items.map(item => item.Name)});
}
window.obFireRowEvent = obFireRowEvent;

function hotkeyElement(tag, options = {}) {
  const attrs = Object.assign({}, options.attrs || {});
  return {
    nodeType: 1, tagName: String(tag || 'DIV').toUpperCase(), parentElement: options.parent === undefined ? mainForm : options.parent,
    style: Object.assign({display: '', visibility: ''}, options.style || {}),
    computedStyle: Object.assign({display: '', visibility: ''}, options.computedStyle || {}),
    hidden: options.hidden === true, disabled: options.disabled === true, inert: options.inert === true,
    clicks: 0,
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name); },
    matches(selector) {
      if (selector !== ':disabled') return false;
      if (this.disabled) return true;
      for (let cur = this.parentElement; cur; cur = cur.parentElement) {
        if (cur.tagName === 'FIELDSET' && (cur.disabled || cur.hasAttribute('disabled'))) return true;
      }
      return false;
    },
    click() { this.clicks++; }
  };
}

function hotkeyButton(options = {}) {
  const attrs = Object.assign({'data-ob-hotkey': ' F9 '}, options.attrs || {});
  if (options.action !== false) attrs['data-ob-fire-click'] = options.actionName || 'Run';
  return hotkeyElement('button', Object.assign({}, options, {
    attrs
  }));
}

function nonActionableHotkeyButtons() {
  const ariaHiddenParent = hotkeyElement('div', {attrs: {'aria-hidden': 'true'}});
  const displayParent = hotkeyElement('div', {style: {display: 'none'}});
  const computedDisplayParent = hotkeyElement('div', {computedStyle: {display: 'none'}});
  const visibilityParent = hotkeyElement('div', {style: {visibility: 'hidden'}});
  const computedVisibilityParent = hotkeyElement('div', {computedStyle: {visibility: 'hidden'}});
  const disabledFieldset = hotkeyElement('fieldset', {attrs: {disabled: ''}, disabled: true});
  const ariaDisabledParent = hotkeyElement('div', {attrs: {'aria-disabled': 'true'}});
  const inertParent = hotkeyElement('div', {attrs: {inert: ''}, inert: true});
  return [
    ['own hidden', hotkeyButton({hidden: true})],
    ['handlerless', hotkeyButton({action: false})],
    ['ancestor aria-hidden', hotkeyButton({parent: ariaHiddenParent})],
    ['ancestor inline display', hotkeyButton({parent: displayParent})],
    ['ancestor computed display', hotkeyButton({parent: computedDisplayParent})],
    ['ancestor inline visibility', hotkeyButton({parent: visibilityParent})],
    ['ancestor computed visibility', hotkeyButton({parent: computedVisibilityParent})],
    ['own disabled', hotkeyButton({disabled: true})],
    ['disabled fieldset', hotkeyButton({parent: disabledFieldset})],
    ['own aria-disabled', hotkeyButton({attrs: {'aria-disabled': 'true'}})],
    ['ancestor aria-disabled', hotkeyButton({parent: ariaDisabledParent})],
    ['own inert', hotkeyButton({attrs: {inert: ''}, inert: true})],
    ['ancestor inert', hotkeyButton({parent: inertParent})],
    ['detached', hotkeyButton({parent: null})],
    ['outside main form', hotkeyButton({parent: body})]
  ];
}

function resetRows(names) {
  items = names.map((name, index) => ({id: index, _ord: index, Name: name}));
  activeCell = {row: 0, cell: 0};
  selectionModel = null;
  selectedRows = [];
  selectedRowsReads = 0;
  selectedRowsClears = 0;
  rowEvents.length = 0;
  window._obFormDirty = false;
  gridState.readOnly = false;
}

eval(uiSource.slice(resolverStart, resolverEnd));
eval(source.slice(mutationStart, start));
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

resetRows(['A', 'B']);
fire({key: 'Insert'});
assert(window._obActiveDOMTable === null, 'direct Slick context left a DOM table marker active');
assert(items.length === 3 && items[2].Name === '', 'exact Insert did not mutate SlickGrid data');
assert(rowEvents.length === 2 && rowEvents[0].eventName === 'ПриДобавленииСтроки' && rowEvents[1].eventName === 'ПослеДобавленияСтроки', 'Insert did not emit the row-add event chain in order');
assert(rowEvents[0].values.join(',') === 'A,B,' && rowEvents[1].values.join(',') === 'A,B,', 'Insert row-add event chain did not observe committed mutation');
const afterInsert = items.length;
fire({key: 'Insert', shiftKey: true});
assert(items.length === afterInsert, 'Shift+Insert was accepted as Insert');
fire({key: 'Insert', defaultPrevented: true});
assert(items.length === afterInsert, 'defaultPrevented Insert reached SlickGrid');

modal = true;
fire({key: 'Insert'});
assert(items.length === afterInsert, 'Insert escaped a blocking modal');
modal = false;

grid.setActiveCell(0, 0);
hotkeyButtons = [hotkeyButton()];
fire({key: 'F9'});
assert(items.length === afterInsert, 'built-in F9 ignored a whitespace-padded explicit form hotkey');

resetRows(['A', 'B']);
hotkeyButtons = [];
fire({key: 'F9'});
assert(items.map(item => item.Name).join(',') === 'A,A,B', 'exact F9 did not copy the active SlickGrid row');
assert(rowEvents.length === 2 && rowEvents[0].eventName === 'ПриДобавленииСтроки' && rowEvents[1].eventName === 'ПослеДобавленияСтроки', 'F9 did not emit the row-add event chain in order');
assert(rowEvents[0].values.join(',') === 'A,A,B' && rowEvents[1].values.join(',') === 'A,A,B', 'F9 row-add event chain fired before the copied row was committed');

for (const [name, candidate] of nonActionableHotkeyButtons()) {
  resetRows(['A', 'B']);
  hotkeyButtons = [candidate];
  const before = items.length;
  fire({key: 'F9'});
  assert(items.length === before + 1, name + ': Slick F9 did not copy exactly once');
  assert(candidate.clicks === 0, name + ': Slick capture clicked a nonactionable candidate');
}

// Resolver walks past a hidden first match and lets the visible second button
// suppress the built-in copy. A hidden-only or missing-form candidate does not.
resetRows(['A', 'B']);
const hiddenFirst = hotkeyButton({hidden: true});
const visibleSecond = hotkeyButton();
hotkeyButtons = [hiddenFirst, visibleSecond];
fire({key: 'F9'});
assert(items.length === 2 && window.obResolveActionableFormHotkey('F9') === visibleSecond,
  'hidden first Slick hotkey blocked a visible second candidate');
hotkeyButtons = [hiddenFirst];
fire({key: 'F9'});
assert(items.length === 3 && hiddenFirst.clicks === 0, 'hidden Slick hotkey consumed or received F9');
const savedForm = mainForm;
const missingFormCandidate = hotkeyButton();
hotkeyButtons = [missingFormCandidate];
mainForm = null;
fire({key: 'F9'});
assert(items.length === 4, 'missing main form suppressed built-in Slick F9');
assert(missingFormCandidate.clicks === 0, 'missing main form clicked its stale hotkey candidate');
mainForm = savedForm;
hotkeyButtons = [];

resetRows(['A', 'B', 'C']);
fire({key: 'ArrowDown', ctrlKey: true, shiftKey: true});
assert(items.map(item => item.Name).join(',') === 'A,B,C', 'Ctrl+Shift+Down was accepted as Ctrl+Down');
fire({key: 'ArrowDown', ctrlKey: true});
assert(items.map(item => item.Name).join(',') === 'B,A,C', 'exact Ctrl+Down did not mutate row order');

const beforeLink = items.length;
fire({key: 'Insert', target: link});
assert(items.length === beforeLink, 'remembered grid hijacked a focused link');
gridState.readOnly = true;
fire({key: 'Insert'});
assert(items.length === beforeLink, 'readonly SlickGrid accepted Insert');

gridState.readOnly = false;
host.style.display = 'none';
window._obActiveGridName = 'Lines';
fire({key: 'Insert', target: body});
assert(items.length === beforeLink && window._obActiveGridName === '', 'hidden remembered SlickGrid accepted a shortcut or stayed active');
host.style.display = '';
window._obActiveGridName = 'Lines';
fire({key: 'Insert', target: unknownTarget});
assert(items.length === beforeLink, 'unknown direct grid fell back to remembered SlickGrid');

// The inverse mixed-form direction: a concrete no-grid DOM target must never
// use a remembered SlickGrid for Insert/Delete/F9.
resetRows(['A', 'B']);
window._obActiveGridName = 'Lines';
const beforeDOMTarget = items.map(item => item.Name).join(',');
fire({key: 'Insert', target: domTarget});
fire({key: 'F9', target: domTarget});
fire({key: 'Delete', target: domTarget});
assert(items.map(item => item.Name).join(',') === beforeDOMTarget && rowEvents.length === 0,
  'DOM table target mutated remembered SlickGrid via Insert/F9/Delete');
assert(window._obActiveGridName === '', 'direct DOM context left a SlickGrid marker active');

// Toolbar Delete executes the production mutation. With no vendored
// RowSelectionModel it must not even call the throwing selection API, must
// remove the active row, and must fire rowdel exactly once after mutation.
resetRows(['A', 'B', 'C']);
grid.setActiveCell(1, 0);
let deleted = false;
const normalInvalidate = grid.invalidate;
grid.invalidate = function() { throw new Error('render failed after data mutation'); };
try { deleted = window.obGridDelRow('Lines'); } catch (error) { throw new Error('toolbar Delete threw after mutation: ' + error.message); }
grid.invalidate = normalInvalidate;
assert(deleted === true && items.map(item => item.Name).join(',') === 'A,C', 'no-model Delete did not remove active row');
assert(selectedRowsReads === 0 && selectedRowsClears === 0, 'no-model Delete called an unavailable selection API');
assert(rowEvents.length === 1 && rowEvents[0].eventName === 'ПриУдаленииСтроки' && rowEvents[0].values.join(',') === 'A,C', 'rowdel did not fire exactly once after active-row mutation');

// If a real selection model is supplied by an embedding application, all
// selected rows are deleted once and selection is cleared safely.
resetRows(['A', 'B', 'C']);
selectionModel = {};
selectedRows = [0, 2, 2];
deleted = window.obGridDelRow('Lines');
assert(deleted === true && items.map(item => item.Name).join(',') === 'B', 'selection-model Delete lost honest multi-select semantics');
assert(selectedRowsReads === 1 && selectedRowsClears === 1, 'selection-model Delete did not read/clear selection exactly once');
assert(rowEvents.length === 1 && rowEvents[0].values.join(',') === 'B', 'multi-delete rowdel fired before final mutation or more than once');
`

	cmd := exec.Command(node, "-e", harness, managedPath, uiPath) //nolint:gosec // test-only executable resolved by exec.LookPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node managed shortcut behavior harness: %v\n%s", err, out)
	}
}
