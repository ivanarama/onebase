// Подсказки браузера в полях форм — ПДн (замечание 26.10): Chrome ведёт историю
// ввода по имени поля и подсказывал телефон или адрес одного клиента в поле
// другого. Сама подсказка — окно браузера, а не DOM, Playwright его не видит.
// Поэтому тест проверяет то, по чему Chrome решает, вести ли историю и
// показывать ли её: autocomplete="off" у поля в момент ввода. Проверено на
// Chromium 109: при "off" запись не сохраняется и подсказки не запрашиваются,
// при "one-time-code", без атрибута и у type="search" — сохраняются.
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

// Текстовые поля страницы, у которых браузер мог бы вести историю ввода.
const leaks = (page) => page.evaluate(() => Array.from(document.querySelectorAll('input, textarea'))
  .filter((el) => el.tagName === 'TEXTAREA' || /^(|text|search|tel|email|url|number)$/.test((el.getAttribute('type') || '').toLowerCase()))
  .filter((el) => el.getAttribute('autocomplete') !== 'off')
  .map((el) => `${el.tagName.toLowerCase()} name=${el.name || '-'} autocomplete=${el.getAttribute('autocomplete')}`));

test('у текстовых полей формы, таблицы, списка и поиска нет истории ввода', async ({ page }) => {
  await login(page);

  await open(page, '/ui/document/РеализацияТоваров/new');
  await expect(page.locator('#main-form')).toBeVisible();
  await page.waitForLoadState('load').catch(() => {});
  expect(await leaks(page), 'форма документа и поиск в шапке').toEqual([]);

  // Редактор ячейки SlickGrid создаётся скриптом при правке строки.
  await page.locator('.managed-tab-btn').getByText('Товары', { exact: true }).click();
  const grid = page.locator('.ob-grid[data-sg-layout]').filter({ visible: true }).first();
  await expect(grid.locator('.slick-header-column').first()).toBeVisible();
  await page.locator('[data-ob-grid-add], [data-ob-rowadd], [data-ob-add-tp]').filter({ visible: true }).first().click();
  await grid.locator('.slick-row').first().locator('.slick-cell').nth(1).dblclick();
  await expect(grid.locator('.slick-cell input')).toHaveCount(1);
  expect(await leaks(page), 'с редактором ячейки').toEqual([]);

  // Поле, дорисованное скриптом уже после загрузки, — тоже без истории.
  const late = await page.evaluate(async () => {
    const input = document.createElement('input');
    input.name = 'Телефон';
    input.setAttribute('autocomplete', 'one-time-code');
    document.body.appendChild(input);
    await new Promise((resolve) => setTimeout(resolve, 0));
    return input.getAttribute('autocomplete');
  });
  expect(late).toBe('off');

  await open(page, '/ui/catalog/Номенклатура');
  await page.waitForLoadState('load').catch(() => {});
  expect(await leaks(page), 'список справочника').toEqual([]);
});

// Поле входа и пароль сохраняют свои значения: им автозаполнение нужно.
test('пароль на странице профиля не превращается в off', async ({ page }) => {
  await login(page);
  await open(page, '/ui/profile/2fa');
  const values = await page.evaluate(() => Array.from(document.querySelectorAll('input[type=password]')).map((el) => el.getAttribute('autocomplete')));
  for (const v of values) expect(v).not.toBe('off');
});
