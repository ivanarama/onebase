const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

let current;
let banner;
let fetchBodies;

function domNode(tagName) {
  return {
    tagName: String(tagName || '').toUpperCase(),
    style: {},
    children: [],
    appendChild(child) { this.children.push(child); return child; },
    remove() {},
    addEventListener() {},
    setAttribute() {},
    getAttribute() { return null; },
    querySelector() { return null; },
    querySelectorAll() { return []; }
  };
}

function resetDOM() {
  banner = domNode('div');
  fetchBodies = [];
  const pathInput = {name: 'Upload', value: '', disabled: false};
  const content = {
    name: '_fc_Upload',
    value: '',
    disabled: false,
    dataset: {},
    getAttribute(name) { return name === 'data-ob-file-content-for' ? 'Upload' : null; }
  };
  const form = {
    id: 'main-form',
    controls: [pathInput, content],
    querySelectorAll(selector) {
      return selector === '[data-ob-file-content-for]' ? [content] : [];
    },
    querySelector(selector) {
      if (selector.includes('data-ob-file-content-for=') && selector.includes('Upload')) return content;
      return null;
    },
    appendChild() {}
  };
  current = {pathInput, content, form};
  return current;
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
  addEventListener() {},
  querySelector() { return null; },
  querySelectorAll() { return []; },
  createElement: domNode,
  getElementById(id) {
    if (id === 'ob-managed-config') {
      return {textContent: JSON.stringify({url: '/form-event'})};
    }
    if (id === 'main-form') return current && current.form;
    if (id === 'upload-path') return current && current.pathInput;
    if (id === 'upload-content') return current && current.content;
    if (id === 'ob-fmevt-banner') return banner;
    return null;
  }
};

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

function pick(file) {
  const input = {files: [file]};
  window.obFilePick(input, 'upload-path', 'upload-content');
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
