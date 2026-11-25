// choice_dropdown: false — ссылочное поле без выпадающего списка (задание
// 22.10). Раньше такое поле оставалось <select> со стрелкой: список
// раскрывался в одну строку «— выбрать —», а выбирать всё равно приходилось
// кнопкой «…». Теперь поле выглядит как поле значения: щелчок, Enter и F4
// открывают форму подбора, Shift+F4 и Delete очищают, значение уходит на
// сервер тем же <select> и сохраняется.

const { test, expect } = require('@playwright/test');
const { login, open, SAVE } = require('./helpers');

const MAIN = 'Главный склад «Чёрная дыра»';
const SALE = 'Склад уценёнки «Хламоприёмник»';

test('поле без списка: выбор подбором, очистка и запись', async ({ page }) => {
  await login(page);
  await open(page, '/ui/document/ПолеБезСписка/new');
  const field = page.locator('select[name="Склад"]');
  await expect(field).toHaveAttribute('data-ref-choice-dropdown', 'false');
  // Без стрелки: вид поля значения, а не выпадающего списка.
  expect(await field.evaluate((el) => getComputedStyle(el).appearance)).toBe('none');
  // Пустое значение — пустое поле, как в 1С: «— выбрать —» в узком поле
  // обрезалось и ничего не объясняло.
  const shown = () => field.evaluate((el) => el.selectedOptions[0] ? el.selectedOptions[0].text : '');
  expect(await shown()).toBe('');

  // Щелчок по самому полю открывает форму подбора, а не список.
  await field.click();
  await expect(page.locator('#_ref-picker-modal')).toBeVisible();
  await page.locator('#_rp-list ._rp-item').filter({ hasText: MAIN }).click();
  await expect(page.locator('#_ref-picker-modal')).toHaveCount(0);
  const mainID = await field.inputValue();
  expect(mainID).toMatch(/^[0-9a-f-]{36}$/);
  expect(await field.evaluate((el) => el.selectedOptions[0].text)).toBe(MAIN);

  // Стрелка у закрытого <select> молча сменила бы значение на соседний пункт.
  await field.focus();
  await page.keyboard.press('ArrowUp');
  await expect(field).toHaveValue(mainID);

  // Delete очищает, F4 снова открывает подбор.
  await page.keyboard.press('Delete');
  await expect(field).toHaveValue('');
  expect(await shown()).toBe('');
  await page.keyboard.press('F4');
  await expect(page.locator('#_ref-picker-modal')).toBeVisible();
  await page.locator('#_rp-list ._rp-item').filter({ hasText: SALE }).click();
  await expect(page.locator('#_ref-picker-modal')).toHaveCount(0);
  const saleID = await field.inputValue();
  expect(saleID).not.toBe(mainID);

  // Кнопка ✕ рядом с полем — та же очистка; Enter открывает подбор.
  await field.locator('..').locator('[data-ob-ref-clear]').click();
  await expect(field).toHaveValue('');
  await field.focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#_ref-picker-modal')).toBeVisible();
  await page.locator('#_rp-list ._rp-item').filter({ hasText: SALE }).click();
  await expect(field).toHaveValue(saleID);

  // Выбранное записывается и после перечитывания показывается в поле.
  await page.click(SAVE);
  await expect(page).not.toHaveURL(/\/new/);
  const url = page.url();
  await open(page, url);
  await expect(page.locator('select[name="Склад"]')).toHaveValue(saleID);
  expect(await page.locator('select[name="Склад"]').evaluate((el) => el.selectedOptions[0].text)).toBe(SALE);
});

// The YAML supplies a managed choice_filter field. Changing its source must
// keep the empty caption both before selection and after an invalidated value.
test('поле без списка: пустая подпись после смены источника choice_filter', async ({ page }) => {
  await login(page);
  await open(page, '/ui/document/ПолеБезСписка/new');
  const source = page.locator('select[name="Склад"]');
  const target = page.locator('select[name="Место"]');
  await expect(target).toHaveAttribute('data-ref-choice-dropdown', 'false');
  await expect(target).toHaveAttribute('data-ref-choice-context', /dependent-no-list/);
  const shown = () => target.evaluate(el => el.selectedOptions[0]?.text);
  await expect.poll(shown).toBe('');

  async function pickWarehouse(label) {
    await source.click();
    await expect(page.locator('#_ref-picker-modal')).toBeVisible();
    const response = page.waitForResponse(r => {
      const url = new URL(r.url());
      return r.request().method() === 'GET' && decodeURI(url.pathname).endsWith('/_ref-options/МестоХранения') &&
        url.searchParams.has('sources');
    });
    await page.locator('#_rp-list ._rp-item').filter({ hasText: label }).click();
    expect((await response).ok()).toBe(true);
    await expect(page.locator('#_ref-picker-modal')).toHaveCount(0);
  }

  await pickWarehouse(MAIN);
  await expect(target).toHaveValue('');
  await expect.poll(shown).toBe('');
  await expect(target.locator('option')).toHaveCount(1);

  await target.click();
  await expect(page.locator('#_ref-picker-modal')).toBeVisible();
  await page.locator('#_rp-list ._rp-item').filter({ hasText: 'Стеллаж 1 «Покосившийся»' }).click();
  await expect(page.locator('#_ref-picker-modal')).toHaveCount(0);
  expect(await target.inputValue()).toMatch(/^[0-9a-f-]{36}$/);
  await expect.poll(shown).toBe('Стеллаж 1 «Покосившийся»');

  await pickWarehouse(SALE);
  await expect(target).toHaveValue('');
  await expect.poll(shown).toBe('');
  await expect(target.locator('option')).toHaveCount(1);
});
