// Ширина колонки таблицы за пользователем, как порядок (замечание 26.10:
// «Адрес» в результатах поиска заявок): настоящее перетаскивание границы
// заголовка SlickGrid, запись через HTTP, свежая отрисовка формы и сброс
// кнопкой «Стандартные колонки».
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

async function openGoods(page) {
  await open(page, '/ui/document/РеализацияТоваров/new');
  await page.locator('.managed-tab-btn').getByText('Товары', { exact: true }).click();
  const grid = page.locator('.ob-grid[data-sg-layout]').filter({ visible: true }).first();
  await expect(grid.locator('.slick-header-column').first()).toBeVisible();
  return grid;
}

test('ширина колонки сохраняется за пользователем и сбрасывается', async ({ page }) => {
  await login(page);
  let grid = await openGoods(page);
  const key = await grid.getAttribute('data-sg-layout');
  // Чистое начало: прошлый прогон мог оставить ширины и порядок.
  expect((await page.request.post('/ui/_grid-widths', { form: { grid: key, widths: '' } })).status()).toBe(204);
  expect((await page.request.post('/ui/_grid-order', { form: { grid: key, order: '' } })).status()).toBe(204);
  grid = await openGoods(page);
  const header = () => grid.locator('.slick-header-column').nth(1);
  const width = () => header().evaluate((el) => el.offsetWidth);
  const before = await width();
  const reset = grid.locator('..').locator('[data-ob-grid-order-reset]');
  await expect(reset).toBeHidden();

  // Тянем правую границу второй колонки на 150 px. Ручка наполовину выходит
  // за заголовок и там обрезана — берёмся за видимую часть, где курсор
  // col-resize.
  const handle = header().locator('.slick-resizable-handle');
  const box = await handle.boundingBox();
  const x = box.x + 2, y = box.y + box.height / 2;
  expect(await page.evaluate(([px, py]) => !!document.elementFromPoint(px, py).closest('.slick-resizable-handle'), [x, y])).toBe(true);
  const saved = page.waitForResponse((r) => r.url().endsWith('/ui/_grid-widths') && r.request().method() === 'POST');
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + 75, y, { steps: 5 });
  await page.mouse.move(x + 150, y, { steps: 5 });
  await page.mouse.up();
  expect((await saved).status()).toBe(204);
  const dragged = await width();
  expect(Math.abs(dragged - (before + 150))).toBeLessThanOrEqual(4);
  await expect(reset).toBeVisible();

  // Новая отрисовка формы — та же ширина, и автоподбор её не трогает.
  grid = await openGoods(page);
  expect(await grid.getAttribute('data-sg-widths')).toBeTruthy();
  expect(Math.abs((await width()) - dragged)).toBeLessThanOrEqual(2);
  await expect(reset).toBeVisible();

  // «Стандартные колонки» возвращают ширину формы и забывают сохранённую.
  const cleared = page.waitForResponse((r) => r.url().endsWith('/ui/_grid-widths') && r.request().method() === 'POST');
  await reset.click();
  expect((await cleared).status()).toBe(204);
  expect(Math.abs((await width()) - before)).toBeLessThanOrEqual(4);
  await expect(reset).toBeHidden();
  grid = await openGoods(page);
  expect(await grid.getAttribute('data-sg-widths')).toBeNull();
  expect(Math.abs((await width()) - before)).toBeLessThanOrEqual(2);
});
