// Real page navigation must release SSE even when Chromium retains documents.
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

test.use({ launchOptions: { ignoreDefaultArgs: ['--disable-back-forward-cache'] } });

test('переходы по подсистемам закрывают SSE, возврат восстанавливает живые данные', async ({ page }) => {
  await page.addInitScript(() => {
    const NativeEventSource = window.EventSource;
    window.__sseTest = { streams: [], restored: false, refreshes: 0 };
    window.EventSource = class extends NativeEventSource {
      constructor(...args) {
        super(...args);
        window.__sseTest.streams.push(this);
      }
      close() {
        sessionStorage.setItem('sse-navigation-closes', String(
          Number(sessionStorage.getItem('sse-navigation-closes') || 0) + 1));
        super.close();
      }
    };
    window.addEventListener('pageshow', event => { window.__sseTest.restored = event.persisted; });
    window.addEventListener('onebase:__oblive_refresh_all__', () => { window.__sseTest.refreshes++; });
  });
  await login(page);
  await open(page, '/ui/');
  const links = await page.locator('.subsys-bar a[href*="subsystem="]').evaluateAll(
    nodes => nodes.map(node => node.getAttribute('href')));
  expect(links.length).toBeGreaterThan(1);
  // Go past the HTTP/1.1 six-connection limit, including return from the last
  // subsystem. Direct goto alone misses retained-document navigation bugs.
  const route = [...links, ...links.slice().reverse(), ...links];
  for (const href of route) {
    await expect.poll(() => page.evaluate(() => window.__obEvents?.readyState)).toBe(1);
    const closed = await page.evaluate(() => Number(sessionStorage.getItem('sse-navigation-closes') || 0));
    const target = page.locator('.subsys-bar a');
    const index = await target.evaluateAll((nodes, url) => nodes.findIndex(node => node.getAttribute('href') === url), href);
    expect(index).toBeGreaterThanOrEqual(0);
    await Promise.all([
      page.waitForNavigation({ waitUntil: 'domcontentloaded', timeout: 10_000 }),
      target.nth(index).click({ noWaitAfter: true }),
    ]);
    await expect.poll(() => page.evaluate(() => Number(sessionStorage.getItem('sse-navigation-closes') || 0))).toBe(closed + 1);
    await expect.poll(() => page.evaluate(() => window.__sseTest.streams.filter(stream => stream.readyState !== 2).length)).toBe(1);
  }
  // A retained document emits pageshow, not a second DOMContentLoaded.
  await page.goBack({ waitUntil: 'commit' });
  await expect.poll(() => page.evaluate(() => window.__obEvents?.readyState)).toBe(1);
  await expect.poll(() => page.evaluate(() => window.__sseTest.streams.filter(stream => stream.readyState !== 2).length)).toBe(1);
  if (await page.evaluate(() => window.__sseTest.restored)) {
    await expect.poll(() => page.evaluate(() => window.__sseTest.refreshes)).toBeGreaterThan(0);
  }
  await page.reload({ waitUntil: 'domcontentloaded' });
  await expect.poll(() => page.evaluate(() => window.__obEvents?.readyState)).toBe(1);
  expect(await page.evaluate(() => window.__sseTest.streams.length)).toBe(1);
});
