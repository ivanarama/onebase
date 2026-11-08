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

  // A real DSL action creates the message; ordinary clicks must still reach
  // the messages header after the panel is collapsed behind the status bar.
  await bar.locator('button').first().click();
  await expect(bar.locator('.ob-sb-note')).toHaveText('action complete');
  const messages = page.locator('#ob-msg-bar');
  const messageHead = page.locator('#ob-msg-head');
  await expect(messages).toHaveClass(/\bopen\b/);
  await expect(messages.locator('#ob-msg-list')).toContainText('status bar action message');
  await messageHead.click();
  await expect(messages).not.toHaveClass(/\bopen\b/);
  await messageHead.click({ timeout: 2000 });
  await expect(messages).toHaveClass(/\bopen\b/);
  await expect(messages.locator('#ob-msg-list')).toBeVisible();
  await expect.poll(async () => {
    const panel = await messages.boundingBox();
    const status = await bar.boundingBox();
    return Math.abs(panel.y + panel.height - status.y);
  }).toBeLessThan(1);
  expect(errors).toEqual([]);
});
