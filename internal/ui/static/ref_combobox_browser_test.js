'use strict';
const { chromium } = require('../../../e2e/node_modules/playwright');
const assert = require('node:assert/strict');
const cfg = JSON.parse(process.env.ONEBASE_REF_BROWSER);
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined });
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', e => { errors.push(e.message); console.error('PAGE ERROR',e.message); });
  page.on('response', r => { if(r.status()>=400) console.error('HTTP ERROR',r.status(),r.url()); });
  page.on('dialog', d => d.accept());
  const formURL = cfg.base + '/ui/document/Заявка/' + cfg.id;
  const hidden = name => page.locator('input[type=hidden][name="' + name + '"]');
  const input = name => page.locator('[data-ob-el="' + name + '"] [role=combobox]');
  const text = input('ПолеНеисправность');
  const options = page.locator('[data-ob-el="ПолеНеисправность"] [role=option]');
  async function search(q) {
    await text.fill(q);
    await options.first().waitFor({ state: 'visible' });
  }
  try {
    await page.goto(formURL);
    await text.waitFor();
    assert.equal(await hidden('Неисправность').count(), 1);
    assert.equal(await input('Просмотр').isEditable(), false);
    assert.equal(await hidden('Неисправность').inputValue(), cfg.legacy);
    assert.equal(await text.inputValue(), 'needle legacy B');
    assert.equal(await text.getAttribute('aria-controls'), await page.locator('[data-ob-el="ПолеНеисправность"] [role=listbox]').getAttribute('id'));
    assert.equal(await page.locator('label[for="' + await text.getAttribute('id') + '"]').count(), 1);
    if (cfg.optional) {
      async function saveAndClose() {
        const ok = page.getByRole('button', { name: 'ОК', exact: true });
        const destination = new URL(await ok.getAttribute('data-ob-close-href'), cfg.base).href;
        let result;
        // Read the real server response before forwarding it to the controller:
        // an allowed close navigates away and can discard the page response body.
        const forward = async route => {
          const response = await route.fetch();
          result = await response.json();
          await route.fulfill({ response });
        };
        await page.route('**/form-close-intent', forward);
        try {
          const navigation = page.waitForURL(destination, { waitUntil: 'domcontentloaded' });
          await Promise.all([navigation, ok.click()]);
          assert.equal(result.close.saved, true);
          assert.equal(result.close.allowed, true);
        } finally {
          await page.unroute('**/form-close-intent', forward);
        }
      }
      // A real native save establishes a durable optional reference first.
      await search('needle');
      await options.first().click();
      const selected = await hidden('Неисправность').inputValue();
      const nativeSave = page.waitForResponse(r => r.request().method() === 'POST' && r.url().includes('/ui/document/'));
      await page.getByRole('button', { name: 'Записать', exact: true }).click();
      assert.equal((await nativeSave).status(), 303);
      await page.goto(formURL);
      await text.waitFor();
      assert.equal(await hidden('Неисправность').inputValue(), selected);
      assert.equal(await text.evaluate(e => e.required), false);
      let closeRequests = 0;
      page.on('request', r => { if (r.url().includes('/form-close-intent')) closeRequests++; });
      await text.fill('unselected reference');
      assert.equal(await text.evaluate(e => e.checkValidity()), false);
      assert.equal(await hidden('Неисправность').inputValue(), '');
      await page.getByRole('button', { name: 'ОК', exact: true }).click();
      // Await controller completion, not a fixed delay, then inspect transport.
      const refused = await page.evaluate(() => window.obRequestFormClose({reason: 'ok', mode: 'save'}));
      assert.equal(refused.allowed, false);
      assert.equal(refused.error, 'invalid-reference');
      for (const mode of ['post', 'save_and_select']) {
        const decision = await page.evaluate(mode => window.obRequestFormClose({reason: 'ok', mode}), mode);
        assert.equal(decision.allowed, false);
        assert.equal(decision.error, 'invalid-reference');
      }
      assert.equal(closeRequests, 0, 'invalid optional text reached the write endpoint');
      assert.equal(await text.inputValue(), 'unselected reference');
      assert.equal(page.url(), new URL(formURL).href);
      // A separate GET proves that the old durable UUID survived the refusal.
      const stored = await browser.newPage();
      await stored.goto(formURL);
      await stored.locator('[data-ob-el="ПолеНеисправность"] [role=combobox]').waitFor();
      assert.equal(await stored.locator('input[type=hidden][name="Неисправность"]').inputValue(), selected);
      await stored.close();
      await search('needle');
      await options.nth(1).click();
      const replacement = await hidden('Неисправность').inputValue();
      assert.notEqual(replacement, selected);
      await saveAndClose();
      // Reopen only after close navigation finishes; this GET checks durable data.
      await page.goto(formURL);
      await text.waitFor();
      assert.equal(await hidden('Неисправность').inputValue(), replacement);
      // Empty optional input remains a valid explicit clear.
      await text.fill('');
      assert.equal(await text.evaluate(e => e.checkValidity()), true);
      await saveAndClose();
      await page.goto(formURL);
      await text.waitFor();
      assert.equal(await hidden('Неисправность').inputValue(), '');
      assert.deepEqual(errors, []);
      console.log('PASS: optional reference OK/save/post/save_and_select refusal, durable UUID, valid replacement and clear');
      return;
    }
    await text.fill('n');
    assert.equal(await hidden('Неисправность').inputValue(), '');
    assert.equal(await text.evaluate(e => e.checkValidity()), false);
    await text.press('Enter');
    assert.equal(await hidden('Неисправность').inputValue(), '');
    await search('needle');
    assert.deepEqual(await options.allTextContents(), ['needle 001', 'needle 002']); // RLS and choice_filter
    await text.press('Enter'); // ambiguous text is not a reference
    assert.equal(await hidden('Неисправность').inputValue(), '');
    await text.press('ArrowUp');
    assert.equal(await options.last().getAttribute('aria-selected'), 'true');
    await text.press('ArrowDown');
    assert.equal(await options.first().getAttribute('aria-selected'), 'true');
    assert.ok(await text.getAttribute('aria-activedescendant'));
    await text.press('Enter');
    assert.equal(await hidden('Неисправность').inputValue(), '20000000-0000-0000-0000-000000000001');
    assert.equal(await text.inputValue(), 'needle 001');
    await search('needle');
    await text.press('Tab');
    assert.equal(await hidden('Неисправность').inputValue(), '');
    assert.equal(await text.getAttribute('aria-expanded'), 'false');
    await search('needle');
    await options.nth(1).click();
    assert.equal(await hidden('Неисправность').inputValue(), cfg.pageTwo);
    await page.locator('[data-ob-el="ПолеНеисправность"] [data-ob-ref-picker]').click();
    await page.locator('#_rp-list ._rp-item').first().waitFor();
    await page.locator('#_rp-list ._rp-item').first().click();
    assert.notEqual(await hidden('Неисправность').inputValue(), '');
    // Real form-event and applyValues; the assigned reference is beyond page one.
    await page.getByRole('button', { name: 'Заполнить', exact: true }).click();
    await page.waitForFunction(() => document.querySelector('[name="Неисправность"]').value.endsWith('000000000051'));
    assert.equal(await text.inputValue(), 'element 051');
    // Programmatic assignment invalidates an already issued text search.
    await page.route('**/ui/_ref-options/**', async route => {
      const response = await route.fetch();
      await new Promise(r => setTimeout(r, 500));
      await route.fulfill({ response });
    });
    await text.fill('needle');
    await page.waitForRequest(r => new URL(r.url()).searchParams.get('q') === 'needle');
    await page.getByRole('button', { name: 'Заполнить', exact: true }).click();
    await page.waitForFunction(() => document.querySelector('[name="Неисправность"]').value.endsWith('000000000051'));
    await page.waitForTimeout(600);
    assert.equal(await text.inputValue(), 'element 051');
    assert.equal(await text.getAttribute('aria-expanded'), 'false');
    await page.unroute('**/ui/_ref-options/**');
    // An unchanged echo must preserve a query typed after the event snapshot.
    // Fetch the real response before holding it; no payload is fabricated.
    let releaseEcho, echoReady;
    const heldEcho = new Promise(resolve => { releaseEcho = resolve; });
    const readyEcho = new Promise(resolve => { echoReady = resolve; });
    await page.route('**/form-event', async route => {
      const response = await route.fetch();
      echoReady();
      await heldEcho;
      await route.fulfill({ response });
    });
    try {
      await page.getByRole('button', { name: 'Повторить', exact: true }).click();
      await readyEcho;
      await text.fill('needle');
      const delivered = page.waitForResponse(r => r.url().endsWith('/form-event'));
      releaseEcho();
      await (await delivered).finished();
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      assert.equal(await text.inputValue(), 'needle');
      assert.equal(await hidden('Неисправность').inputValue(), '');
      assert.equal(await text.evaluate(e => e.checkValidity()), false);
    } finally {
      releaseEcho();
      await page.unroute('**/form-event');
    }
    await search('element 051');
    await options.first().click();
    // hidden_when must disable the fieldset as well as closing the combobox.
    await search('needle');
    await page.locator('[name="Скрыть"][type=checkbox]').check();
    await text.waitFor({ state: 'hidden' });
    assert.equal(await text.evaluate(e => e.closest('fieldset').disabled), true);
    assert.equal(await text.getAttribute('aria-expanded'), 'false');
    assert.equal(await hidden('Неисправность').evaluate(e => new FormData(e.form).getAll(e.name).length), 0);
    await page.locator('[name="Скрыть"][type=checkbox]').uncheck();
    await text.waitFor({ state: 'visible' });
    assert.equal(await text.evaluate(e => e.closest('fieldset').disabled), false);
    assert.equal(await hidden('Неисправность').evaluate(e => new FormData(e.form).getAll(e.name).length), 1);
    // Conditional readonly travels through the public form-event response.
    await page.locator('[name="Запрет"][type=checkbox]').check();
    await page.waitForFunction(() => document.querySelector('[data-ob-el="ПолеНеисправность"] [role=combobox]').readOnly);
    assert.equal(await hidden('Неисправность').count(), 1);
    assert.equal(await text.evaluate(e => new FormData(e.form).getAll('Неисправность').length), 1);
    await page.locator('[name="Запрет"][type=checkbox]').uncheck();
    await page.waitForFunction(() => !document.querySelector('[data-ob-el="ПолеНеисправность"] [role=combobox]').readOnly);
    // Reversed HTTP responses, with abort unavailable, still require generations.
    await page.evaluate(() => { window.AbortController = undefined; });
    await page.route('**/ui/_ref-options/**', async route => {
      const q = new URL(route.request().url()).searchParams.get('q');
      const response = await route.fetch();
      if (q === 'needle') await new Promise(r => setTimeout(r, 500));
      await route.fulfill({ response });
    });
    await text.fill('needle');
    await page.waitForRequest(r => new URL(r.url()).searchParams.get('q') === 'needle');
    await search('element 051');
    await page.waitForTimeout(600);
    assert.deepEqual(await options.allTextContents(), ['element 051']);
    await options.first().click();
    await page.unroute('**/ui/_ref-options/**');
    // A changed source invalidates a saved value with observable change.
    await page.getByRole('button', { name: 'ИсточникB', exact: true }).click();
    await page.waitForFunction(() => !document.querySelector('[name="Неисправность"]').value);
    assert.equal(await text.inputValue(), '');
    await search('needle');
    assert.deepEqual(await options.allTextContents(), ['needle legacy B', 'needle other B']);
    // A network failure cannot bring back a UUID or use cached local candidates.
    await page.route('**/ui/_ref-options/**', route => route.abort());
    await text.fill('element');
    await page.waitForFunction(() => document.querySelector('[data-ob-el="ПолеНеисправность"] [role=status]').textContent.includes('проверить'));
    assert.equal(await text.evaluate(e => e.checkValidity()), false);
    assert.equal(await hidden('Неисправность').inputValue(), '');
    await page.unroute('**/ui/_ref-options/**');
    await page.getByRole('button', { name: 'ИсточникA', exact: true }).click();
    await page.waitForFunction(root => document.querySelector('[name="Направление"]').value === root, cfg.rootA);
    await text.fill('');
    assert.equal(await text.evaluate(e => e.checkValidity()), false);
    await search('needle');
    await text.press('Escape');
    assert.equal(await text.getAttribute('aria-expanded'), 'false');
    await search('needle');
    await options.first().click();
    const selected = await hidden('Неисправность').inputValue();
    // Actual native POST edit followed by GET of the saved UUID.
    const savedEdit = page.waitForResponse(r => r.request().method() === 'POST' && r.url().includes('/ui/document/'));
    await page.getByRole('button', { name: 'Записать', exact: true }).click();
    assert.equal((await savedEdit).status(), 303);
    await page.waitForLoadState('domcontentloaded');
    await page.goto(formURL);
    await text.waitFor();
    assert.equal(await hidden('Неисправность').inputValue(), selected);
    // Form-local reference uses the same keyboard control and single UUID.
    const local = input('ЛокальнаяНеисправность');
    await local.fill('needle');
    const localOpts = page.locator('[data-ob-el="ЛокальнаяНеисправность"] [role=option]');
    await localOpts.first().waitFor();
    await localOpts.first().click();
    assert.notEqual(await hidden('ЛокальнаяНеисправность').inputValue(), '');
    // Create through the managed form, then reopen the newly saved object.
    await page.goto(cfg.base + '/ui/document/Заявка/new');
    await page.getByRole('button', { name: 'ИсточникA', exact: true }).click();
    await page.waitForFunction(root => document.querySelector('[name="Направление"]').value === root, cfg.rootA);
    await search('needle');
    await options.first().click();
    await page.getByRole('button', { name: 'Записать', exact: true }).click();
    await page.waitForURL(url => !url.pathname.endsWith('/new'));
    await text.waitFor();
    assert.equal(await hidden('Неисправность').inputValue(), selected);
    // Native inline-create iframe returns its saved UUID to the enhanced control.
    await page.goto(cfg.inlineBase + '/ui/catalog/Заказ/new');
    await input('ПолеСклад').waitFor();
    await page.locator('[data-ob-el="ПолеСклад"] [data-ob-ref-picker]').click();
    await page.locator('#_rp-create').click();
    const popup = page.frameLocator('#_ref-create-modal iframe');
    await popup.locator('[name="Наименование"]').fill('Новый склад combobox');
    const inlineSave = page.waitForResponse(r => r.request().method() === 'POST' && r.url().includes('/ui/catalog/'));
    await popup.getByRole('button', { name: 'Записать и выбрать', exact: true }).click();
    const inlineResponse = await inlineSave;
    assert.equal(inlineResponse.status(), 200);
    await page.waitForFunction(() => document.querySelector('[name="Склад"]').value !== '');
    assert.equal(await input('ПолеСклад').inputValue(), 'Новый склад combobox');
    assert.equal(await hidden('Склад').count(), 1);
    assert.deepEqual(errors, []);
    console.log('PASS: public GET/search/keyboard/mouse/picker/event/readonly/RLS/races/network/POST edit+create');
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
