const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const source = fs.readFileSync('static/managed.js', 'utf8');
const attrsStart = source.indexOf('  var FORM_ATTRS =');
const attrsEnd = source.indexOf('  function stashFormAttrs()', attrsStart);
assert.ok(attrsStart >= 0 && attrsEnd > attrsStart, 'production attribute initialization not found');
const attrsInit = source.slice(attrsStart, attrsEnd);

function extract(name) {
  const start = source.indexOf('function ' + name + '(');
  if (start < 0) throw new Error('missing production function ' + name);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}' && --depth === 0) return (source.slice(start - 6, start) === 'async ' ? 'async ' : '') + source.slice(start, i + 1);
  }
  throw new Error('unterminated production function ' + name);
}

function runtime(cfg, controls = []) {
  const form = {
    controls,
    appendChild(input) { this.controls.push(input); },
    querySelector(selector) {
      const match = /^\[name="([^"]+)"\]$/.exec(selector);
      return match ? this.controls.find((el) => el.name === match[1]) || null : null;
    },
    // applyValues обходит все копии реквизита с этим name (#1759).
    querySelectorAll(selector) {
      const match = /^\[name="([^"]+)"\]$/.exec(selector);
      return match ? this.controls.filter((el) => el.name === match[1]) : [];
    },
  };
  const document = {
    getElementById(id) { return id === 'main-form' ? form : null; },
    createElement(tag) { return {tagName: tag.toUpperCase(), value: '', type: '', classList: {contains() { return false; }}}; },
  };
  // Model successful controls, including unchecked/disabled visible fields.
  class FormData {
    constructor(form) {
      this.values = new Map(form.controls.filter((el) => !el.disabled && (el.type !== 'checkbox' || el.checked))
        .map((el) => [el.name, el.value]));
    }
    set(k, v) { this.values.set(k, v); }
    forEach(fn) { this.values.forEach(fn); }
  }
  const api = new Function('document', 'window', 'cfg', 'FormData', `
    function obManagedReady(fn) { fn(); }
    var DOC_ID = '';
    var formEditState = {revision: 0};
    function serviceField(name) { return name; }
    ${attrsInit}
    ${extract('managedRefParts')}
    ${extract('ensureRefOption')}
    ${extract('applyValues')}
    ${extract('snapshotFormEvent')}
    return {ensureFormAttrControls, applyValues, snapshotFormEvent};
  `)(document, {}, cfg, FormData);
  api.form = form;
  return api;
}

async function carry(fixture) {
  const api = runtime(fixture.config);
  const initial = await api.snapshotFormEvent('ПолеА', 'ПриИзменении');
  api.applyValues(fixture.response.values, fixture.response.refOptions);
  const next = await api.snapshotFormEvent('ПолеБ', 'ПриИзменении');
  return {initial: Object.fromEntries(initial.body), next: Object.fromEntries(next.body)};
}

if (process.env.ONEBASE_FORM_ATTR_FIXTURE_B64) {
  carry(JSON.parse(Buffer.from(process.env.ONEBASE_FORM_ATTR_FIXTURE_B64, 'base64').toString('utf8')))
    .then((result) => process.stdout.write(JSON.stringify(result))).catch((err) => { console.error(err); process.exitCode = 1; });
} else {
  test('unplaced scalar attributes survive response and the next event snapshot', async () => {
    const api = runtime({formAttrs: ['Flag', 'Empty', 'Ref'], formAttrValues: {Flag: 'false', Empty: '', Ref: 'initial-id'}});
    assert.equal(api.form.controls.length, 3);
    const initial = await api.snapshotFormEvent('Other', 'ПриИзменении');
    assert.equal(initial.body.get('Flag'), 'false');
    assert.equal(initial.body.get('Empty'), '');
    assert.equal(initial.body.get('Ref'), 'initial-id');
    api.ensureFormAttrControls();
    assert.equal(api.form.controls.length, 3, 'initialization must be idempotent');
    api.applyValues({Flag: true, Empty: null, Ref: {UUID: 'next-id', Name: 'Label'}}, null);
    let snapshot = await api.snapshotFormEvent('Other', 'ПриИзменении');
    assert.equal(snapshot.body.get('Flag'), 'true');
    assert.equal(snapshot.body.get('Empty'), '');
    assert.equal(snapshot.body.get('Ref'), 'next-id');
    api.applyValues({Flag: false, Ref: null}, null);
    snapshot = await api.snapshotFormEvent('Other', 'ПриИзменении');
    assert.equal(snapshot.body.get('Flag'), 'false');
    assert.equal(snapshot.body.get('Ref'), '');
  });

  test('placed controls keep their own values and disabled/unchecked semantics', async () => {
    const visible = {name: 'Visible', value: 'user edit', type: 'text'};
    const checkbox = {name: 'Checkbox', value: 'true', type: 'checkbox', checked: false};
    const locked = {name: 'Locked', value: 'secret', type: 'text', disabled: true};
    const api = runtime({formAttrs: ['Visible', 'Checkbox', 'Locked'], formAttrValues: {Visible: 'initial'}}, [visible, checkbox, locked]);
    assert.equal(api.form.controls.length, 3, 'do not create competing hidden controls');
    const snapshot = await api.snapshotFormEvent('Other', 'ПриИзменении');
    assert.equal(snapshot.body.get('Visible'), 'user edit');
    assert.equal(snapshot.body.has('Checkbox'), false);
    assert.equal(snapshot.body.has('Locked'), false);
  });
}
