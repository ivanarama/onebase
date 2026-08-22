const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const htmlPath = process.env.ONEBASE_START_ERROR_HTML;
if (!htmlPath) throw new Error('ONEBASE_START_ERROR_HTML is not set');
const html = fs.readFileSync(htmlPath, 'utf8');
const markerStart = 'var _onebaseStartFixBegin = true;';
const markerEnd = 'var _onebaseStartFixEnd = true;';
const sourceStart = html.indexOf(markerStart);
const sourceEnd = html.indexOf(markerEnd, sourceStart + markerStart.length);
if (sourceStart < 0 || sourceEnd < 0) throw new Error('start-fix production JavaScript slice not found');
const startFixSource = html.slice(sourceStart + markerStart.length, sourceEnd);

function createHarness(body) {
  const elements = new Map();
  const starts = [];
  let document;

  function element(id, display = '') {
    const attrs = new Map();
    const node = {
      id,
      style: {display},
      children: [],
      textContent: '',
      disabled: false,
      setAttribute(name, value) { attrs.set(String(name), String(value)); },
      appendChild(child) { this.children.push(child); return child; },
      removeChild(child) { this.children = this.children.filter((item) => item !== child); },
      focus() { document.activeElement = this; },
      select() {}
    };
    Object.defineProperty(node, 'innerHTML', {
      get() { return ''; },
      set() { node.children = []; }
    });
    if (id) elements.set(id, node);
    return node;
  }

  const modal = element('start-error-modal', 'none');
  const card = element('start-error-card');
  const errorText = element('start-error-text');
  const fix = element('start-error-fix', 'none');
  const fixList = element('start-error-fix-list');
  const fixButton = element('start-error-fix-btn', 'none');
  const skipped = element('start-error-skipped', 'none');
  const skippedList = element('start-error-skipped-list');
  const continueButton = element('start-error-continue-btn', 'none');
  const status = element('start-error-status');
  const bodyNode = element('body');

  document = {
    body: bodyNode,
    activeElement: null,
    getElementById(id) { return elements.get(id) || null; },
    createElement() { return element(''); },
    execCommand() { return true; }
  };

  const context = vm.createContext({
    document,
    navigator: {},
    alert(message) { throw new Error('unexpected alert: ' + message); },
    setLauncherInert() {},
    fetch() {
      return Promise.resolve({
        ok: true,
        json() { return Promise.resolve(body); }
      });
    },
    _nativeOK: false,
    startBase(_event, id) { starts.push(id); },
    startBaseNative(_event, id) { starts.push(id); }
  });
  vm.runInContext(startFixSource, context);

  function openFix() {
    context.showStartErrorModal('unique code gate', {
      kind: 'renumber',
      objects: [{object: 'Контрагенты', field: 'Код', empty: 2}]
    }, 'base-1');
  }

  return {
    context,
    starts,
    nodes: {modal, card, errorText, fix, fixList, fixButton, skipped, skippedList, continueButton, status},
    openFix
  };
}

function settle() {
  return new Promise((resolve) => setImmediate(resolve));
}

test('skipped objects stay visible until the user continues', async () => {
  const harness = createHarness({
    ok: true,
    filled: 2,
    skipped: [
      {object: 'Номенклатура', field: 'Код', error: 'нет колонок is_folder, parent_id'},
      {object: 'Валюты', field: 'Код', error: 'нет колонки _is_predefined'}
    ]
  });
  harness.openFix();
  harness.context.runStartFix();
  await settle();

  assert.deepEqual(harness.starts, [], 'base must not start before skipped warning is acknowledged');
  assert.equal(harness.nodes.modal.style.display, 'flex');
  assert.equal(harness.nodes.skipped.style.display, 'block');
  assert.equal(harness.nodes.skippedList.children.length, 2);
  assert.match(harness.nodes.skippedList.children[0].textContent, /Номенклатура\.Код.*is_folder/);
  assert.match(harness.nodes.skippedList.children[1].textContent, /Валюты\.Код.*_is_predefined/);
  assert.equal(harness.nodes.fixButton.style.display, 'none');
  assert.equal(harness.nodes.continueButton.style.display, 'inline-block');
  assert.match(harness.nodes.status.textContent, /Дозаполнено записей: 2.*Пропущено объектов: 2/);

  harness.context.continueStartAfterRenumber();
  assert.deepEqual(harness.starts, ['base-1']);
  assert.equal(harness.nodes.modal.style.display, 'none');
});

test('successful renumber without skipped objects keeps automatic restart', async () => {
  const harness = createHarness({ok: true, filled: 2, skipped: []});
  harness.openFix();
  harness.context.runStartFix();
  await settle();

  assert.deepEqual(harness.starts, ['base-1']);
  assert.equal(harness.nodes.modal.style.display, 'none');
  assert.equal(harness.nodes.skipped.style.display, 'none');
  assert.equal(harness.nodes.continueButton.style.display, 'none');
});
