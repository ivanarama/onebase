'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

// ПолеКартинки с data_path: ответ события формы меняет реквизит с
// идентификатором вложения — applyValues обновляет скрытое поле, а
// refreshPictureBindings перерисовывает картинку и ссылку «открыть в полном
// размере». Не-UUID картинку прячет и в src/href не попадает.

const managed = fs.readFileSync('static/managed.js', 'utf8');
function slice(beginMark, endMark) {
  const start = managed.indexOf(beginMark);
  const end = managed.indexOf(endMark, start);
  assert.ok(start >= 0 && end > start, 'slice not found: ' + beginMark);
  return managed.slice(start, end + endMark.length);
}
const src = slice('// BEGIN onebase-ro-apply-values', '// END onebase-ro-apply-values') + '\n' +
  slice('// BEGIN onebase-picture-bind', '// END onebase-picture-bind') +
  '\nthis.applyValues = applyValues;';

function node(attrs) {
  const el = { attrs: new Map(Object.entries(attrs || {})), value: attrs && attrs.value || '', hidden: false, children: [] };
  el.getAttribute = (n) => (el.attrs.has(n) ? el.attrs.get(n) : null);
  el.setAttribute = (n, v) => el.attrs.set(n, String(v));
  el.removeAttribute = (n) => el.attrs.delete(n);
  el.querySelector = (sel) => (sel === 'img' ? el.children[0] || null : null);
  return el;
}

function setup(initial, withEditor, missingEditor, renderedAttrs) {
  // withEditor: реквизит редактирует ПолеВвода — скрытое поле превью без name,
  // значение у редактора (#2177).
  const input = node(withEditor
    ? { type: 'hidden', 'data-ob-picture-for': 'ПолеФото', 'data-ob-picture-field': 'ФотоПревью', value: initial }
    : { type: 'hidden', name: 'ФотоПревью', 'data-ob-picture-for': 'ПолеФото', 'data-ob-picture-field': 'ФотоПревью', value: initial });
  if (renderedAttrs) input.attrs = new Map(Object.entries(renderedAttrs));
  const field = input.getAttribute('data-ob-picture-field');
  input.type = 'hidden';
  input.tagName = 'INPUT';
  const editor = withEditor ? node({ type: 'text', name: 'ФотоПревью', value: initial }) : null;
  if (editor) { editor.type = 'text'; editor.tagName = 'INPUT'; }
  // Resolve name at query time: a preview can become the submitting carrier
  // when the GET omitted its editor inside a hidden container.
  const named = () => missingEditor
    ? (input.getAttribute('name') === field ? input : null)
    : (editor || input);
  const img = node({ src: '' });
  const link = node({ href: '' });
  link.children.push(img);
  const empty = node({});
  const form = {
    querySelector(sel) {
      if (sel === '[name="' + field + '"]') return named();
      if (sel === '[data-ob-picture-link="ПолеФото"]') return link;
      if (sel === '[data-ob-picture-empty="ПолеФото"]') return empty;
      return null;
    },
    // applyValues обновляет все копии реквизита (querySelectorAll по name).
    querySelectorAll(sel) {
      if (sel === 'input[data-ob-picture-for]') return [input];
      return sel === '[name="' + field + '"]' && named() ? [named()] : [];
    },
  };
  const ctx = {
    window: {},
    document: { getElementById: (id) => (id === 'main-form' ? form : null) },
    managedRefParts: () => null,
    ensureRefOption: () => {},
  };
  vm.createContext(ctx);
  vm.runInContext(src, ctx);
  return { ctx, input, editor, img, link, empty };
}

test('ответ события с идентификатором вложения показывает картинку и ссылку', () => {
  const t = setup('');
  const id = '3f2504e0-4f89-11d3-9a0c-0305e82c3301';
  t.ctx.applyValues({ 'ФотоПревью': id.toUpperCase() });
  const url = '/ui/attachments/' + id + '/download';
  assert.equal(t.img.getAttribute('src'), url);
  assert.equal(t.link.getAttribute('href'), url);
  assert.equal(t.link.hidden, false);
  assert.equal(t.empty.hidden, true);
});

test('не-UUID прячет картинку и не подставляет адрес', () => {
  const t = setup('3f2504e0-4f89-11d3-9a0c-0305e82c3301');
  t.ctx.applyValues({ 'ФотоПревью': 'javascript:alert(1)' });
  assert.equal(t.link.hidden, true);
  assert.equal(t.empty.hidden, false);
  assert.notEqual(t.img.getAttribute('src'), 'javascript:alert(1)');
  assert.notEqual(t.link.getAttribute('href'), 'javascript:alert(1)');
});

test('реквизит с полем ввода: превью берёт значение у редактора', () => {
  const t = setup('3f2504e0-4f89-11d3-9a0c-0305e82c3301', true);
  const id = '9a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d';
  t.ctx.applyValues({ 'ФотоПревью': id });
  assert.equal(t.editor.value, id, 'applyValues обновляет редактор');
  assert.equal(t.input.value, id, 'скрытое поле превью синхронизировано с редактором');
  assert.equal(t.img.getAttribute('src'), '/ui/attachments/' + id + '/download');
  assert.equal(t.link.hidden, false);
});

// The Go regression supplies attributes parsed from the public GET form.
if (process.argv.includes('--rendered-picture')) {
  const attrs = JSON.parse(fs.readFileSync(0, 'utf8'));
  test('GET формы: превью без редактора в DOM обновляется ответом события', () => {
    const t = setup(attrs.value, false, true, attrs);
    const field = attrs['data-ob-picture-field'];
    const id = '9a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d';
    t.ctx.applyValues({ [field]: id });
    assert.equal(t.input.value, id);
    assert.equal(t.img.getAttribute('src'), '/ui/attachments/' + id + '/download');
    assert.equal(t.link.getAttribute('href'), '/ui/attachments/' + id + '/download');
    assert.equal(t.link.hidden, false);
    t.ctx.applyValues({ Other: 'unrelated' });
    assert.equal(t.input.value, id, 'частичный ответ сохраняет превью');
    t.ctx.applyValues({ [field]: 'javascript:alert(1)' });
    assert.equal(t.link.hidden, true);
    assert.equal(t.empty.hidden, false);
    t.ctx.applyValues({ [field]: null });
    assert.equal(t.input.value, '');
    assert.equal(t.link.hidden, true);
  });
}
