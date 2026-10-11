const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');
const html = fs.readFileSync(process.env.ONEBASE_START_ERROR_HTML, 'utf8');
// html/template removes JavaScript comments. Bound each fragment by the
// rendered script element and, when needed, the next function declaration.
function scriptFrom(startMarker, endMarker) {
  const start = html.indexOf(startMarker);
  assert.ok(start >= 0, `missing script start: ${startMarker}`);
  const scriptStart = html.lastIndexOf('<script>', start);
  const previousScriptEnd = html.lastIndexOf('</script>', start);
  assert.ok(scriptStart >= 0 && scriptStart > previousScriptEnd,
    `script start is outside a script element: ${startMarker}`);
  const scriptEnd = html.indexOf('</script>', start);
  assert.ok(scriptEnd > start, `missing script end: ${startMarker}`);
  const end = endMarker ? html.indexOf(endMarker, start) : scriptEnd;
  assert.ok(end > start && end <= scriptEnd,
    `missing or out-of-script boundary: ${endMarker || '</script>'}`);
  return html.slice(start, end);
}
const start = scriptFrom('function startBase(el, id)');
const isolated = scriptFrom('function startIsolated(', 'function cleanProfiles(');
for (const mode of ['startBase', 'startBaseNative', 'startIsolated']) {
  for (const warning of [false, true]) {
    test(`${mode}: successful launch returns to ${warning ? 'warning banner' : 'base list'}`, async () => {
      const baseWindow = {location: {}, document: {write() {}, close() {}}};
      const launcherWindow = {location: {}, open() { return baseWindow; }};
      const result = {url: 'http://127.0.0.1:8080', ok: true};
      if (warning) result.launcher_url = '/?sel=base-control&flash=warning-key';
      const context = {
        _nativeOK: false,
        window: launcherWindow,
        document: {getElementById() { return null; }, addEventListener() {}},
        suppressEvent() {}, startButton() { return null; }, setStartButtonHTML() {},
        setTimeout(fn) { fn(); }, fetch() { return Promise.resolve({json: () => Promise.resolve(result)}); },
        showStartError() { throw new Error('warning became a startup error'); },
        showStartErrorModal() { throw new Error('warning became a startup error'); }
      };
      vm.createContext(context);
      vm.runInContext(start + '\n' + isolated, context);
      context[mode](null, 'base-control', '');
      await new Promise(resolve => setImmediate(resolve));
      assert.equal(launcherWindow.location.href, result.launcher_url || '/?sel=base-control');
      if (mode === 'startBase') assert.equal(baseWindow.location.href, result.url);
    });
  }
}
