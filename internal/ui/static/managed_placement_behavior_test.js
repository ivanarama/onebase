'use strict';

// Настоящий applyElementStates из managed.js работает на дереве, которое
// production HTTP-обработчик формы отдал Go-тесту. Так тест одновременно
// сторожит раздельные права повторных и безымянных размещений после события.
// Динамические якоря проверяет настоящий браузер в e2e/tests/dynamic-anchors.spec.js.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');

const placementPath = process.env.ONEBASE_PLACEMENT_FIXTURE;
assert.ok(placementPath, 'a production HTTP placement fixture is required');
const placement = JSON.parse(fs.readFileSync(placementPath, 'utf8'));
const tree = placement.tree;

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

test('HTTP event preserves admin locks on each repeated or unnamed placement', () => {
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
