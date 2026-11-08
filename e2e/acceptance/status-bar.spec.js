const { test, expect } = require('@playwright/test');

test('status bar fully hides and restores after real source polling', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/ui/', { waitUntil: 'domcontentloaded' });
  const bar = page.locator('#ob-status-bar');
  await expect(bar).toBeVisible();
  await expect(bar.locator('.ob-sb-left')).toHaveText('call active');
  await expect(bar.locator('button')).toHaveCount(2);
  await expect.poll(() => bar.evaluate(node => node.hidden)).toBe(true);
  await expect(bar).toHaveCSS('display', 'none');
  expect(await bar.evaluate(node => node.getBoundingClientRect().height)).toBe(0);
  expect(await bar.locator('button').first().evaluate(node => node.getClientRects().length)).toBe(0);
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--ob-status-bar-h').trim())).toBe('0px');
  await expect(bar).toBeVisible();
  await expect(bar.locator('.ob-sb-right')).toHaveText('operator ready');
  await expect(bar.locator('button').first()).toBeVisible();
  expect(await bar.evaluate(node => node.getBoundingClientRect().height)).toBe(30);
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--ob-status-bar-h').trim())).toBe('30px');
  expect(errors).toEqual([]);
});
