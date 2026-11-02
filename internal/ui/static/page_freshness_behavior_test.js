const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// Свежесть страницы: из bfcache — перечитать; ответ другого запуска сервера —
// сообщить и перезагрузить, а несохранённый ввод не выбрасывать молча.
const uiSource = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const begin = uiSource.indexOf('// BEGIN onebase-page-freshness');
const end = uiSource.indexOf('// END onebase-page-freshness', begin);
if (begin < 0 || end < 0) throw new Error('page freshness source markers not found');
const source = uiSource.slice(begin, end);

function setup({ boot = 'aaa', dirty = false } = {}) {
  const listeners = {};
  const timers = [];
  const appended = [];
  let reloads = 0;
  const el = (tag) => {
    const node = {
      tag, attrs: {}, children: [], style: {}, textContent: '', listeners: {},
      setAttribute(k, v) { this.attrs[k] = String(v); },
      getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; },
      appendChild(c) { this.children.push(c); },
      addEventListener(n, fn) { this.listeners[n] = fn; },
    };
    return node;
  };
  const meta = el('meta');
  meta.setAttribute('content', boot);
  const window = {
    _obFormDirty: dirty,
    location: { reload() { reloads++; } },
    addEventListener(n, fn) { listeners[n] = fn; },
  };
  const document = {
    querySelector: (sel) => (sel === 'meta[name="ob-boot"]' ? meta : null),
    createElement: el,
    body: { appendChild(c) { appended.push(c); } },
  };
  const context = { window, document, setTimeout: (fn, ms) => timers.push({ fn, ms }) };
  vm.runInNewContext(source, context);
  return { window, listeners, timers, appended, reloads: () => reloads };
}

const response = (boot) => ({ headers: { get: (k) => (k === 'X-OB-Boot' ? boot : null) } });

test('страница из bfcache перечитывается, обычный показ — нет', () => {
  const s = setup();
  s.listeners.pageshow({ persisted: false });
  assert.equal(s.reloads(), 0);
  s.listeners.pageshow({ persisted: true });
  assert.equal(s.reloads(), 1);
});

test('метка запуска сервера: другая — перезапуск, та же или нет заголовка — нет', () => {
  const s = setup({ boot: 'aaa' });
  assert.equal(s.window.obServerRestarted(response('bbb')), true);
  assert.equal(s.window.obServerRestarted(response('aaa')), false);
  assert.equal(s.window.obServerRestarted(response(null)), false);
});

test('после перезапуска чистая форма сообщает и перезагружается сама', () => {
  const s = setup({ dirty: false });
  s.window.obReloadAfterRestart();
  s.window.obReloadAfterRestart(); // повторный вызов не дублирует сообщение
  assert.equal(s.appended.length, 1);
  assert.match(s.appended[0].textContent, /Сервер обновлён/);
  assert.equal(s.timers.length, 1);
  s.timers[0].fn();
  assert.equal(s.reloads(), 1);
});

test('после перезапуска форма с несохранённым вводом ждёт решения человека', () => {
  const s = setup({ dirty: true });
  s.window.obReloadAfterRestart();
  assert.equal(s.timers.length, 0, 'несохранённый ввод не выбрасывается по таймеру');
  const button = s.appended[0].children[0];
  assert.equal(button.textContent, 'Перезагрузить');
  button.listeners.click();
  assert.equal(s.reloads(), 1);
});
