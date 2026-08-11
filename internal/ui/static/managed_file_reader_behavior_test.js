const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

let current;
let banner;
let fetchBodies;
let submittedBodies;
const documentListeners = new Map();

function domNode(tagName) {
  const attributes = new Map();
  const node = {
    tagName: String(tagName || '').toUpperCase(),
    style: {},
    children: [],
    appendChild(child) {
      this.children.push(child);
      child.parentElement = this;
      return child;
    },
    remove() {
      if (!this.parentElement) return;
      const index = this.parentElement.children.indexOf(this);
      if (index >= 0) this.parentElement.children.splice(index, 1);
    },
    addEventListener() {},
    setAttribute(name, value) { attributes.set(String(name), String(value)); },
    getAttribute(name) { return attributes.has(String(name)) ? attributes.get(String(name)) : null; },
    hasAttribute(name) { return attributes.has(String(name)); },
    querySelector() { return null; },
    querySelectorAll() { return []; }
  };
  Object.defineProperty(node, 'innerHTML', {
    get() { return ''; },
    set(value) { if (value === '') this.children = []; }
  });
  Object.defineProperty(node, 'rows', {get() { return this.children; }});
  return node;
}

function resetDOM() {
  banner = domNode('div');
  fetchBodies = [];
  submittedBodies = [];
  const pathInput = {
    name: 'Upload', value: '', disabled: false,
    getAttribute() { return null; }
  };
  const content = {
    // Processor helpers can have a collision-safe name; the runtime must use
    // rendered data-ob-file-content-for identity rather than the _fc_ prefix.
    name: '_fc_Upload_',
    value: '',
    disabled: false,
    dataset: {},
    getAttribute(name) { return name === 'data-ob-file-content-for' ? 'Upload' : null; }
  };
  const form = Object.assign(Object.create(global.HTMLFormElement.prototype), {
    id: 'main-form',
    controls: [pathInput, content],
    getAttribute() { return null; },
    hasAttribute() { return false; },
    querySelectorAll(selector) {
      if (selector !== '[data-ob-file-content-for]') return [];
      return this.controls.filter(control => control.getAttribute && control.getAttribute('data-ob-file-content-for'));
    },
    querySelector(selector) {
      const match = selector.match(/data-ob-file-content-for="([^"]*)"/);
      if (!match) return null;
      return this.controls.find(control => control.getAttribute && control.getAttribute('data-ob-file-content-for') === match[1]) || null;
    },
    appendChild(child) {
      this.controls.push(child);
      child.form = this;
      child.remove = () => {
        const index = this.controls.indexOf(child);
        if (index >= 0) this.controls.splice(index, 1);
      };
      return child;
    }
  });
  pathInput.form = form;
  content.form = form;
  current = {
    pathInput,
    content,
    form,
    nodes: {'upload-path': pathInput, 'upload-content': content}
  };
  return current;
}

function addFileControl(name, helperName) {
  const slug = name.toLowerCase();
  const pathInput = {
    name, value: '', disabled: false, form: current.form,
    getAttribute() { return null; }
  };
  const content = {
    name: helperName,
    value: '',
    disabled: false,
    dataset: {},
    form: current.form,
    getAttribute(attr) { return attr === 'data-ob-file-content-for' ? name : null; }
  };
  current.form.controls.push(pathInput, content);
  current.nodes[slug + '-path'] = pathInput;
  current.nodes[slug + '-content'] = content;
  return {pathInput, content, pathId: slug + '-path', contentId: slug + '-content'};
}

function installTableBody(id, attributes) {
  const tbody = domNode('tbody');
  for (const [name, value] of Object.entries(attributes || {})) tbody.setAttribute(name, value);
  current.nodes[id] = tbody;
  return tbody;
}

global.window = global;
global.location = {pathname: '/ui/processor/Test'};
global.history = {replaceState() {}};
global.navigator = {clipboard: null};
global.sessionStorage = {getItem() { return null; }, setItem() {}, removeItem() {}};
global.confirm = () => true;
global.CSS = {escape: value => String(value)};
global.window.addEventListener = () => {};
global.document = {
  readyState: 'loading',
  title: 'test',
  activeElement: null,
  body: domNode('body'),
  addEventListener(type, listener) {
    if (!documentListeners.has(type)) documentListeners.set(type, []);
    documentListeners.get(type).push(listener);
  },
  querySelector() { return null; },
  querySelectorAll() { return []; },
  createElement: domNode,
  getElementById(id) {
    if (id === 'ob-managed-config') {
      return {textContent: JSON.stringify({url: '/form-event'})};
    }
    if (id === 'main-form') return current && current.form;
    if (current && current.nodes && current.nodes[id]) return current.nodes[id];
    if (id === 'ob-fmevt-banner') return banner;
    return null;
  }
};

