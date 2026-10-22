const { test, expect } = require('@playwright/test');

test('ValueTable columns survive native payload and real activation round trips', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(String(error)));
  await page.addInitScript(() => {
    window.vtResponses = [];
    const fetch = window.fetch;
    window.fetch = async (...args) => {
      const response = await fetch(...args);
      if (String(args[0]).includes('/form-event')) {
        const json = response.json.bind(response);
        response.json = async () => {
          const value = await json();
          // Observe completion after the real response has reached managed.js.
          setTimeout(() => window.vtResponses.push(value), 0);
          return value;
        };
      }
      return response;
    };
  });
  await page.goto('/ui/catalog/Контрагент/new');
  const body = page.locator('tbody[data-vt-fields]:not([data-ob-table-readonly="1"])');
  await expect(body.locator('tr')).toHaveCount(2);
  const responseCount = () => page.evaluate(() => window.vtResponses.length);
  const payload = () => page.evaluate(() => {
    const data = new FormData(document.getElementById('main-form'));
    return Object.fromEntries([...new Set(data.keys())].filter(k => k.startsWith('vt.')).map(k => [k, data.getAll(k)]));
  });
  async function assertPayload() {
    const data = await payload();
    for (let row = 0; row < 2; row++) {
      for (const field of ['Name', 'Locked', 'VisibleFlag', 'Flag', 'ID']) {
        expect(data[`vt.Results.${row}.${field}`]).toHaveLength(1);
      }
      expect(data[`vt.Results.${row}.Flag`]).toEqual([row === 0 ? 'true' : 'false']);
      expect(data[`vt.Results.${row}.VisibleFlag`]).toEqual([row === 0 ? 'true' : 'false']);
    }
    expect(Object.keys(data)).toHaveLength(10);
  }
  await assertPayload();
  let count = await responseCount();
  await body.locator('tr').nth(0).locator('td').first().click();
  await expect.poll(responseCount).toBe(++count);
  let response = await page.evaluate(() => window.vtResponses.at(-1));
  expect(response.ok).toBe(true);
  expect(response.messages).toEqual(['row0', 'true', 'true']);
  await assertPayload();
  await body.locator('tr').nth(0).locator('td').first().click();
  await page.waitForTimeout(150);
  expect(await responseCount()).toBe(count);
  await body.locator('tr').nth(1).locator('td').first().click();
  await expect.poll(responseCount).toBe(++count);
  response = await page.evaluate(() => window.vtResponses.at(-1));
  expect(response.ok).toBe(true);
  expect(response.messages).toEqual(['row1', 'false', 'false']);
  // Hidden values changing at the same index must invalidate the selection.
  await page.locator('[data-ob-fire-click="Replace"]').click();
  await expect.poll(responseCount).toBe(++count);
  expect(await page.evaluate(() => window.vtResponses.at(-1).ok)).toBe(true);
  await body.locator('tr').nth(1).locator('td').first().click();
  await expect.poll(responseCount).toBe(++count);
  expect(await page.evaluate(() => window.vtResponses.at(-1).ok)).toBe(true);
  // The real delegated Add button uses the same plan and row activation.
  await page.locator('[data-ob-add-vt="Results"]').click();
  await expect(body.locator('tr')).toHaveCount(3);
  const added = body.locator('tr').last();
  await expect(added).toHaveAttribute('data-ob-vt-row', '2');
  await expect(added.locator('input[type="number"]')).toHaveAttribute('readonly', '');
  await expect(added.locator('td').nth(3)).toBeHidden();
  await expect(added.locator('td').nth(4)).toBeHidden();
  await added.locator('input[type="text"]').first().fill('new row');
  await added.locator('td').first().click();
  await expect.poll(responseCount).toBe(++count);
  response = await page.evaluate(() => window.vtResponses.at(-1));
  expect(response.ok).toBe(true);
  expect(response.messages).toEqual(['new row', 'false', 'false']);
  expect(errors).toEqual([]);
});
