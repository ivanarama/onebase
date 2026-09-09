'use strict';

// Настоящий applyElementStates из managed.js работает на дереве, которое
// production HTTP-обработчик формы отдал Go-тесту. Так тест одновременно
// сторожит якоря шаблона и поведение клиента после события формы.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');

const domPath = process.env.ONEBASE_DYNAMIC_ANCHORS_DOM;
assert.ok(domPath, 'ONEBASE_DYNAMIC_ANCHORS_DOM must point to the rendered form tree');
const tree = JSON.parse(fs.readFileSync(domPath, 'utf8'));

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
    this.children = (spec.children || []).map((child) => new Element(child));
    this.style = parseStyle(this.attributes.get('style'));
    this.disabled = this.attributes.has('disabled');
    this.readOnly = this.attributes.has('readonly');
  }

  getAttribute(name) {
    return this.attributes.has(String(name)) ? this.attributes.get(String(name)) : null;
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
    const tags = String(selector).split(',').map((tag) => tag.trim().toUpperCase());
    if (tags.some((tag) => !/^[A-Z]+$/.test(tag))) {
      throw new Error('unsupported descendant selector: ' + selector);
    }
    return this.descendants().filter((node) => tags.includes(node.tagName));
  }

  querySelector(selector) {
    const match = /^\[data-ob-el="([^"]+)"\]$/.exec(selector);
    if (!match) throw new Error('unsupported document selector: ' + selector);
    return this.descendants().find((node) => node.getAttribute('data-ob-el') === match[1]) || null;
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

test('event state hides decorations and locks the real command bar', () => {
  const app = boot();
  const decorations = ['НадписьСтатуса', 'КартинкаСФайлом', 'КартинкаБезФайла'];
  const panel = anchor(app, 'ПанельКоманд');
  const buttons = panel.querySelectorAll('button');
  assert.ok(buttons.length > 0, 'fixture has no real command-bar buttons');

  app.applyElementStates({
    hidden: Object.fromEntries(decorations.concat('ПанельКоманд').map((name) => [name, true])),
    readonly: {ПанельКоманд: true}
  });
  for (const name of decorations) assert.equal(anchor(app, name).style.display, 'none');
  assert.equal(panel.style.display, 'none');
  for (const button of buttons) assert.equal(button.disabled, true);

  app.applyElementStates({
    hidden: Object.fromEntries(decorations.concat('ПанельКоманд').map((name) => [name, false])),
    readonly: {ПанельКоманд: false}
  });
  for (const name of decorations) assert.equal(anchor(app, name).style.display, '');
  assert.equal(panel.style.display, '');
  for (const button of buttons) assert.equal(button.disabled, false);
});
