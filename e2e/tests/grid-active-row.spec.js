// Текущая строка таблицы SlickGrid подсвечена постоянно, как в 1С (замечание
// 26.10, результаты поиска заявок): раньше фон был только при наведении мыши,
// а щелчок мимо таблицы оставлял строку без подсветки.
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

test('текущая строка таблицы подсвечена и после щелчка мимо таблицы', async ({ page }) => {
  await login(page);
  await open(page, '/ui/document/РеализацияТоваров/new');
  await page.locator('.managed-tab-btn').getByText('Товары', { exact: true }).click();
  const grid = page.locator('.ob-grid[data-sg-layout]').filter({ visible: true }).first();
  await expect(grid.locator('.slick-header-column').first()).toBeVisible();
  const add = page.locator('[data-ob-grid-add], [data-ob-rowadd], [data-ob-add-tp]').filter({ visible: true }).first();
  // Новая строка сразу открывает редактор с выпадающим списком — закрываем.
  for (let i = 0; i < 2; i++) {
    await add.click();
    await page.keyboard.press('Escape');
    await page.keyboard.press('Escape');
  }
  const rows = grid.locator('.slick-row');
  await expect(rows).toHaveCount(2);
  const bg = (i) => rows.nth(i).locator('.slick-cell').last().evaluate((el) => getComputedStyle(el).backgroundColor);

  await rows.nth(0).locator('.slick-cell').last().click();
  await expect(rows.nth(0)).toHaveClass(/\bactive\b/);
  // Мышь и фокус уходят из таблицы: щелчок по заголовку формы.
  await page.locator('h2').first().click();
  await page.mouse.move(0, 0);
  expect(await bg(0)).toBe('rgb(219, 234, 254)');
  expect(await bg(1)).not.toBe('rgb(219, 234, 254)');
});

// Строки с условным фоном (#2356): условное оформление задаёт фон с
// !important, и текущая строка должна оставаться видимой поверх него, а цвет
// текста условного оформления — сохраняться.
test('текущая строка видна поверх условного фона строки', async ({ page }) => {
  await login(page);
  await open(page, '/ui/document/ПодсветкаСтроки/new');
  const grid = page.locator('.ob-grid[data-sg-layout]').filter({ visible: true }).first();
  await expect(grid.locator('.slick-header-column').first()).toBeVisible();
  const add = page.locator('[data-ob-grid-add], [data-ob-rowadd], [data-ob-add-tp]').filter({ visible: true }).first();
  for (let i = 0; i < 2; i++) {
    await add.click();
    await page.keyboard.press('Escape');
    await page.keyboard.press('Escape');
  }
  const rows = grid.locator('.slick-row');
  await expect(rows).toHaveCount(2);
  const cell = (i) => rows.nth(i).locator('.slick-cell').last();
  const style = (i) => cell(i).evaluate((el) => {
    const cs = getComputedStyle(el);
    return { bg: cs.backgroundColor, color: cs.color };
  });

  await cell(0).click();
  await expect(rows.nth(0)).toHaveClass(/\bactive\b/);
  await page.locator('h2').first().click();
  await page.mouse.move(0, 0);
  const active = await style(0), other = await style(1);
  expect(active.bg).toBe('rgb(219, 234, 254)');
  expect(other.bg).toBe('rgb(254, 226, 226)');
  expect(active.color).toBe('rgb(153, 27, 27)');
});
