// Run the production feed with the continuation rendered by the HTTP handler.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
async function main() {
  const fixture = JSON.parse(fs.readFileSync(0, 'utf8'));
  const source = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
  const start = source.indexOf('function obInitFeed(');
  const end = source.indexOf('\nobReady(', start);
  assert.ok(start >= 0 && end > start);
  const attrs = {'data-next': '2', 'data-pages': '4', 'data-container': '#list-body', 'data-item': 'tr'};
  const link = {href: fixture.next};
  let click;
  const more = {
    isConnected: true,
    getAttribute: (name) => attrs[name],
    setAttribute: (name, value) => { attrs[name] = String(value); },
    querySelector: () => link,
    addEventListener: (_name, handler) => { click = handler; },
    getBoundingClientRect: () => ({top: 10000})
  };
  const container = {isConnected: true, appendChild() {}};
  const root = {querySelector: (sel) => sel === '#feed-more' ? more : sel === '#list-body' ? container : null};
  const requests = [];
  const app = {
    URL, URLSearchParams,
    window: {location: new URL('https://onebase.test/ui/catalog/other?sort=Name&dir=desc'), innerHeight: 800},
    document: {documentElement: {}, importNode: (item) => item},
    fetch: async (url) => { requests.push(url); return {text: async () => '<tr></tr>'}; },
    DOMParser: class { parseFromString() { return {querySelectorAll: () => [{}]}; } }
  };
  vm.createContext(app);
  vm.runInContext(source.slice(start, end), app);
  app.obInitFeed(root);
  for (let page = 2; page <= 3; page++) {
    click({target: {closest: () => link}, preventDefault() {}});
    await new Promise(setImmediate);
  }
  process.stdout.write(JSON.stringify(requests));
}
main().catch((err) => { console.error(err); process.exitCode = 1; });
