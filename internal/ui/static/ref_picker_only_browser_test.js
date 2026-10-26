'use strict';
const { chromium } = require('../../../e2e/node_modules/playwright');
const assert = require('node:assert/strict');
const cfg = JSON.parse(process.env.ONEBASE_REF_BROWSER);
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined });
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('dialog', d => d.accept());
  const url = cfg.base + '/ui/document/Заявка/' + cfg.id;
  const field = name => page.locator('[data-ob-el="' + name + '"]');
  const text = field('ПолеНеисправность').locator('[role=combobox]');
  const value = name => (name === 'Неисправность' ? field('ПолеНеисправность') : page).locator('input[type=hidden][name="' + name + '"]');
  const writes = [];
  page.on('request', r => {
    if (r.method() === 'POST' && (/\/ui\/document\/[^/]+\/[^/]+$/.test(new URL(r.url()).pathname) || r.url().endsWith('/form-close-intent'))) writes.push(r);
  });
  async function settled() {
    await page.waitForFunction(() => Array.from(document.querySelectorAll('[data-ob-ref-search]')).every(e => !e.hasAttribute('aria-busy')));
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  }
  async function reopen() { await page.goto(url); await text.waitFor(); }
  async function source(name) {
    const response = page.waitForResponse(r => r.url().endsWith('/form-event'));
    await page.getByRole('button', { name: 'Источник' + name, exact: true }).click();
    await (await response).finished();
    await page.waitForFunction(root => document.querySelector('[name="Направление"]').value === root, name === 'B' ? cfg.rootB : cfg.rootA);
    await settled();
  }
  async function pick(name, caption) {
    const event = name === 'ЛокальнаяНеисправность' ? page.waitForResponse(r => r.url().endsWith('/form-event')) : null;
    await field(name).locator('[data-ob-ref-picker]').click();
    await page.locator('#_rp-list ._rp-item').filter({ hasText: caption }).click();
    await page.locator('#_ref-picker-modal').waitFor({ state: 'detached' });
    if (event) assert.equal((await (await event).json()).ok, true);
    await settled();
  }
  async function save(expected) {
    const response = page.waitForResponse(r => r.request().method() === 'POST' && /\/ui\/document\/[^/]+\/[^/]+$/.test(new URL(r.url()).pathname));
    await page.getByRole('button', { name: 'Записать', exact: true }).click();
    const result = await response;
    assert.equal(result.status(), 303);
    assert.deepEqual(new URLSearchParams(result.request().postData()).getAll('Неисправность'), [expected]);
    await page.waitForLoadState('domcontentloaded');
    await reopen();
    assert.equal(await value('Неисправность').inputValue(), expected);
  }
  async function durable(sourceID, reference) {
    const other = await browser.newPage();
    try {
      await other.goto(url);
      await other.locator('[data-ob-el="ПолеНеисправность"] [role=combobox]').waitFor();
      assert.equal(await other.locator('input[name="Направление"]').inputValue(), sourceID);
      assert.equal(await other.locator('[data-ob-el="ПолеНеисправность"] input[name="Неисправность"]').inputValue(), reference);
    } finally { await other.close(); }
  }
  async function explicitClear() {
    const clear = field('ПолеНеисправность').getByRole('button', { name: 'Очистить', exact: true });
    await value('Неисправность').evaluate(e => { window.explicitChanges = 0; e.addEventListener('change', () => window.explicitChanges++); });
    // Cancel remains a non-mutating picker action, independent of clear.
    await field('ПолеНеисправность').locator('[data-ob-ref-picker]').click();
    await page.locator('#_ref-picker-modal').getByRole('button', { name: 'Отмена', exact: true }).click();
    assert.equal(await value('Неисправность').inputValue(), cfg.legacy);
    const count = writes.length;
    await clear.click();
    assert.equal(await text.inputValue(), '');
    assert.equal(await value('Неисправность').inputValue(), '');
    assert.deepEqual(await value('Неисправность').evaluate(e => new FormData(e.form).getAll(e.name)), ['']);
    assert.equal(await page.evaluate(() => window.explicitChanges), 1);
    assert.equal(writes.length, count, 'clear must not save the form');
    assert.equal(await clear.isDisabled(), true);
    await clear.evaluate(e => e.click());
    assert.equal(await page.evaluate(() => window.explicitChanges), 1, 'empty clear must not emit change');
    await save('');
    await durable(cfg.rootB, '');

    // Native button keyboard activation follows the readonly caption in tab order.
    await pick('ПолеНеисправность', 'needle legacy B');
    await value('Неисправность').evaluate(e => { window.explicitChanges = 0; e.addEventListener('change', () => window.explicitChanges++); });
    await text.focus(); await page.keyboard.press('Tab');
    assert.equal(await clear.evaluate(e => e === document.activeElement), true);
    await page.keyboard.press('Enter');
    assert.equal(await text.inputValue(), '');
    assert.equal(await page.evaluate(() => window.explicitChanges), 1);
    await save(''); await durable(cfg.rootB, '');
    await pick('ПолеНеисправность', 'needle legacy B');
    await save(cfg.legacy);

    // save:false attributes are observed by a real form-event handler, not
    // presented as durable object fields.
    await pick('ЛокальнаяНеисправность', 'needle legacy B');
    const observation = field('Наблюдение').locator('input');
    assert.notEqual(await observation.inputValue(), '');
    await value('ЛокальнаяНеисправность').evaluate(e => { window.localChanges = 0; e.addEventListener('change', () => window.localChanges++); });
    const changed = page.waitForResponse(r => r.url().endsWith('/form-event'));
    await field('ЛокальнаяНеисправность').getByRole('button', { name: 'Очистить', exact: true }).click();
    const observed = await changed;
    assert.equal((await observed.json()).ok, true);
    assert.deepEqual(new URLSearchParams(observed.request().postData()).getAll('ЛокальнаяНеисправность'), ['']);
    await settled();
    assert.equal(await page.evaluate(() => window.localChanges), 1);
    assert.equal(await value('ЛокальнаяНеисправность').inputValue(), '');
    assert.equal(await observation.inputValue(), '');
    await durable(cfg.rootB, cfg.legacy);

    // A hidden placement cannot clear the active one, even with a queued click.
    const inactive = field('СкрытаяНеисправность');
    const hiddenValue = inactive.locator('input[name="Неисправность"]');
    const hiddenBefore = await hiddenValue.inputValue();
    await hiddenValue.evaluate(e => { window.hiddenChanges = 0; e.addEventListener('change', () => window.hiddenChanges++); });
    await inactive.locator('[data-ob-ref-clear]').evaluate(e => e.dispatchEvent(new MouseEvent('click', { bubbles: true })));
    assert.equal(await hiddenValue.inputValue(), hiddenBefore);
    assert.equal(await page.evaluate(() => window.hiddenChanges), 0);
    assert.deepEqual(await value('Неисправность').evaluate(e => new FormData(e.form).getAll(e.name)), [cfg.legacy]);
    const locked = page.waitForResponse(r => r.url().endsWith('/form-event'));
    await page.locator('[name="Запрет"][type=checkbox]').check(); await (await locked).finished(); await settled();
    await value('Неисправность').evaluate(e => { window.lockedChanges = 0; e.addEventListener('change', () => window.lockedChanges++); });
    await field('ПолеНеисправность').locator('[data-ob-ref-clear]').evaluate(e => e.dispatchEvent(new MouseEvent('click', { bubbles: true })));
    assert.equal(await value('Неисправность').inputValue(), cfg.legacy);
    assert.equal(await page.evaluate(() => window.lockedChanges), 0);
    const unlocked = page.waitForResponse(r => r.url().endsWith('/form-event'));
    await page.locator('[name="Запрет"][type=checkbox]').uncheck(); await (await unlocked).finished(); await settled();
    assert.equal(await clear.isEnabled(), true);
    // Disabled fieldsets must also block a stale event, without readonly.
    await value('Неисправность').evaluate(e => e.closest('fieldset').disabled = true);
    await clear.evaluate(e => e.dispatchEvent(new MouseEvent('click', { bubbles: true })));
    assert.equal(await value('Неисправность').inputValue(), cfg.legacy);
    assert.equal(await page.evaluate(() => window.lockedChanges), 0);
    await reopen();

    // Hold a genuine response without AbortController. Clearing cancels its
    // generation, so the late selected_allowed result cannot change the field.
    await page.evaluate(() => { window.AbortController = undefined; });
    let release, ready;
    const held = new Promise(resolve => { release = resolve; });
    const received = new Promise(resolve => { ready = resolve; });
    let first = true;
    const old = async route => {
      if (!first || new URL(route.request().url()).searchParams.get('check_selected_id') !== cfg.legacy) return route.continue();
      first = false;
      const response = await route.fetch();
      ready(); await held; await route.fulfill({ response });
    };
    await page.route('**/ui/_ref-options/**', old);
    try {
      await page.getByRole('button', { name: 'ИсточникA', exact: true }).click(); await received;
      await value('Неисправность').evaluate(e => { window.lateChanges = 0; e.addEventListener('change', () => window.lateChanges++); });
      await clear.click();
      const delivered = page.waitForResponse(r => new URL(r.url()).searchParams.get('check_selected_id') === cfg.legacy);
      release(); await (await delivered).finished(); await settled();
      assert.equal(await value('Неисправность').inputValue(), '');
      assert.equal(await text.inputValue(), '');
      assert.equal(await page.evaluate(() => window.lateChanges), 1);
      await pick('ПолеНеисправность', 'needle 001');
      assert.notEqual(await value('Неисправность').inputValue(), '');
    } finally { release(); await page.unroute('**/ui/_ref-options/**', old); }
    await reopen();
  }
  async function refused() {
    const count = writes.length;
    await page.getByRole('button', { name: 'Записать', exact: true }).click();
    assert.equal(writes.length, count, 'invalid picker-only reference reached native HTTP save');
    for (const mode of ['save', 'post', 'save_and_select']) {
      const decision = await page.evaluate(mode => window.obRequestFormClose({reason: 'ok', mode}), mode);
      assert.equal(decision.allowed, false);
      assert.equal(decision.error, 'invalid-reference');
      assert.equal(decision.intentId, '');
    }
    assert.equal(writes.length, count, 'invalid picker-only reference reached a writing HTTP endpoint');
    await durable(cfg.rootB, cfg.legacy);
  }
  try {
    await reopen();
    assert.equal(await text.evaluate(e => e.readOnly), true);
    assert.equal(await value('Неисправность').inputValue(), cfg.legacy, 'opening must preserve old out-of-filter UUID');
    await source('B');
    await pick('ПолеНеисправность', 'needle legacy B');
    await save(cfg.legacy);
    await durable(cfg.rootB, cfg.legacy);
    if (cfg.optional) await explicitClear();
    else assert.equal(await field('ПолеНеисправность').locator('[data-ob-ref-clear]').count(), 0);
    for (const failure of ['network', 'null-items', 'missing-selected']) {
      await reopen();
      // A form attribute uses the same picker-only validation path.
      await pick('ЛокальнаяНеисправность', 'needle legacy B');
      const corrupt = async route => {
        const query = new URL(route.request().url()).searchParams;
        if (query.get('check_selected_id') !== cfg.legacy) return route.continue();
        if (failure === 'network') return route.abort();
        const response = await route.fetch();
        const body = await response.json();
        if (failure === 'null-items') body.items = null;
        else delete body.selected_allowed;
        await route.fulfill({ response, json: body });
      };
      await page.route('**/ui/_ref-options/**', corrupt);
      await source('A');
      await page.waitForFunction(() => {
        const status = document.querySelector('[data-ob-el="ПолеНеисправность"] [role=status]');
        return status.textContent === window.obUIMessage('refSearchError', 'refSearchError');
      });
      assert.equal(await value('Неисправность').inputValue(), cfg.legacy);
      assert.equal(await text.evaluate(e => e.readOnly && e.checkValidity()), true, 'native readonly constraint validation is insufficient');
      await refused();
      // Entity readonly must not mask an invalid editable form attribute.
      const locked = page.waitForResponse(r => r.url().endsWith('/form-event'));
      await page.locator('[name="Запрет"][type=checkbox]').check();
      await (await locked).finished(); await settled();
      assert.deepEqual(await value('Неисправность').evaluate(e => new FormData(e.form).getAll(e.name)), [cfg.legacy]);
      await refused();
      await page.unroute('**/ui/_ref-options/**', corrupt);
      // The real protected endpoint independently rejects B's UUID in A.
      const query = await value('Неисправность').evaluate(e => obRefChoiceQuery(e));
      const check = await page.request.get(cfg.base + '/ui/_ref-options/Неисправность?limit=50&check_selected_id=' + cfg.legacy + query);
      assert.equal(check.status(), 200);
      assert.equal((await check.json()).selected_allowed, false);
      // Successful current-context verification restores writing without reloading.
      await source('B');
      const unlocked = page.waitForResponse(r => r.url().endsWith('/form-event'));
      await page.locator('[name="Запрет"][type=checkbox]').uncheck();
      await (await unlocked).finished(); await settled();
      await save(cfg.legacy);
    }
    await reopen();
    await page.evaluate(() => { window.AbortController = undefined; });
    await value('Неисправность').evaluate(e => { window.refChanges = 0; e.addEventListener('change', () => window.refChanges++); });
    let release, ready;
    const held = new Promise(resolve => { release = resolve; });
    const received = new Promise(resolve => { ready = resolve; });
    let first = true;
    const old = async route => {
      if (!first || new URL(route.request().url()).searchParams.get('check_selected_id') !== cfg.legacy) return route.continue();
      first = false;
      const response = await route.fetch();
      assert.equal((await response.json()).selected_allowed, false);
      ready(); await held; await route.fulfill({ response });
    };
    await page.route('**/ui/_ref-options/**', old);
    try {
      await page.getByRole('button', { name: 'ИсточникA', exact: true }).click();
      await received;
      // Pending verification must refuse writes as well as a completed failure.
      await refused();
      const changed = page.waitForResponse(r => r.url().endsWith('/form-event'));
      await page.getByRole('button', { name: 'ИсточникB', exact: true }).click();
      await (await changed).finished();
      const delivered = page.waitForResponse(r => new URL(r.url()).searchParams.get('check_selected_id') === cfg.legacy);
      release(); await (await delivered).finished(); await settled();
      assert.equal(await value('Неисправность').inputValue(), cfg.legacy);
      assert.equal(await page.evaluate(() => window.refChanges), 0, 'stale A rejection cleared current B selection');
    } finally { release(); await page.unroute('**/ui/_ref-options/**', old); }
    await source('A');
    assert.equal(await value('Неисправность').inputValue(), '');
    assert.equal(await page.evaluate(() => window.refChanges), 1, 'selected_allowed:false must clear exactly once');
    assert.deepEqual(await value('Неисправность').evaluate(e => new FormData(e.form).getAll(e.name)), ['']);
    if (cfg.optional) await save('');
    else await refused();
    await pick('ПолеНеисправность', 'needle 001');
    const selected = await value('Неисправность').inputValue();
    assert.notEqual(selected, cfg.legacy);
    await save(selected);
    await durable(cfg.rootA, selected);
    assert.equal(await field('Просмотр').locator('[role=combobox]').isEditable(), false);
    assert.deepEqual(errors, []);
    console.log('PASS: picker-only required/optional, native/close HTTP refusal, durable pair, form attribute, readonly_when, malformed/network/pending/stale checks, one-change automatic and explicit mouse/keyboard clear, save:false observer, hidden/readonly/disabled fences and picker recovery');
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
