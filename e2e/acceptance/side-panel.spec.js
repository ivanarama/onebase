const { test, expect } = require('@playwright/test');

// Правая панель оболочки (home_page.yaml side_panel): справа, рабочая
// область вкладок не заходит под неё; щелчок по строке открывает объект во
// вкладке; ширину меняют перетаскиванием края, и она сохраняется.
test('side panel sits right of the tabs, opens rows as tabs and keeps its width', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 750 });
  expect((await page.request.post('/auth/login', { data: { login: 'admin', password: 'secret123' } })).ok()).toBe(true);
  await page.goto('/ui/app', { waitUntil: 'domcontentloaded' });
  const panel = page.locator('#ob-side-panel');
  await expect(panel).toBeVisible();
  await expect(panel).toContainText('Проверить жалобу');
  let box = await panel.boundingBox();
  expect(Math.round(box.width)).toBe(300);
  expect(Math.round(box.x + box.width)).toBe(1440);
  const body = await page.locator('#ob-tabbody').boundingBox();
  expect(body.x + body.width).toBeLessThanOrEqual(box.x + 1);

  // Строка виджета панели открывает объект вкладкой оболочки.
  await panel.locator('[data-ob-row-url]').first().click();
  await expect(page.locator('.ob-tab')).toHaveCount(1);
  await expect(page.locator('#ob-tabbody iframe')).toHaveAttribute('src', /\/ui\/document\/Tasks\//);
  await expect(panel).toBeVisible();

  // Ширина: тянем край влево на 100px, после перезагрузки — та же.
  const grip = panel.locator('[data-ob-side-resize]');
  const g = await grip.boundingBox();
  await page.mouse.move(g.x + 2, g.y + 50);
  await page.mouse.down();
  await page.mouse.move(g.x - 98, g.y + 50, { steps: 5 });
  await page.mouse.up();
  box = await panel.boundingBox();
  expect(Math.round(box.width)).toBe(400);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await expect(panel).toBeVisible();
  expect(Math.round((await panel.boundingBox()).width)).toBe(400);
});
