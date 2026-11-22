const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const ui = fs.readFileSync('static/ui.js', 'utf8');
const managed = fs.readFileSync('static/managed.js', 'utf8');
function slice(src, from, to) {
  const start = src.indexOf(from);
  const end = src.indexOf(to, start + from.length);
  if (start < 0 || end < 0) throw new Error('block is absent: ' + from);
  return src.slice(start, end);
}

// Реальные делегаты document из ui.js: делегат списка и делегат формы. Оба
// ловят щелчок по 🔍; до правки один щелчок открывал окно выбора дважды, и
// второе открытие отменяло запрос первого.
function runtime() {
  const handlers = [];
  const fields = { 'ref-Дом': { id: 'ref-Дом' } };
  const document = {
    addEventListener(type, fn) { if (type === 'click') handlers.push(fn); },
    querySelectorAll() { return []; },
    getElementById(id) { return fields[id] || null; },
  };
  const opened = [];
  const sandbox = {
    window: {}, document, console,
    openRefPicker(target) { opened.push(target); },
    obClosestSelect(btn) { return btn.select; },
    obReady() {},
  };
  sandbox.window = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(slice(ui, '// BEGIN onebase-ref-picker-claim', '// END onebase-ref-picker-claim'), sandbox);
  vm.runInContext(slice(ui, 'function obInitListDelegates() {', '\nfunction ') + '\nobInitListDelegates();', sandbox);
  vm.runInContext(slice(ui, 'function obInitFormDelegates() {', '\nfunction ') + '\nobInitFormDelegates();', sandbox);
  return { handlers, opened };
}

function click(button) {
  const target = {
    closest(selector) {
      if (selector === '[data-ob-ref-picker]' && button.attr) return button;
      if (selector === '[data-ob-ref-picker-self]' && button.self) return button;
      return null;
    },
  };
  return { target, preventDefault() {}, stopPropagation() {} };
}

for (const [name, button] of [
  ['автоформа: id поля', { attr: 'ref-Дом', getAttribute(k) { return k === 'data-ob-ref-picker' ? 'ref-Дом' : null; } }],
  ['управляемая форма: closest', { attr: 'closest', select: { id: 'sel' }, getAttribute(k) { return k === 'data-ob-ref-picker' ? 'closest' : null; } }],
]) {
  test(`один щелчок по 🔍 открывает окно выбора один раз (${name})`, () => {
    const env = runtime();
    assert.ok(env.handlers.length >= 2, 'оба делегата повешены на document');
    const ev = click(button);
    for (const h of env.handlers) h(ev);
    assert.equal(env.opened.length, 1);
    assert.ok(env.opened[0] && typeof env.opened[0] === 'object', 'открыто окно для найденного поля, а не для строки адреса');
    const again = click(button);
    for (const h of env.handlers) h(again);
    assert.equal(env.opened.length, 2, 'следующий щелчок открывает окно снова');
  });
}

test('делегат управляемой формы тоже берёт отметку щелчка', () => {
  const branch = slice(managed, "if (btn.hasAttribute('data-ob-ref-picker')) {", "if (btn.hasAttribute('data-ob-ref-current')) {");
  assert.match(branch, /obOpenRefPickerOnce\(e, pickSel\)/);
});
