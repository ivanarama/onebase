const { test, expect } = require('@playwright/test');

// readonly_when колонки табличной части в сетке: условие по шапке запирает
// столбец (двойной щелчок не открывает редактор) и снимается событием формы
// без перезагрузки; соседняя колонка без условия остаётся редактируемой.
test('table part column readonly_when locks and unlocks the grid column', async ({ page }) => {
  expect((await page.request.post('/auth/login', { data: { login: 'admin', password: 'secret123' } })).ok()).toBe(true);
  await page.goto('/ui/document/Orders/' + process.env.OB_ACCEPTANCE_ORDER, { waitUntil: 'domcontentloaded' });
  const qty = page.locator('.slick-row').first().locator('.slick-cell').nth(1);
  const product = page.locator('.slick-row').first().locator('.slick-cell').nth(0);
  await expect(qty).toHaveText('5');
  const editor = page.locator('.slick-cell.active input, .slick-cell.editable input');

  // Rendered closed: Qty locked, Product editable.
  await expect(qty).toHaveClass(/ob-col-readonly/);
  await qty.dblclick();
  await expect(editor).toHaveCount(0);
  await product.dblclick();
  await expect(editor).toHaveCount(1);
  await page.keyboard.press('Escape');
  await expect(editor).toHaveCount(0);

  // Open the order: the form event unlocks the column without a reload.
  const status = page.locator('input[name="Status"]');
  await status.fill('Open');
  await status.press('Tab');
  await expect(qty).not.toHaveClass(/ob-col-readonly/);
  await qty.dblclick();
  await expect(editor).toHaveCount(1);
  await page.keyboard.press('Escape');

  // Close again: locked again.
  await status.fill('Closed');
  await status.press('Tab');
  await expect(qty).toHaveClass(/ob-col-readonly/);
  await qty.dblclick();
  await expect(editor).toHaveCount(0);
});
