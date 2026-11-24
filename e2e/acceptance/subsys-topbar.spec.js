const { test, expect } = require('@playwright/test');

// Разделы — вкладками в тёмной верхней полосе: отдельной строки нет, высота
// рабочей области вкладок такая же, как у базы без разделов.
async function open(page, base) {
  expect((await page.request.post(base + '/auth/login', { data: { login: 'admin', password: 'secret123' } })).ok()).toBe(true);
  await page.goto(base + '/ui/app', { waitUntil: 'domcontentloaded' });
  await expect(page.locator('#ob-tabbody')).toBeVisible();
}
const metrics = page => page.evaluate(() => {
  const r = el => el ? el.getBoundingClientRect() : null;
  return { topbar: r(document.querySelector('.topbar')), body: r(document.getElementById('ob-tabbody')) };
});

test('subsystem tabs live inside the dark top bar and cost no working height', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 750 });
  await open(page, process.env.OB_ACCEPTANCE_PLAIN_URL);
  await expect(page.locator('.subsys-bar')).toHaveCount(0);
  const plain = await metrics(page);

  await page.context().clearCookies();
  await open(page, process.env.OB_ACCEPTANCE_URL);
  const bar = page.locator('.topbar > nav.subsys-bar');
  await expect(bar).toHaveCount(1);
  await expect(bar.locator('a')).toHaveText(['Главная', 'Мессенджер', 'Склад']);
  await expect(bar.locator('a.active')).toHaveText('Главная');
  const withBar = await metrics(page);
  // One bar, same height as without subsystems; the tab area is not shorter.
  expect(withBar.topbar.height).toBe(plain.topbar.height);
  expect(withBar.body.top).toBe(plain.body.top);
  expect(withBar.body.height).toBe(plain.body.height);
  // Tabs sit inside the bar, right of the title, before search.
  const box = await bar.boundingBox();
  const title = await page.locator('.topbar-title').boundingBox();
  const search = await page.locator('.topbar-search').boundingBox();
  expect(box.y).toBeGreaterThanOrEqual(withBar.topbar.y);
  expect(box.y + box.height).toBeLessThanOrEqual(withBar.topbar.y + withBar.topbar.height + 0.5);
  expect(box.x).toBeGreaterThanOrEqual(title.x + title.width - 0.5);
  expect(box.x + box.width).toBeLessThanOrEqual(search.x + 0.5);

  // Switching a section works as before and marks it active.
  await bar.getByText('Мессенджер', { exact: true }).click();
  await expect(page.locator('.topbar > nav.subsys-bar a.active')).toHaveText('Мессенджер');
  expect((await metrics(page)).body.height).toBe(plain.body.height);

  // Narrow window: tabs scroll inside the bar instead of adding a row.
  await page.setViewportSize({ width: 600, height: 750 });
  const narrow = await metrics(page);
  const narrowBar = await page.locator('.topbar > nav.subsys-bar').boundingBox();
  expect(narrowBar.y + narrowBar.height).toBeLessThanOrEqual(narrow.topbar.y + narrow.topbar.height + 0.5);
});
