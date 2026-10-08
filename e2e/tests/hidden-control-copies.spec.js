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
    const source = page.locator(`[data-ob-el="Видимый${warehouse}"] select`);
    const hidden = page.locator(`[data-ob-el="Скрытый${warehouse}"]`);
    const target = page.locator(`select[name="${location}"]`);
    await expect(hidden).toHaveAttribute('disabled', '');
    await expect(hidden).toBeHidden();
    const mainID = await source.locator('option').filter({ hasText: 'Главный склад «Чёрная дыра»' }).getAttribute('value');
    const discountID = await source.locator('option').filter({ hasText: 'Склад уценёнки «Хламоприёмник»' }).getAttribute('value');
    expect(mainID).toBeTruthy();
    expect(discountID).toBeTruthy();

    await source.selectOption(mainID);
    await expect.poll(async () => (await target.locator('option').allTextContents()).join('\n'))
      .toContain('Стеллаж 1 «Покосившийся»');
    expect(await page.locator('#main-form').evaluate((form, name) => new FormData(form).getAll(name), warehouse)).toEqual([mainID]);

    const response = page.waitForResponse((resp) => {
      const url = new URL(resp.url());
      return url.pathname.includes('/_ref-options/') && url.searchParams.get('q') === '' &&
        JSON.parse(url.searchParams.get('sources') || '{}')[`Объект.${warehouse}`] === mainID;
    });
    await target.locator('..').locator('[data-ob-ref-picker]').click();
    const data = await (await response).json();
    expect(data.total).toBe(3);
    await expect(page.locator('#_rp-list')).toContainText('Стеллаж 1 «Покосившийся»');
    await page.keyboard.press('Escape');
    await source.selectOption(discountID);
    await expect.poll(async () => (await target.locator('option').allTextContents()).join('\n'))
      .not.toContain('Стеллаж 1 «Покосившийся»');
    await source.selectOption(mainID);
    await expect.poll(async () => (await target.locator('option').allTextContents()).join('\n'))
      .toContain('Стеллаж 1 «Покосившийся»');
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
