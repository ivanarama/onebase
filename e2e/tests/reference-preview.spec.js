const { test, expect } = require('@playwright/test');
const path = require('path');

// Exercise the normal click delegate, network response and picker DOM with the
// complete production script. Only the page scaffold and option data are fixtures.
for (const contextual of [false, true]) {
  test(`preview is plain text (${contextual ? 'POST' : 'GET'} choices)`, async ({ page }) => {
    const text = '<img src="/preview-xss" onerror="window.previewXSS = true">\n'
      + '<b>Памятка & пояснение</b>\n  Вторая строка';
    let seenMethod;
    await page.route('**/preview-fixture', (route) => route.fulfill({
      contentType: 'text/html; charset=utf-8',
      body: `<!doctype html><html><head><meta charset="utf-8"><script src="/static/ui.js"></script></head><body>
        <form><select id="reference" data-ref-entity="Items"
          ${contextual ? 'data-ref-context="{}" data-ref-element="Reference"' : ''}>
          <option value=""></option></select>
          <button type="button" data-ob-ref-picker="reference">Выбрать</button></form>
        </body></html>`,
    }));
    await page.route('**/static/ui.js', (route) => route.fulfill({
      contentType: 'application/javascript; charset=utf-8',
      path: path.resolve(__dirname, '../../internal/ui/static/ui.js'),
    }));
    await page.route('**/ui/_ref-options/**', (route) => {
      seenMethod = route.request().method();
      return route.fulfill({ json: {
        preview: 'Info', total: 2,
        items: [
          { id: 'first', _label: 'С пояснением', Info: text },
          { id: 'empty', _label: 'Без пояснения', Info: '' },
        ],
      } });
    });
    let imageRequested = false;
    page.on('request', (request) => {
      if (request.url().endsWith('/preview-xss')) imageRequested = true;
    });

    await page.goto('/preview-fixture');
    await page.getByRole('button', { name: 'Выбрать', exact: true }).click();
    const preview = page.locator('#_rp-preview');
    await expect(preview).toBeVisible();
    // toHaveText normalizes whitespace; exact DOM text and innerText preserve
    // the line breaks and leading spaces that the user must see.
    await expect.poll(() => preview.textContent()).toBe(text);
    expect(await preview.innerText()).toBe(text);
    await expect(preview).toHaveCSS('white-space', 'pre-wrap');
    await expect(preview.locator('*')).toHaveCount(0);
    expect(await page.evaluate(() => window.previewXSS)).toBeUndefined();
    expect(imageRequested).toBe(false);
    expect(seenMethod).toBe(contextual ? 'POST' : 'GET');

    const search = page.locator('#_rp-search');
    await search.press('ArrowDown');
    await expect.poll(() => preview.textContent()).toBe('—');
    await search.press('ArrowUp');
    await expect.poll(() => preview.textContent()).toBe(text);
    await search.press('Enter');
    await expect(page.locator('#reference')).toHaveValue('first');
    await expect(page.locator('#_ref-picker-modal')).toHaveCount(0);
  });
}