function newSubmitEvent(form, submitter) {
  return {
    type: 'submit',
    target: form,
    submitter: submitter || null,
    defaultPrevented: false,
    propagationStopped: false,
    immediatePropagationStopped: false,
    preventDefault() { this.defaultPrevented = true; },
    stopPropagation() { this.propagationStopped = true; },
    stopImmediatePropagation() {
      this.immediatePropagationStopped = true;
      this.propagationStopped = true;
    }
  };
}

function dispatchSubmit(form, submitter) {
  const event = newSubmitEvent(form, submitter);
  for (const listener of documentListeners.get('submit') || []) {
    listener(event);
    if (event.immediatePropagationStopped || event.propagationStopped) break;
  }
  return event;
}

function formBody(form) {
  const body = new URLSearchParams();
  new FakeFormData(form).forEach((value, name) => body.append(name, value));
  return body.toString();
}

class FakeHTMLFormElement {}
FakeHTMLFormElement.prototype.requestSubmit = function(submitter) {
  const event = dispatchSubmit(this, submitter || null);
  if (!event.defaultPrevented) submittedBodies.push(formBody(this));
};
FakeHTMLFormElement.prototype.submit = function() {
  submittedBodies.push(formBody(this));
};
global.HTMLFormElement = FakeHTMLFormElement;

class FakeFormData {
  constructor(form) {
    this.values = [];
    for (const control of form.controls || []) {
      if (!control.disabled && control.name) this.append(control.name, control.value || '');
    }
  }
  append(name, value) { this.values.push([String(name), value]); }
  set(name, value) {
    name = String(name);
    this.values = this.values.filter(item => item[0] !== name);
    this.values.push([name, value]);
  }
  forEach(fn) { for (const [name, value] of this.values) fn(value, name); }
}
global.FormData = FakeFormData;

class FakeFileReader {
  static instances = [];
  constructor() {
    this.result = null;
    this.error = null;
    FakeFileReader.instances.push(this);
  }
  readAsArrayBuffer(file) { this.file = file; }
  succeed(text) {
    const bytes = Buffer.from(text, 'utf8');
    this.result = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
    this.onload();
  }
  fail(message) {
    this.error = new Error(message);
    this.onerror();
  }
}
global.FileReader = FakeFileReader;

global.fetch = async (_url, options) => {
  fetchBodies.push(options.body.toString());
  return {json: async () => ({messages: []})};
};

resetDOM();
const managedSource = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');
vm.runInThisContext(managedSource, {filename: 'managed.js'});
// DOMContentLoaded is intentionally not dispatched: initialise the real
// delegated handlers directly, without running unrelated UI widgets.
obManagedInitDelegates();

function pick(file) {
  const input = {files: [file]};
  window.obFilePick(input, 'upload-path', 'upload-content');
}

function pickControl(file, pathId, contentId) {
  window.obFilePick({files: [file]}, pathId, contentId);
}

function nextTask() {
  return new Promise(resolve => setImmediate(resolve));
}

test('immediate click waits for the current file read', async () => {
  resetDOM();
  FakeFileReader.instances = [];
  pick({name: 'instant.txt'});

  const fired = window.obFire('Upload', 'ПриИзменении');
  await Promise.resolve();
  assert.equal(fetchBodies.length, 0, 'request started before FileReader completed');

  FakeFileReader.instances[0].succeed('instant contents');
  await fired;
  assert.equal(fetchBodies.length, 1);
  assert.equal(new URLSearchParams(fetchBodies[0]).get('Upload'), 'instant contents');
});

test('obFire re-syncs grid state after waiting for FileReader', async () => {
  resetDOM();
  FakeFileReader.instances = [];
  const gridState = {name: 'tp_json.Товары', value: '', disabled: false, getAttribute() { return null; }};
  current.form.controls.push(gridState);
  const originalGridSync = window.obGridSync;
  let syncCalls = 0;
  window.obGridSync = () => {
    syncCalls += 1;
    gridState.value = syncCalls === 1 ? '[{"Цена":1}]' : '[{"Цена":2}]';
    return true;
  };
  try {
    pick({name: 'grid-event.txt'});
    const fired = window.obFire('Upload', 'ПриИзменении');
    await Promise.resolve();
    assert.equal(syncCalls, 1);
    assert.equal(fetchBodies.length, 0);

    FakeFileReader.instances[0].succeed('contents');
    await fired;
    assert.equal(syncCalls, 2);
    assert.equal(new URLSearchParams(fetchBodies[0]).get('tp_json.Товары'), '[{"Цена":2}]');
  } finally {
    window.obGridSync = originalGridSync;
  }
});

