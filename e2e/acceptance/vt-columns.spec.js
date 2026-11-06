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

for (const selected of [null, 0, 1]) {
  test(`ValueTable deletion reindexes activation and payload (selected=${selected})`, async ({ page }) => {
    const errors = [];
    const responses = [];
    page.on('pageerror', error => errors.push(String(error)));
    page.on('response', async response => {
      if (response.url().includes('/form-event')) responses.push(await response.json());
    });
    await page.goto('/ui/catalog/Контрагент/new');
    const body = page.locator('tbody[data-vt-fields]:not([data-ob-table-readonly="1"])');
    const rows = body.locator('tr');
    await expect(rows).toHaveCount(2);
    async function activate(index, messages) {
      const count = responses.length;
      const reply = page.waitForResponse(response => response.url().includes('/form-event'));
      await rows.nth(index).locator('td').first().click();
      const response = await (await reply).json();
      expect(response.ok).toBe(true);
      expect(response.messages).toEqual(messages);
      // Wait for the real event echo to finish replacing the row DOM.
      await expect(rows.nth(index).locator('input[type="text"]').first()).toHaveValue(messages[0]);
      await expect.poll(() => responses.length).toBe(count + 1);
      await rows.nth(index).locator('td').first().click();
      await page.waitForTimeout(150);
      expect(responses).toHaveLength(count + 1);
    }
    if (selected !== null) {
      await activate(selected, selected === 0 ? ['row0', 'true', 'true'] : ['row1', 'false', 'false']);
    }
    const count = responses.length;
    await rows.first().locator('[data-ob-remove-row]').click();
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toHaveAttribute('data-ob-vt-row', '0');
    const payload = await page.evaluate(() => [...new FormData(document.getElementById('main-form'))]
      .filter(([name]) => name.startsWith('vt.')));
    expect(payload).toEqual([
      ['vt.Results.0.Name', 'row1'], ['vt.Results.0.Locked', '7'],
      ['vt.Results.0.VisibleFlag', 'false'], ['vt.Results.0.Flag', 'false'], ['vt.Results.0.ID', 'id1'],
    ]);
    await page.waitForTimeout(150);
    expect(responses).toHaveLength(count);
    // The surviving row now occupies the old selected index; it must activate.
    await activate(0, ['row1', 'false', 'false']);
    // Add after deletion must not reuse an existing transport index.
    await page.locator('[data-ob-add-vt="Results"]').click();
    await expect(rows).toHaveCount(2);
    await expect(rows.last()).toHaveAttribute('data-ob-vt-row', '1');
    await rows.last().locator('input[type="text"]').first().fill('added');
    await activate(1, ['added', 'false', 'false']);
    // Deleting the selected last row, then the final row, also clears selection.
    await rows.last().locator('[data-ob-remove-row]').click();
    await expect(rows).toHaveCount(1);
    await activate(0, ['row1', 'false', 'false']);
    await rows.first().locator('[data-ob-remove-row]').click();
    await expect(rows).toHaveCount(0);
    await page.locator('[data-ob-add-vt="Results"]').click();
    await rows.first().locator('input[type="text"]').first().fill('replacement');
    await activate(0, ['replacement', 'false', 'false']);
    expect(errors).toEqual([]);
  });
}
