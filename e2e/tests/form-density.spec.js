const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

// Load real YAML forms through the HTTP handler: CSS text alone cannot prove
// that compact rules override the group/page inline defaults (#2277).
for (const compact of [false, true]) {
  test(`form group spacing: ${compact ? 'compact' : 'default'}`, async ({ page }) => {
    await login(page);
    await open(page, `/ui/document/${compact ? 'ПлотнаяФорма' : 'ОбычнаяФорма'}/new`);
    expect(await page.locator('html').evaluate((el) => el.classList.contains('ob-density-compact'))).toBe(compact);
    for (const name of ['Group', 'Nested', 'Page']) {
      const container = page.locator(`[data-ob-el="${name}"]`);
      await expect(container).toBeVisible();
      await expect(container).toHaveCSS('padding-top', compact ? '6px' : '12px');
      await expect(container).toHaveCSS('padding-right', compact ? '10px' : '14px');
      await expect(container).toHaveCSS('padding-bottom', compact ? '8px' : '12px');
      await expect(container).toHaveCSS('padding-left', compact ? '10px' : '14px');
      await expect(container).toHaveCSS('margin-bottom', compact ? '8px' : '14px');
    }
  });
}

// density: dense — оба класса на <html>, отступы групп меньше compact, поле
// 25px; кнопка «…» ссылки — вровень с полем, флажок — по центру поля.
test('form group spacing: dense', async ({ page }) => {
  await login(page);
  await open(page, '/ui/document/ОченьПлотнаяФорма/new');
  const classes = await page.locator('html').evaluate((el) => [...el.classList]);
  expect(classes).toEqual(expect.arrayContaining(['ob-density-compact', 'ob-density-dense']));
  for (const name of ['Group', 'Nested', 'Page']) {
    const container = page.locator(`[data-ob-el="${name}"]`);
    await expect(container).toBeVisible();
    await expect(container).toHaveCSS('padding-top', '4px');
    await expect(container).toHaveCSS('padding-right', '8px');
    await expect(container).toHaveCSS('padding-bottom', '5px');
    await expect(container).toHaveCSS('padding-left', '8px');
    await expect(container).toHaveCSS('margin-bottom', '5px');
  }
  const input = await page.locator('[data-ob-el="RowText"] input').boundingBox();
  expect(input.height).toBeCloseTo(25, 0);
  const ref = await page.locator('[data-ob-el="RowRef"] .managed-control-row>button').first().boundingBox();
  expect(ref.height).toBeCloseTo(25, 0);
  expect(Math.abs(ref.y - input.y)).toBeLessThanOrEqual(1);
  const flag = await page.locator('[data-ob-el="RowFlag"]').boundingBox();
  // Флажок в той же строке, что и поле, и по центру его высоты.
  expect(Math.abs((flag.y + flag.height / 2) - (input.y + input.height / 2))).toBeLessThanOrEqual(1.5);
});
