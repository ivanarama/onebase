const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

// These copies come from a loaded YAML form with real hidden_when, not DOM
// clones. The test document is added only to the isolated E2E project.
for (const [order, warehouse, file, location] of [
  ['первая', 'СкладПервый', 'ФайлПервый', 'МестоПервое'],
  ['вторая', 'СкладВторой', 'ФайлВторой', 'МестоВторое'],
]) {
  test(`зависимый подбор: скрытая копия ${order}`, async ({ page }) => {
    await login(page);
    await open(page, '/ui/document/СкрытыеКопии/new');
    const source = page.locator(`[data-ob-el="Видимый${warehouse}"]`);
    const hidden = page.locator(`[data-ob-el="Скрытый${warehouse}"]`);
    const target = page.locator(`input[type="hidden"][name="${location}"]`);
    await expect(hidden).toHaveAttribute('disabled', '');
    await expect(hidden).toBeHidden();
    async function chooseWarehouse(label) {
      await source.getByRole('combobox').fill(label);
      await source.getByRole('option', { name: label, exact: true }).click();
      await expect(source.getByRole('combobox')).toHaveValue(label);
      const value = source.locator(`input[type="hidden"][name="${warehouse}"]`);
      await expect(value).not.toHaveValue('');
      const id = await value.inputValue();
      expect(await page.locator('#main-form').evaluate((form, name) => new FormData(form).getAll(name), warehouse)).toEqual([id]);
      return id;
    }
    async function openLocations(warehouseID) {
      const response = page.waitForResponse((resp) => {
        const url = new URL(resp.url());
        return url.pathname.includes('/_ref-options/') && url.searchParams.get('q') === '' &&
          JSON.parse(url.searchParams.get('sources') || '{}')[`Объект.${warehouse}`] === warehouseID;
      });
      // The UUID is inside the enhanced combobox; its picker is a sibling.
      await target.locator('../..').locator('[data-ob-ref-picker]').click();
      const result = await response;
      expect(result.ok()).toBe(true);
      return result.json();
    }

    const mainID = await chooseWarehouse('Главный склад «Чёрная дыра»');
    const mainLocations = await openLocations(mainID);
    expect(mainLocations.total).toBe(3);
    expect(mainLocations.items.map(item => item._label)).toContain('Стеллаж 1 «Покосившийся»');
    await expect(page.locator('#_rp-list')).toContainText('Стеллаж 1 «Покосившийся»');
    await page.keyboard.press('Escape');
    const discountID = await chooseWarehouse('Склад уценёнки «Хламоприёмник»');
    expect(discountID).not.toBe(mainID);
    const discountLocations = await openLocations(discountID);
    expect((discountLocations.items || []).map(item => item._label)).not.toContain('Стеллаж 1 «Покосившийся»');
    await expect(page.locator('#_rp-list')).not.toContainText('Стеллаж 1 «Покосившийся»');
    await page.keyboard.press('Escape');
    expect(await chooseWarehouse('Главный склад «Чёрная дыра»')).toBe(mainID);
    const restoredLocations = await openLocations(mainID);
    expect(restoredLocations.total).toBe(3);
    expect(restoredLocations.items.map(item => item._label)).toContain('Стеллаж 1 «Покосившийся»');
    await expect(page.locator('#_rp-list')).toContainText('Стеллаж 1 «Покосившийся»');
    await page.keyboard.press('Escape');
  });

  test(`выбор файла: скрытая копия ${order}`, async ({ page }) => {
    await login(page);
    await open(page, '/ui/document/СкрытыеКопии/new');
    const visible = page.locator(`[data-ob-el="Видимый${file}"]`);
    const hidden = page.locator(`[data-ob-el="Скрытый${file}"]`);
    await expect(hidden).toHaveAttribute('disabled', '');
    await expect(hidden).toBeHidden();
    const chooserPromise = page.waitForEvent('filechooser');
    await visible.locator('[data-ob-file-trigger]').click();
    const chooser = await chooserPromise;
    await chooser.setFiles({ name: 'review.txt', mimeType: 'text/plain', buffer: Buffer.from('Текст проверки', 'utf8') });
    await expect(visible.locator(`input[name="${file}"]`)).toHaveValue('review.txt');
    await expect(visible.locator('textarea')).toHaveValue('Текст проверки');
    await expect(hidden.locator('input[type="text"]')).toHaveValue('');
    await expect(hidden.locator('textarea')).toHaveValue('');
    const values = await page.locator('#main-form').evaluate((form, name) => {
      const data = new FormData(form);
      return {path: data.getAll(name), content: data.getAll('_fc_' + name)};
    }, file);
    expect(values).toEqual({path: ['review.txt'], content: ['Текст проверки']});
  });
}
