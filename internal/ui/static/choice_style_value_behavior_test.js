const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

// choice_style_value: поле ссылки показывает выбранное значение тем же
// оформлением, что строка в списке выбора. Проверяется боевой код ui.js
// (блок obPaintChoiceValue в конце файла) на подменном DOM.
const source = fs.readFileSync('static/ui.js', 'utf8');
const start = source.indexOf('// choice_style_value: поле ссылки показывает выбранное значение');
const end = source.indexOf('/* Индикатор выполнения у форм с data-ob-busy', start);
if (start < 0 || end < 0) throw new Error('choice_style_value block is absent from ui.js');
const block = source.slice(start, end);

function element(attrs, options) {
  const a = Object.assign({}, attrs);
  return {
    tagName: 'SELECT',
    options,
    selectedIndex: 0,
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(a, name) ? a[name] : null; },
    setAttribute(name, value) { a[name] = String(value); },
    select(value) { this.selectedIndex = this.options.findIndex((o) => o.value === value); },
  };
}
function option(value, style) {
  return { value, getAttribute(name) { return name === 'style' ? (style || null) : null; } };
}

function load(selects) {
  const listeners = {};
  const document = {
    readyState: 'complete',
    addEventListener(type, fn) { listeners[type] = fn; },
    querySelectorAll() { return selects; },
  };
  const sandbox = { document };
  sandbox.window = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(block, sandbox);
  return { sandbox, listeners };
}

test('field shows the selected value with its choice row style and follows changes', () => {
  const field = element({ 'data-ob-choice-style-value': '1', 'data-ob-base-style': 'flex:1', style: 'flex:1;color:rgb(250,0,0)' }, [
    option(''), option('full', 'color:rgb(250,0,0)'), option('part', 'background:rgb(255,255,0)'), option('plain'),
  ]);
  field.select('full');
  const plain = element({ style: 'flex:1' }, [option(''), option('full', 'color:rgb(250,0,0)')]);
  plain.select('full');
  const { listeners } = load([field, plain]);
  assert.equal(field.getAttribute('style'), 'flex:1;color:rgb(250,0,0)', 'initial paint keeps the server style');

  field.select('part');
  listeners.change({ target: field });
  assert.equal(field.getAttribute('style'), 'flex:1;background:rgb(255,255,0)', 'partially restricted → yellow background only');

  field.select('plain');
  listeners.change({ target: field });
  assert.equal(field.getAttribute('style'), 'flex:1', 'ordinary value → no styling');

  field.select('');
  listeners.change({ target: field });
  assert.equal(field.getAttribute('style'), 'flex:1', 'empty value → no styling');

  listeners.change({ target: plain });
  assert.equal(plain.getAttribute('style'), 'flex:1', 'a field without choice_style_value is never repainted');
});

test('programmatic value changes are repainted by obPaintChoiceValues', () => {
  const field = element({ 'data-ob-choice-style-value': '1', 'data-ob-base-style': 'flex:1', style: 'flex:1' }, [
    option(''), option('arch', 'color:#FF4500'),
  ]);
  const { sandbox } = load([field]);
  field.select('arch');
  sandbox.window.obPaintChoiceValues();
  assert.equal(field.getAttribute('style'), 'flex:1;color:#FF4500');
});
