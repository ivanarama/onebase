const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const htmlPath = process.env.ONEBASE_ADMIN_USERS_HTML;
if (!htmlPath) throw new Error('ONEBASE_ADMIN_USERS_HTML is not set');
const selfPasswordResponse = JSON.parse(process.env.ONEBASE_SELF_PASSWORD_RESPONSE || 'null');
const html = fs.readFileSync(htmlPath, 'utf8');
const scriptStart = html.indexOf('<script>');
const scriptEnd = html.indexOf('</script>', scriptStart);
if (scriptStart < 0 || scriptEnd < 0) throw new Error('admin users production JavaScript not found');
const source = html.slice(scriptStart + '<script>'.length, scriptEnd);

function response({status = 200, contentType = 'application/json', body = {}, redirected = false, url = ''} = {}) {
  return {
    status,
    redirected,
    url,
    headers: {
      get(name) { return String(name).toLowerCase() === 'content-type' ? contentType : null; }
    },
    json() { return Promise.resolve(body); }
  };
}

function createHarness() {
  const queue = [];
  const calls = [];
  const elements = [];
  const navigations = [];
  const body = {
    children: [],
    appendChild(child) { this.children.push(child); child.parentNode = this; },
    removeChild(child) {
      const index = this.children.indexOf(child);
      if (index >= 0) this.children.splice(index, 1);
    },
  };
  const document = {
    body,
    createElement(tagName) {
      const element = {
        tagName: String(tagName).toLowerCase(),
        style: {},
        children: [],
        value: '',
        appendChild(child) { this.children.push(child); child.parentNode = this; },
        focus() {},
      };
      elements.push(element);
      return element;
    },
    getElementById() { return null; },
  };
  const context = {
    fetch(url, options) {
      calls.push({url, options});
      if (!queue.length) return Promise.reject(new Error(`unexpected fetch ${url}`));
      return Promise.resolve(queue.shift());
    },
    document,
    window: {location: {assign(url) { navigations.push(url); }}},
    setTimeout(fn) { fn(); },
    console,
  };
  vm.createContext(context);
  vm.runInContext(source, context, {filename: 'admin-users.js'});
  return {
    context,
    calls,
    elements,
    navigations,
    enqueue(value) { queue.push(response(value)); },
  };
}

test('successful own password change explains the revoked session and opens login', async () => {
  assert.equal(selfPasswordResponse && selfPasswordResponse.currentSessionEnded, true);
  const h = createHarness();
  h.enqueue({body: selfPasswordResponse});

  h.context.cfgUserPasswd('current-admin');
  const inputs = h.elements.filter((element) => element.tagName === 'input');
  const buttons = h.elements.filter((element) => element.tagName === 'button');
  assert.equal(inputs.length, 2);
  inputs[0].value = 'An0ther-Str0ng!';
  inputs[1].value = 'An0ther-Str0ng!';
  buttons[0].onclick();
  await new Promise((resolve) => setImmediate(resolve));

  const infoHTML = h.elements
    .filter((element) => element.tagName === 'div')
    .map((element) => element.textContent || '')
    .find((value) => value.includes('Configurator session has ended'));
  assert.match(infoHTML, /The Configurator session has ended — sign in again/);
  const infoButton = h.elements.filter((element) => element.tagName === 'button').at(-1);
  infoButton.onclick();
  assert.deepEqual(h.navigations, ['/bases/cfg-users-browser/configurator/login']);
});

// #1570: cfgInfo получает текст серверной ошибки и обязан показывать его
// буквально — innerHTML интерпретировал <, >, & как разметку.
test('cfgInfo renders the server message as literal text', async () => {
  const h = createHarness();
  let closed = 0;
  h.context.cfgInfo('<b>Ошибка</b> & <img src=x onerror="alert(1)"> "кавычки"', function () { closed++; });

  const message = h.elements
    .filter((element) => element.tagName === 'div')
    .find((element) => (element.textContent || '').includes('<b>Ошибка</b> & <img'));
  assert.ok(message, 'div с литеральным текстом сообщения не найден');
  assert.equal(message.children.length, 0, 'HTML внутри сообщения создал вложенные элементы');
  assert.ok((message.textContent || '').includes('"кавычки"'));

  const ok = h.elements.filter((element) => element.tagName === 'button').at(-1);
  ok.onclick();
  assert.equal(closed, 1, 'OK должен закрывать окно и звать callback ровно один раз');
});

test('cfgPost reports the localized configurator login redirect', async () => {
  const h = createHarness();
  h.enqueue({
    status: 200,
    contentType: 'text/html; charset=utf-8',
    redirected: true,
    url: 'http://example.test/bases/db/configurator/login',
    body: '<form>login</form>',
  });

  await assert.rejects(
    h.context.cfgPost('users/passwd', {id: 'admin', password: 'new'}),
    /The Configurator session has ended — sign in again/,
  );
  assert.equal(h.calls.length, 1);
  assert.equal(h.calls[0].options.method, 'POST');
});

test('cfgPost rejects a JSON handler error', async () => {
  const h = createHarness();
  h.enqueue({status: 409, body: {error: 'last administrator cannot be deleted'}});

  await assert.rejects(
    h.context.cfgPost('users/delete', {id: 'admin'}),
    /last administrator cannot be deleted/,
  );
});

test('cfgPost returns a successful JSON payload', async () => {
  const h = createHarness();
  h.enqueue({body: {ok: true, sessionStarted: false}});

  const result = await h.context.cfgPost('users/create', {login: 'user'});
  assert.equal(result.ok, true);
  assert.equal(result.sessionStarted, false);
});

test('cfgPost reports a localized unexpected non-JSON response', async () => {
  const h = createHarness();
  h.enqueue({status: 502, contentType: 'text/plain', body: 'bad gateway'});

  await assert.rejects(
    h.context.cfgPost('users/lang', {id: 'admin', lang: 'en'}),
    /Unexpected server response \(HTTP 502\)/,
  );
});
