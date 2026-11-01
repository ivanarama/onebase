const { test, expect } = require('@playwright/test');

const key = 'w.' + Buffer.from('ЗадачиФильтр').toString('base64url') + '.' + Buffer.from('Тема').toString('base64url');
const filter = page => page.locator('[data-ob-filter="' + key + '"]');
const rows = page => page.locator('[data-ob-widget-card] tbody tr');

// No synthetic history entries or popstate: the real widget pushes both states.
test('shell preserves widget Back/Forward and guards only its entry boundary', async ({ page }) => {
  const errors = [], dialogs = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('dialog', d => { dialogs.push(d.type()); d.dismiss().catch(() => {}); });
  const login = await page.request.post('/auth/login', { data: { login: 'admin', password: 'secret123' } });
  expect(login.ok()).toBe(true);
  await page.goto('/ui/app?home=1', { waitUntil: 'domcontentloaded' });
  await expect(filter(page)).toBeVisible();
  await page.evaluate(() => { window.historyAcceptanceDocument = 'same shell'; });

  async function expectFilter(value) {
    await expect(filter(page)).toHaveValue(value);
    await expect.poll(() => new URL(page.url()).searchParams.get(key) || '').toBe(value);
    await expect(rows(page)).toHaveCount(value ? 1 : 2);
    if (value) await expect(rows(page)).toContainText(value);
    expect(await page.evaluate(() => window.historyAcceptanceDocument)).toBe('same shell');
  }
  for (const value of ['A', 'B']) {
    await filter(page).fill(value);
    await filter(page).press('Enter');
    await expectFilter(value);
  }
  await page.evaluate(() => history.back());
  await expectFilter('A');
  await page.evaluate(() => history.forward());
  await expectFilter('B');
  await page.evaluate(() => history.back());
  await expectFilter('A');
  await page.evaluate(() => history.back());
  await expectFilter('');

  // Reaching the entry boundary must retain all forward entries, too.
  await page.evaluate(() => window.addEventListener('popstate', () => window.historyAcceptancePops++));
  for (let i = 0; i < 2; i++) {
    await page.evaluate(() => { window.historyAcceptancePops = 0; history.back(); });
    await expect.poll(() => page.evaluate(() => window.historyAcceptancePops)).toBe(2);
    await expectFilter('');
  }
  await page.evaluate(() => history.forward());
  await expectFilter('A');
  await page.evaluate(() => history.forward());
  await expectFilter('B');
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.evaluate(() => { window.historyAcceptanceDocument = 'same shell'; });
  await expectFilter('B');
  await page.evaluate(() => history.back());
  await expectFilter('A');
  await page.evaluate(() => history.forward());
  await expectFilter('B');
  expect(dialogs).toEqual([]);
  expect(errors).toEqual([]);
});
