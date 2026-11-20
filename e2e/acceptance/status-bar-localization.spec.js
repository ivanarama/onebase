const { test, expect } = require('@playwright/test');

test('status bar errors follow the English page language', async ({ page }) => {
  const base = process.env.OB_ACCEPTANCE_STATUS_BAR_EN_URL;
  if (!base) throw new Error('English status-bar fixture is required');
  await page.goto(base + '/ui/', { waitUntil: 'domcontentloaded' });
  const bar = page.locator('#ob-status-bar');
  const button = bar.locator('button');
  await expect(button).toHaveCount(1);
  expect(await page.evaluate(() => window.obUIMessage('closeNotConfirmed', '')))
    .toBe('The form was not closed because the server did not confirm it.');

  // The server refuses a configured button hidden by the real source.
  const rejected = await page.request.post(base + '/ui/_status-bar', { form: { button: 'НаЛинии' } });
  expect(rejected.status()).toBe(409);
  expect((await rejected.text()).trim()).toBe('The button is currently unavailable');

  // Deliver that real rejection to a normal click, then lose the connection.
  await page.route('**/ui/_status-bar', route => route.request().method() === 'POST'
    ? route.fulfill({ response: rejected }) : route.continue());
  await button.click();
  await expect(bar.locator('.ob-sb-note')).toHaveText('The button is currently unavailable');
  await expect(button).toBeEnabled();
  await page.unroute('**/ui/_status-bar');
  await page.route('**/ui/_status-bar', route => route.request().method() === 'POST'
    ? route.abort('connectionfailed') : route.continue());
  await button.click();
  await expect(bar.locator('.ob-sb-note')).toHaveText('No connection to the server');
  await expect(button).toBeEnabled();
});
