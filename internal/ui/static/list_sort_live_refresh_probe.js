// Called by the HTTP regression with attributes from the actual rendered list.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

async function main() {
  const fixture = JSON.parse(fs.readFileSync(0, 'utf8'));
  const source = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
  const begin = source.indexOf('// BEGIN onebase-live-list');
  const end = source.indexOf('// END onebase-live-list', begin);
  assert.ok(begin >= 0 && end > begin, 'production live-list listener is missing');
  const listeners = new Map();
  const timers = new Map();
  const requests = [];
  let timerID = 0;
  const container = {
    getAttribute: (name) => fixture.attrs[name] || null,
    hasAttribute: (name) => Object.hasOwn(fixture.attrs, name)
  };
  const app = {
    document: {
      readyState: 'complete', visibilityState: 'visible',
      addEventListener() {},
      querySelectorAll: () => [container]
    },
    location: {href: fixture.href},
    addEventListener: (name, handler) => { listeners.set(name, handler); },
    setTimeout: (fn) => { timers.set(++timerID, fn); return timerID; },
    clearTimeout: (id) => { timers.delete(id); },
    fetch: async (url, options) => {
      assert.equal(options.headers['X-Requested-With'], 'obLiveList');
      requests.push(url);
      // Go sends the captured URL through the real HTTP handler and checks rows.
      // No DOM replacement is needed in this URL-selection test.
      return {ok: false, status: 503};
    }
  };
  app.window = app;
  vm.createContext(app);
  vm.runInContext(source.slice(begin, end), app);
  for (const [event, detail] of [
    ['onebase:данные.клиент', {}],
    ['onebase:__oblive_refresh_all__', {}],
    ['onebase:ui.обновитьСписок', {entity: 'Клиент'}]
  ]) {
    assert.ok(listeners.has(event), 'missing production listener: ' + event);
    listeners.get(event)({detail});
    assert.equal(timers.size, 1, 'event must schedule exactly one refresh');
    const pending = [...timers.values()];
    timers.clear();
    pending.forEach((fn) => fn());
    await new Promise(setImmediate);
  }
  process.stdout.write(JSON.stringify(requests));
}
main().catch((error) => { console.error(error); process.exitCode = 1; });
