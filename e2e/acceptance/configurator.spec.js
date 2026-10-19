const { test, expect } = require('@playwright/test');

test('configurator creates, reorders and persists typed eq/in_hierarchy/is_folder conditions', async ({ page }) => {
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
  const editURL = `/bases/${process.env.OB_ACCEPTANCE_BASE}/configurator/forms/edit?entity=Заявка&name=объекта`;
  await page.goto(editURL, { waitUntil: 'domcontentloaded' });
  await page.locator('#canvas-host [data-node-id="elements.1"]').click();
  const cards = page.locator('.prop-choice-card');
  async function edit(action) {
    const pending = page.waitForResponse((response) => response.url().endsWith('/forms/edit-op') && response.request().method() === 'POST');
    await action();
    const response = await pending;
    expect(response.status()).toBe(200);
    expect((await response.json()).ok).toBe(true);
  }
  const row = (index, label) => cards.nth(index).locator('.prop-row').filter({ has: page.getByText(label, { exact: true }) });
  async function fill(index, label, value) {
    await edit(async () => {
      const input = row(index, label).locator('input');
      await input.fill(value);
      await input.press('Tab');
    });
  }
  await edit(() => page.getByRole('button', { name: '+ условие', exact: true }).click());
  await expect(cards).toHaveCount(1);
  await fill(0, 'field', 'Направление');
  await fill(0, 'from', 'Объект.Направление');
  await edit(() => row(0, 'Оператор').locator('select').selectOption('in_hierarchy'));
  // Exercise both public operators through the editor, then restore hierarchy.
  await edit(() => row(0, 'Оператор').locator('select').selectOption('eq'));
  await edit(() => row(0, 'Оператор').locator('select').selectOption('in_hierarchy'));
  await edit(() => page.getByRole('button', { name: '+ условие', exact: true }).click());
  await expect(cards).toHaveCount(2);
  await fill(1, 'field', 'is_folder');
  await edit(() => row(1, 'Источник').locator('select').selectOption('value'));
  await edit(() => row(1, 'value').locator('select').selectOption('true'));
  await edit(() => row(1, 'value').locator('select').selectOption('false'));
  await edit(() => cards.nth(1).getByRole('button', { name: '↑', exact: true }).click());
  await expect(row(0, 'field').locator('input')).toHaveValue('is_folder');
  await edit(() => cards.nth(0).getByRole('button', { name: '↓', exact: true }).click());
  await expect(row(0, 'field').locator('input')).toHaveValue('Направление');
  await Promise.all([
    page.waitForURL((url) => url.searchParams.has('saved')),
    page.locator('button[onclick="saveForm()"]').click(),
  ]);
  // A new GET must rebuild the canvas/model from persisted YAML.
  await page.goto(editURL, { waitUntil: 'domcontentloaded' });
  await page.locator('#canvas-host [data-node-id="elements.1"]').click();
  await expect(cards).toHaveCount(2);
  await expect(row(0, 'field').locator('input')).toHaveValue('Направление');
  await expect(row(0, 'Оператор').locator('select')).toHaveValue('in_hierarchy');
  await expect(row(0, 'from').locator('input')).toHaveValue('Объект.Направление');
  await expect(row(1, 'field').locator('input')).toHaveValue('is_folder');
  await expect(row(1, 'Оператор').locator('select')).toHaveValue('eq');
  await expect(row(1, 'value').locator('select')).toHaveValue('false');
  const yaml = await page.evaluate(() => getYAML());
  expect(yaml).toContain('value: false');
  expect(yaml).not.toContain('value: "false"');
  expect(yaml).toContain('# acceptance: preserve neighboring YAML comment');
  expect(pageErrors).toEqual([]);
});
