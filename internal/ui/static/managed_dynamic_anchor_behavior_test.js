'use strict';

// Настоящий applyElementStates из managed.js работает на дереве, которое
// production HTTP-обработчик формы отдал Go-тесту. Так тест одновременно
// сторожит якоря шаблона и поведение клиента после события формы.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');

const placementPath = process.env.ONEBASE_PLACEMENT_FIXTURE;
const placement = placementPath ? JSON.parse(fs.readFileSync(placementPath, 'utf8')) : null;
const domPath = process.env.ONEBASE_DYNAMIC_ANCHORS_DOM;
assert.ok(placement || domPath, 'a production HTTP form fixture is required');
const tree = placement ? placement.tree : JSON.parse(fs.readFileSync(domPath, 'utf8'));

const source = fs.readFileSync('static/managed.js', 'utf8');
function extract(name) {
  const start = source.indexOf('function ' + name);
  assert.ok(start >= 0, 'managed.js has no function ' + name);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') {
      depth--;
      if (depth === 0) return source.slice(start, i + 1);
    }
  }
  throw new Error('unterminated function ' + name);
}

function parseStyle(value) {
  const style = {};
  String(value || '').split(';').forEach((rule) => {
    const colon = rule.indexOf(':');
    if (colon < 0) return;
    style[rule.slice(0, colon).trim()] = rule.slice(colon + 1).trim();
  });
  return style;
}

class Element {
  constructor(spec) {
    this.tagName = String(spec.tag).toUpperCase();
    this.attributes = new Map(Object.entries(spec.attrs || {}));
    this.parentElement = null;
    this.children = (spec.children || []).map((child) => {
      const element = new Element(child);
      element.parentElement = this;
      return element;
    });
    this.style = parseStyle(this.attributes.get('style'));
    this.disabled = this.attributes.has('disabled');
    this.readOnly = this.attributes.has('readonly');
  }

  getAttribute(name) {
    return this.attributes.has(String(name)) ? this.attributes.get(String(name)) : null;
  }

  hasAttribute(name) {
    return this.attributes.has(String(name));
  }

  closest(selector) {
    const match = /^\[([a-z0-9-]+)\]$/.exec(String(selector));
    if (!match) throw new Error('unsupported closest selector: ' + selector);
    for (let node = this; node; node = node.parentElement) {
      if (node.attributes.has(match[1])) return node;
    }
    return null;
  }

  descendants() {
    const found = [];
    const walk = (node) => {
      for (const child of node.children) {
        found.push(child);
        walk(child);
      }
    };
    walk(this);
    return found;
  }

  querySelectorAll(selector) {
    const selectors = String(selector).split(',').map((part) => part.trim());
    const matches = (node, part) => {
      const match = /^([a-zA-Z][\w-]*)(?::not\(\[([\w-]+)\]\))?$/.exec(part);
      if (!match) throw new Error('unsupported descendant selector: ' + selector);
      return node.tagName === match[1].toUpperCase() && (!match[2] || !node.hasAttribute(match[2]));
    };
    return this.descendants().filter((node) => selectors.some((part) => matches(node, part)));
  }

  querySelector(selector) {
    // Карты состояний ключуются путём размещения (data-ob-el-path), а якорь
    // человекомочитаемого имени остаётся data-ob-el (#1543). Стаб понимает
    // оба атрибута.
    const match = /^\[data-ob-([a-z-]+)="([^"]+)"\]$/.exec(selector);
    if (!match) throw new Error('unsupported document selector: ' + selector);
    const attr = 'data-ob-' + match[1];
    return this.descendants().find((node) => node.getAttribute(attr) === match[2]) || null;
  }
}

function boot() {
  const root = new Element(tree);
  const context = {CSS: null};
  context.window = context;
  context.document = {querySelector(selector) { return root.querySelector(selector); }};
  const applyElementStates = new Function(
    'window', 'document', 'CSS',
    extract('applyElementStates') + '\nreturn applyElementStates;'
  )(context, context.document, context.CSS);
  return {root, applyElementStates};
}