test('full submit re-syncs grid state after waiting for FileReader', async () => {
  resetDOM();
  FakeFileReader.instances = [];
  const gridState = {name: 'tp_json.Товары', value: '', disabled: false, getAttribute() { return null; }};
  current.form.controls.push(gridState);
  current.form.hasAttribute = name => name === 'data-ob-grid-sync';
  const originalGridSync = window.obGridSync;
  let syncCalls = 0;
  window.obGridSync = () => {
    syncCalls += 1;
    gridState.value = syncCalls === 1 ? '[{"Цена":1}]' : '[{"Цена":2}]';
    return true;
  };
  try {
    pick({name: 'grid-submit.txt'});
    const event = dispatchSubmit(current.form);
    assert.equal(event.defaultPrevented, true);
    assert.equal(syncCalls, 1);
    assert.equal(submittedBodies.length, 0);

    FakeFileReader.instances[0].succeed('contents');
    await nextTask();
    assert.equal(syncCalls, 2);
    assert.equal(submittedBodies.length, 1);
    assert.equal(new URLSearchParams(submittedBodies[0]).get('tp_json.Товары'), '[{"Цена":2}]');
  } finally {
    window.obGridSync = originalGridSync;
  }
});

test('a stale first reader cannot overwrite or block the newer selection', async () => {
  resetDOM();
  FakeFileReader.instances = [];
  pick({name: 'first.txt'});
  pick({name: 'second.txt'});

  const fired = window.obFire('Upload', 'ПриИзменении');
  FakeFileReader.instances[0].succeed('stale contents');
  await Promise.resolve();
  assert.equal(current.content.value, '');
  assert.equal(fetchBodies.length, 0);

  FakeFileReader.instances[1].succeed('fresh contents');
  await fired;
  assert.equal(current.content.value, 'fresh contents');
  assert.equal(new URLSearchParams(fetchBodies[0]).get('Upload'), 'fresh contents');
});

test('a file read error fails closed', async () => {
  resetDOM();
  FakeFileReader.instances = [];
  pick({name: 'broken.txt'});

  const fired = window.obFire('Upload', 'ПриИзменении');
  FakeFileReader.instances[0].fail('disk error');
  await fired;
  assert.equal(fetchBodies.length, 0, 'request was sent after FileReader error');
  assert.match(current.content._obFileReadState.error, /broken\.txt/);
  assert.equal(current.content._obFileReadState.loading, false);
});

test('missing or throwing FileReader constructor settles the state with an error', async () => {
  const original = global.FileReader;
  try {
    for (const replacement of [
      undefined,
      class ThrowingFileReader { constructor() { throw new Error('constructor failed'); } }
    ]) {
      resetDOM();
      global.FileReader = replacement;
      assert.doesNotThrow(() => pick({name: 'unsupported.txt'}));
      const state = current.content._obFileReadState;
      await state.pending;
      assert.equal(state.loading, false);
      assert.match(state.error, /unsupported\.txt/);
    }
  } finally {
    global.FileReader = original;
  }
});

test('implicit Enter submit waits for every current read and sends non-empty processor helpers', async () => {
  resetDOM();
  FakeFileReader.instances = [];
  const attachment = addFileControl('Attachment', '_fc_Attachment_');
  pick({name: 'upload.txt'});
  pickControl({name: 'attachment.txt'}, attachment.pathId, attachment.contentId);

  // Named controls may shadow both methods on a real HTMLFormElement. The
  // runtime must call the native prototype method and must not recurse.
  current.form.requestSubmit = 'shadowed control';
  current.form.submit = 'shadowed control';
  const event = dispatchSubmit(current.form, null);
  assert.equal(event.defaultPrevented, true, 'implicit submit was not cancelled synchronously');
  assert.equal(submittedBodies.length, 0);

  FakeFileReader.instances[0].succeed('upload body');
  await nextTask();
  assert.equal(submittedBodies.length, 0, 'form submitted before every file read completed');

  FakeFileReader.instances[1].succeed('attachment body');
  await nextTask();
  assert.equal(submittedBodies.length, 1);
  const body = new URLSearchParams(submittedBodies[0]);
  assert.equal(body.get('_fc_Upload_'), 'upload body');
  assert.equal(body.get('_fc_Attachment_'), 'attachment body');
});

