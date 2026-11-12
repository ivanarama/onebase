const { test, expect } = require('@playwright/test');

// ПриАктивизацииСтроки после замены строк сервером: «Найти» дважды подряд,
// результат каждый раз в строке 0 — щелчок по ней обязан выбрать строку и во
// второй раз (раньше actLastRow не сбрасывался и щелчок молча игнорировался).
// Возврат курсора после перерисовки сам строку не выбирает.
test('row activation fires for row 0 after the rows are replaced by a second search', async ({ page }) => {
  expect((await page.request.post('/auth/login', { data: { login: 'admin', password: 'secret123' } })).ok()).toBe(true);
  await page.goto('/ui/document/Orders/new', { waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => typeof window.obFire === 'function' && document.readyState === 'complete');
  const selected = page.locator('input[name="Selected"]');
  const row0 = page.locator('.slick-row').first().locator('.slick-cell').first();

  await page.getByRole('button', { name: 'Find', exact: true }).click();
  await expect(row0).toHaveText('R1');
  await row0.click();
  await expect(selected).toHaveValue('R1');

  await page.getByRole('button', { name: 'Find', exact: true }).click();
  await expect(row0).toHaveText('R2');
  // Перерисовка вернула курсор на строку 0, но выбор очищен «Найти» и сам не
  // восстановился: строку выбирает пользователь.
  await page.waitForTimeout(500);
  await expect(selected).toHaveValue('');
  await row0.click();
  await expect(selected).toHaveValue('R2');
});
