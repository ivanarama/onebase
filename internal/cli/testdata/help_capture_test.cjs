const { test } = require('node:test');
const assert = require('node:assert/strict');
const { capture } = require('../help_capture.cjs');

function fixture() {
  const actions = [];
  let closed = false;
  const page = {
    setDefaultTimeout() {}, emulateMedia: async () => {},
    goto: async url => actions.push(['goto', url]),
    locator: selector => ({
      fill: async value => actions.push(['fill', selector, value]),
      click: async () => actions.push(['click', selector]),
      selectOption: async value => actions.push(['select', selector, value]),
      waitFor: async () => actions.push(['wait', selector]),
      screenshot: async opts => actions.push(['screenshot', selector, opts]),
    }),
    screenshot: async opts => actions.push(['screenshot', 'page', opts]),
  };
  const chromium = { launch: async () => ({
    newContext: async opts => { actions.push(['context', opts]); return { newPage: async () => page }; },
    close: async () => { closed = true; },
  }) };
  return { actions, chromium, closed: () => closed };
}

test('capture uses role state, performs steps, masks data and closes browser', async () => {
  const f = fixture();
  await capture({ output: '/images', storageState: '/secret/state.json', scenario: {
    base_url: 'http://127.0.0.1:8080', screenshots: [{ file: 'call.png', path: '/ui/document/Call/new',
      steps: [{ action: 'fill', selector: '#name', value: 'Example' }, { action: 'click', selector: '#open' }],
      wait_for: '#ready', mask: ['.private'], hide: ['.topbar'], selector: 'main',
    }],
  } }, f.chromium);
  assert.equal(f.actions[0][1].storageState, '/secret/state.json');
  assert.ok(f.actions.some(a => a[0] === 'fill' && a[2] === 'Example'));
  const shot = f.actions.find(a => a[0] === 'screenshot');
  assert.equal(shot[1], 'main');
  assert.equal(shot[2].mask.length, 1);
  assert.equal(shot[2].animations, 'disabled');
  assert.ok(shot[2].style.includes('.topbar{visibility:hidden!important}'));
  assert.ok(f.closed());
});

test('invalid destinations and steps fail and close browser', async () => {
  for (const shot of [
    { file: '../escape.png', path: '/ui/' },
    { file: 'safe.png', path: 'https://elsewhere.example/' },
    { file: 'safe.png', path: '/ui/', steps: [{ action: 'eval', selector: 'body' }] },
  ]) {
    const f = fixture();
    await assert.rejects(capture({ output: '/images', scenario: { base_url: 'http://127.0.0.1', screenshots: [shot] } }, f.chromium));
    assert.ok(f.closed());
  }
});