test('ordinary submit is blocked when a current file read fails', async () => {
  resetDOM();
  FakeFileReader.instances = [];
  pick({name: 'broken-submit.txt'});

  const event = dispatchSubmit(current.form, null);
  assert.equal(event.defaultPrevented, true);
  FakeFileReader.instances[0].fail('disk error');
  await nextTask();
  assert.equal(submittedBodies.length, 0, 'form submitted after FileReader error');
  assert.match(current.content._obFileReadState.error, /broken-submit\.txt/);
});

test('ordinary submit safely falls back to native submit without requestSubmit', async () => {
  const nativeRequestSubmit = FakeHTMLFormElement.prototype.requestSubmit;
  try {
    FakeHTMLFormElement.prototype.requestSubmit = undefined;
    resetDOM();
    FakeFileReader.instances = [];
    current.form.submit = 'shadowed control';
    pick({name: 'legacy.txt'});
    const submitter = {
      name: '_action', value: 'run', disabled: false, form: current.form
    };

    const event = dispatchSubmit(current.form, submitter);
    assert.equal(event.defaultPrevented, true);
    FakeFileReader.instances[0].succeed('legacy body');
    await nextTask();

    assert.equal(submittedBodies.length, 1);
    const body = new URLSearchParams(submittedBodies[0]);
    assert.equal(body.get('_fc_Upload_'), 'legacy body');
    assert.equal(body.get('_action'), 'run');
  } finally {
    FakeHTMLFormElement.prototype.requestSubmit = nativeRequestSubmit;
  }
});

test('table round-trip preserves readonly controls, mirrors and delete guards', () => {
  resetDOM();
  window._obGrids = {};
  window._tpRefOpts = {
    Товары: {Номенклатура: [{id: 'ref-1', _label: 'Товар'}]},
    Редактируемые: {Номенклатура: [{id: 'ref-2', _label: 'Другой'}]}
  };
  window._tpEnumLabels = {};
  window._tpEnumOrder = {};

  const tpBody = installTableBody('tp-body-Товары', {
    'data-ob-table-readonly': '1',
    'data-tp-cmd': '1',
    'data-tp-fields': 'Номенклатура|reference:Товар,Количество|number'
  });
  window.applyTableParts({Товары: [{Номенклатура: 'ref-1', Количество: 2}]});
  const tpRow = tpBody.children[0];
  assert.equal(tpRow.children[0].children[0].disabled, true, 'selection remained enabled');
  const refCell = tpRow.children[1];
  assert.equal(refCell.children[0].disabled, true, 'reference select remained enabled');
  assert.equal(refCell.children[1].type, 'hidden');
  assert.equal(refCell.children[1].name, 'tp.Товары.0.Номенклатура');
  assert.equal(refCell.children[1].value, 'ref-1');
  assert.equal(tpRow.children[2].children[0].readOnly, true, 'number input remained editable');
  const tpDelete = tpRow.children[3].children[0];
  assert.equal(tpDelete.disabled, true);
  assert.equal(tpDelete.onclick, undefined);

  const vtBody = installTableBody('vt-body-Подбор', {
    'data-ob-table-readonly': '1',
    'data-vt-fields': 'Флаг|bool,Комментарий|string'
  });
  window.applyFormTables({Подбор: [{Флаг: true, Комментарий: 'текст'}]});
  const vtRow = vtBody.children[0];
  assert.equal(vtRow.children[0].children[0].disabled, true, 'bool remained enabled');
  assert.equal(vtRow.children[0].children[1].type, 'hidden');
  assert.equal(vtRow.children[0].children[1].value, 'true');
  assert.equal(vtRow.children[1].children[0].readOnly, true, 'text remained editable');
  assert.equal(vtRow.children[2].children[0].disabled, true, 'VT delete remained enabled');

  const editableBody = installTableBody('tp-body-Редактируемые', {
    'data-tp-fields': 'Номенклатура|reference:Товар,Количество|number'
  });
  window.applyTableParts({Редактируемые: [{Номенклатура: 'ref-2', Количество: 4}]});
  const editableRow = editableBody.children[0];
  assert.equal(editableRow.children[0].children.length, 1, 'editable ref unexpectedly got hidden mirror');
  assert.equal(editableRow.children[0].children[0].disabled, false);
  assert.equal(editableRow.children[1].children[0].readOnly, false);
  assert.equal(editableRow.children[2].children[0].disabled, false);
  assert.equal(typeof editableRow.children[2].children[0].onclick, 'function');

  let mutated = false;
  window._obGrids = {
    Закрытые: {
      readOnly: true,
      dataView: {getItems() { mutated = true; return []; }},
      grid: {}
    }
  };
  window.obGridAddRow('Закрытые');
  window.obGridDelRow('Закрытые');
  assert.equal(mutated, false, 'exported grid mutators bypassed readonly');
});
