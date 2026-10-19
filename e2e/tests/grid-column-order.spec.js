// Real SlickGrid drag/reset events, HTTP persistence and a fresh form render.
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

for (const scenario of ['moves', 'reset', 'network-error', 'http-error']) {
  test(`column order keeps the latest action after delayed ${scenario}`, async ({ page }) => {
    await login(page);
    await open(page, '/ui/document/РеализацияТоваров/new');
    await page.locator('.managed-tab-btn').getByText('Товары', { exact: true }).click();
    const grid = page.locator('.ob-grid[data-sg-layout]').filter({ visible: true }).first();
    await expect(grid.locator('.slick-header-column').first()).toBeVisible();
    const key = await grid.getAttribute('data-sg-layout');
    // Each test starts with the configured layout, regardless of earlier runs.
    const cleared = await page.request.post('/ui/_grid-order', { form: { grid: key, order: '' } });
    expect(cleared.status()).toBe(204);
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.locator('.managed-tab-btn').getByText('Товары', { exact: true }).click();
    const headers = grid.locator('.slick-header-column');
    const order = () => grid.evaluate(el => el._obGridState.columns.map(c => c.id));
    await expect(headers.first()).toBeVisible();
    const initial = await order();
    expect(initial.length).toBeGreaterThanOrEqual(3);
    await page.evaluate(() => {
      const original = window.fetch;
      window.gridOrderCalls = [];
      window.fetch = function(url, options) {
        if (url === '/ui/_grid-order') window.gridOrderCalls.push(options.body);
        return original.apply(this, arguments);
      };
    });
    let release, entered;
    const held = new Promise(resolve => { release = resolve; });
    const firstRequest = new Promise(resolve => { entered = resolve; });
    let started = 0, completed = 0;
    await page.route('**/ui/_grid-order', async route => {
      const number = ++started;
      if (number === 1) {
        entered();
        await held;
        if (scenario === 'network-error') { await route.abort('failed'); return; }
        if (scenario === 'http-error') { await route.fulfill({ status: 500, body: 'test failure' }); return; }
      }
      const response = await route.fetch();
      expect(response.status()).toBe(204);
      await route.fulfill({ response });
      completed++;
    });
    try {
      await headers.nth(0).dragTo(headers.nth(2));
      await firstRequest;
      await headers.nth(1).dragTo(headers.nth(0));
      if (scenario === 'reset') {
        await grid.locator('..').locator('[data-ob-grid-order-reset]').click();
      } else {
        await headers.nth(2).dragTo(headers.nth(0));
      }
      const expected = await order();
      expect(await page.evaluate(() => window.gridOrderCalls.length)).toBe(1);
      release();
      await expect.poll(() => started).toBe(2);
      await expect.poll(() => completed).toBe(scenario.endsWith('error') ? 1 : 2);
      expect(await page.evaluate(() => window.gridOrderCalls.length)).toBe(2);
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.locator('.managed-tab-btn').getByText('Товары', { exact: true }).click();
      await expect(headers.first()).toBeVisible();
      expect(await order()).toEqual(expected);
      if (scenario === 'reset') expect(expected).toEqual(initial);
      else expect(expected).not.toEqual(initial);
    } finally {
      release();
    }
  });
}
