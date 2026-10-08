const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const start = source.indexOf('(function () {\n  if (window.__obNavInit)');
const end = source.indexOf('\nfunction obApplyValueAxisFormatter', start);
assert.ok(start >= 0 && end > start, 'production navigation initializer exists');
const production = source.slice(start, end);

function detail(id, legacy, open = true) {
  return {
    open,
    getAttribute(name) { return name === 'data-navsec' ? id : name === 'data-navsec-legacy' ? legacy : null; },
    addEventListener(name, fn) { this[name] = fn; },
    toggleTo(value) { this.open = value; this.toggle(); },
  };
}

function initialize(details, values) {
  const classList = {contains() { return false; }, toggle() {}};
  const document = {
    documentElement: {classList}, body: {classList}, addEventListener() {},
    getElementById() { return null; },
    querySelector() { return null; },
    querySelectorAll(selector) { return selector === 'aside details.navsec' ? details : []; },
  };
  const sandbox = {
    window: {matchMedia() { return {matches: false, addEventListener() {}}; }}, document,
    obReady(fn) { fn(); },
    localStorage: {
      getItem(key) { return values.has(key) ? values.get(key) : null; },
      setItem(key, value) { values.set(key, value); },
    },
  };
  vm.runInNewContext(production, sandbox, {filename: 'ui.js#navigation'});
}

test('rename and language changes retain saved section/folder state', () => {
  const saved = new Map();
  const section = detail('stable-school-section', 'School');
  const folder = detail('stable-school-folder', null, false);
  initialize([section, folder], saved);
  section.toggleTo(false);
  folder.toggleTo(true);
  const renamed = detail('stable-school-section', 'Education');
  const translatedFolder = detail('stable-school-folder', null, false);
  initialize([renamed, translatedFolder], saved);
  assert.equal(renamed.open, false);
  assert.equal(translatedFolder.open, true);
  const otherContext = detail('another-context-school-section', 'Education');
  initialize([otherContext], saved);
  assert.equal(otherContext.open, true);
});

test('legacy preferences migrate once; stable identity takes precedence', () => {
  const saved = new Map([['navsec:Catalogs', '0']]);
  const section = detail('stable-catalogs', 'Catalogs');
  initialize([section], saved);
  assert.equal(section.open, false);
  section.toggleTo(true);
  const translated = detail('stable-catalogs', 'Справочники', false);
  initialize([translated], saved);
  assert.equal(translated.open, true);
  assert.equal(saved.get('navsec:Catalogs'), '0');
});
