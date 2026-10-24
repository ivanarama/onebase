const { test, expect } = require('@playwright/test');
const { ids = {}, refs = {}, locals = {} } = JSON.parse(process.env.OB_HIDDEN_REF_ATTR_FIXTURE || '{}');
const oldID = refs['Исходный склад'];
const newID = refs['Выбранный склад'];
const visible = page => page.locator('.ob-ref-combobox:visible').getByRole('combobox');
async function choose(page, id) {
  const label = id === oldID ? 'Исходный склад' : 'Выбранный склад';
  await visible(page).fill(label);
  await visible(page).locator('..').getByRole('option', {name: label, exact: true}).click();
  await submitted(page, id);
}
const row = page => page.locator('[data-ob-control-fieldset]').filter({ has: visible(page) });
let pageErrors;
test.beforeEach(async ({ page }) => {
  pageErrors = [];
  page.on('pageerror', e => pageErrors.push(e.message));
});
test.afterEach(() => expect(pageErrors).toEqual([]));

async function submitted(page, value) {
  await expect(visible(page).locator('..').locator('input[type=hidden][name="Склад"]')).toHaveValue(value);
  expect(await page.locator('#main-form').evaluate(f => new FormData(f).getAll('Склад'))).toEqual([value]);
}
async function fire(page, button) {
  const [response] = await Promise.all([
    page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/form-event')),
    page.getByRole('button', { name: button, exact: true }).click(),
  ]);
  const data = await response.json();
  expect(data.ok).toBe(true);
  expect(data.error || '').toBe('');
  return data;
}
async function picker(page) {
  const [response] = await Promise.all([
    page.waitForResponse(r => decodeURI(r.url()).includes('/ui/_ref-options/Склад')),
    row(page).locator('[data-ob-ref-picker]').click(),
  ]);
  expect(response.ok()).toBe(true);
  await expect(page.locator('#_rp-list ._rp-item').filter({ hasText: 'Выбранный склад' })).toBeVisible();
}
async function card(page, id) {
  const opened = page.context().waitForEvent('page');
  await row(page).locator('[data-ob-ref-current]').click();
  const popup = await opened;
  await expect(popup).toHaveURL(new RegExp(`/ui/catalog/[^/]+/${id}`));
  await popup.waitForLoadState();
  await popup.close();
}

for (const [name, id] of Object.entries(ids)) {
  const url = `/ui/catalog/${name}/${id}?lang=ru`;
  async function open(page) {
    await page.goto(url);
    await expect(page.locator('input[type=hidden][name="Склад"]')).toHaveCount(2);
    await expect(visible(page)).toHaveCount(1);
  }
  test(`${name}: own picker, event, both visibility directions and save:false reload`, async ({ page }) => {
    await open(page);
    // Initial selection is deliberately different from the picker result.
    await choose(page, oldID);
    await picker(page);
    await page.locator('#_rp-list ._rp-item').filter({ hasText: 'Выбранный склад' }).click();
    await expect(page.locator('#_ref-picker-modal')).toHaveCount(0);
    await submitted(page, newID);
    // Before any event the hidden copy is still empty, so global-ID card lookup fails too.
    await expect(page.locator('[data-ob-control-fieldset]:not(:visible) input[type=hidden][name="Склад"]')).toHaveValue('');
    await card(page, newID);
    for (let i = 0; i < 2; i++) {
      const data = await fire(page, 'Переключить');
      expect(data.values.Получено).toBe('Выбранный склад');
      await submitted(page, newID);
      // Keep the hidden copy deliberately different after the real event too.
      await page.locator('select[name="Склад"]:not(:visible)').evaluate((s, id) => { s.value = id; }, oldID);
      await card(page, newID);
      await picker(page);
      await page.locator('#_rp-list ._rp-item').filter({ hasText: 'Выбранный склад' }).click();
      await submitted(page, newID);
    }
    const [save] = await Promise.all([
      page.waitForResponse(r => r.request().method() === 'POST' && !r.url().endsWith('/form-event') && decodeURI(r.url()).includes(`/ui/catalog/${name}/`)),
      page.locator('button[name="_action"][value=""]').first().click(),
    ]);
    expect(save.status()).toBeLessThan(400);
    await page.waitForLoadState();
    await submitted(page, newID);
    // POST and redirect restore the local attr through the existing stash; a
    // fresh server GET restores persistent fields but leaves save:false empty.
    expect((await fire(page, 'Проверить')).values.Получено).toBe('Выбранный склад');
    await picker(page);
    await page.locator('#_rp-cancel').click();
    await submitted(page, newID);
    await visible(page).fill('');
    await submitted(page, '');
    expect((await fire(page, 'Проверить')).values.Получено).toBe('Пусто');
    await submitted(page, '');
    await page.reload();
    await submitted(page, locals[name] ? '' : newID);
  });

  test(`${name}: readonly blocks selection and card reads its own copy`, async ({ page }) => {
    await open(page);
    await choose(page, newID);
    // readonly is computed by a real event, preserving the copy's selected value.
    await fire(page, 'Заблокировать');
    await expect(visible(page)).not.toBeEditable();
    await expect(row(page).locator('[data-ob-ref-picker]')).toBeDisabled();
    await expect(row(page).locator('[data-ob-ref-current]')).toBeEnabled();
    await card(page, newID);
    await expect(page.locator('#_ref-picker-modal')).toHaveCount(0);
  });
}