function anchor(app, name) {
  const node = app.root.querySelector('[data-ob-el="' + name + '"]');
  assert.ok(node, 'rendered form has no data-ob-el=' + name);
  return node;
}

// Ключ в картах ответа события — путь размещения, прочитанный с того же узла.
function pathOf(app, name) {
  const path = anchor(app, name).getAttribute('data-ob-el-path');
  assert.ok(path, 'data-ob-el=' + name + ' has no data-ob-el-path');
  return path;
}

test('event state hides decorations and locks the real command bar', {skip: !!placement}, () => {
  const app = boot();
  const decorations = ['НадписьСтатуса', 'КартинкаСФайлом', 'КартинкаБезФайла'];
  const dynamic = decorations.concat('ФлажокСрочно', 'ПанельКоманд');
  const paths = Object.fromEntries(dynamic.map((name) => [name, pathOf(app, name)]));
  // Пути уникальны и не пусты: безымянные и одноимённые размещения обязаны
  // получать разные ключи, а не общий ключ имени (#1543).
  assert.equal(new Set(Object.values(paths)).size, dynamic.length);
  const checkbox = anchor(app, 'ФлажокСрочно');
  const panel = anchor(app, 'ПанельКоманд');
  const buttons = panel.querySelectorAll('button');
  assert.ok(buttons.length > 0, 'fixture has no real command-bar buttons');
  assert.equal(checkbox.style.display, 'flex');
  assert.equal(panel.style.display, 'flex');

  // The first event includes false values for every declared hidden_when.
  // Applying that response must not erase an inline layout declaration.
  app.applyElementStates({
    hidden: Object.fromEntries(dynamic.map((name) => [paths[name], false]))
  });
  for (const name of decorations) assert.equal(anchor(app, name).style.display, '');
  assert.equal(checkbox.style.display, 'flex');
  assert.equal(panel.style.display, 'flex');

  app.applyElementStates({
    hidden: Object.fromEntries(dynamic.map((name) => [paths[name], true])),
    readonly: {[paths['ПанельКоманд']]: true}
  });
  for (const name of decorations) assert.equal(anchor(app, name).style.display, 'none');
  assert.equal(checkbox.style.display, 'none');
  assert.equal(panel.style.display, 'none');
  for (const button of buttons) assert.equal(button.disabled, true);

  app.applyElementStates({
    hidden: Object.fromEntries(dynamic.map((name) => [paths[name], false])),
    readonly: {[paths['ПанельКоманд']]: false}
  });
  for (const name of decorations) assert.equal(anchor(app, name).style.display, '');
  assert.equal(checkbox.style.display, 'flex');
  assert.equal(panel.style.display, 'flex');
  for (const button of buttons) assert.equal(button.disabled, false);
});


test('HTTP event preserves admin locks on each repeated or unnamed placement', {skip: !placement}, () => {
  const app = boot();
  const copies = app.root.descendants().filter((node) => node.getAttribute('name') === 'ТипЗвонка');
  const open = app.root.descendants().find((node) => node.getAttribute('name') === 'Комментарий');
  assert.equal(copies.length, 2, 'both placements must render');
  assert.ok(open, 'neighbor must render');
  const controls = copies.concat(open);
  const paths = controls.map((node) => node.closest('[data-ob-el-path]').getAttribute('data-ob-el-path'));
  assert.ok(paths.every(Boolean));
  assert.equal(new Set(paths).size, 3);
  for (let i = 0; i < controls.length; i++) {
    const expected = i < 2 && placement.locked;
    assert.equal(controls[i].readOnly, expected, 'initial rendering');
    assert.equal(placement.states.readonly[paths[i]], expected, 'HTTP event path');
  }
  app.applyElementStates(placement.states);
  for (let i = 0; i < controls.length; i++) {
    assert.equal(controls[i].readOnly, i < 2 && placement.locked, 'client after actual event');
  }
});
