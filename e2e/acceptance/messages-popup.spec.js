const { test, expect } = require('@playwright/test');
const toast = page => page.locator('#ob-fmevt-banner > div');
const visibleCount = async page => (await Promise.all(page.frames().map(frame => toast(frame).filter({ hasText: 'same-message' }).count()))).reduce((a, b) => a + b, 0);
const shownCount = async page => (await Promise.all(page.frames().map(frame => frame.evaluate(() => window.popupShows || 0)))).reduce((a, b) => a + b, 0);
let errors;
test.beforeEach(async ({ page }) => {
  errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.request.post('/ui/messages/clear');
});
test.afterEach(() => expect(errors).toEqual([]));

for (const topology of ['standalone', 'iframe']) {
for (const order of ['event-first', 'poll-first']) {
  test(`one notification for one real form event: ${topology}, ${order}`, async ({ page }) => {
    let form = page;
    if (topology === 'iframe') {
      await page.goto('/ui/app');
      await page.evaluate(() => window.obOpenTab('/ui/catalog/PopupProbe/new', 'Popup'));
      const handle = await page.locator('#ob-tabbody iframe').last().elementHandle();
      form = await handle.contentFrame();
      await expect(form.locator('[data-ob-el="Notify"]')).toBeVisible();
    } else {
      await page.goto('/ui/catalog/PopupProbe/new');
    }
    await expect.poll(() => page.evaluate(() => typeof window.obReloadMessages)).toBe('function');
    for (const frame of page.frames()) await frame.evaluate(() => {
      window.popupShows = 0;
      new MutationObserver(changes => {
        for (const change of changes) {
          for (const node of change.addedNodes) {
            if (node.parentElement?.id === 'ob-fmevt-banner') window.popupShows++;
          }
        }
      }).observe(document.body, { childList: true, subtree: true });
    });
    let release, received;
    const barrier = new Promise(resolve => { release = resolve; });
    // Hold just one delivery channel, preserving the real server response.
    await page.route(order === 'poll-first' ? '**/form-event' : '**/ui/messages', async route => {
      const response = await route.fetch();
      received = await response.json();
      await barrier;
      await route.fulfill({ response });
    });
    await form.locator('[data-ob-el="Notify"]').click();
    if (order === 'poll-first') {
      await expect.poll(() => received?.messages?.length).toBe(1);
      await page.evaluate(() => window.obReloadMessages());
    }
    await expect.poll(() => visibleCount(page)).toBe(1);
    release();
    await page.unrouteAll({ behavior: 'wait' });
    // Observe total deliveries as well as currently visible toasts.
    await expect.poll(() => form.locator('[data-ob-el="Notify"]').isEnabled()).toBe(true);
    await page.evaluate(() => window.obReloadMessages());
    await page.waitForTimeout(100);
    const messages = await (await page.request.get('/ui/messages')).json();
    expect(messages.messages).toHaveLength(1);
    expect(messages.messages[0].id).toMatch(/^[0-9a-f-]{36}$/);
    expect(await shownCount(page)).toBe(1);
    // Distinct messages with identical text must each be delivered once.
    await form.locator('[data-ob-el="Notify"]').click();
    await expect.poll(async () => (await (await page.request.get('/ui/messages')).json()).messages.length).toBe(2);
    await expect.poll(() => shownCount(page)).toBe(2);
    await expect.poll(() => visibleCount(page)).toBe(2);
  });
}

}

for (const skew of [120000, -120000]) {
  test(`page redirect messages use server time with browser skew ${skew}`, async ({ page }) => {
    await page.addInitScript(delta => {
      const now = Date.now.bind(Date);
      Date.now = () => now() + delta;
    }, skew);
    await page.goto('/ui/page/PopupPage');
    await page.getByRole('button', { name: 'Notify', exact: true }).click();
    await expect(toast(page).filter({ hasText: 'page-message' })).toHaveCount(1);
    await page.reload();
    await page.waitForTimeout(100);
    await expect(toast(page).filter({ hasText: 'page-message' })).toHaveCount(0);
  });
}

test('page action error remains an error after redirect until closed', async ({ page }) => {
  await page.goto('/ui/page/PopupPage');
  await page.getByRole('button', { name: 'Error', exact: true }).click();
  const error = toast(page).filter({ hasText: 'page-error' });
  await expect(error).toHaveCount(1);
  await expect(error).toHaveCSS('background-color', 'rgb(254, 226, 226)');
  await page.waitForTimeout(10500);
  await expect(error).toHaveCount(1);
  await error.getByText('×', { exact: true }).click();
  await expect(error).toHaveCount(0);
});


test('event delivered after switching tabs remains visible in the shell exactly once', async ({ page }) => {
  await page.goto('/ui/app');
  await page.evaluate(() => window.obOpenTab('/ui/catalog/PopupProbe/new', 'Popup'));
  const handle = await page.locator('#ob-tabbody iframe').last().elementHandle();
  const form = await handle.contentFrame();
  await expect(form.locator('[data-ob-el="Notify"]')).toBeVisible();
  let releaseEvent, releasePoll, received;
  const eventBarrier = new Promise(resolve => { releaseEvent = resolve; });
  const pollBarrier = new Promise(resolve => { releasePoll = resolve; });
  await page.route('**/ui/messages', async route => {
    await pollBarrier;
    await route.continue();
  });
  await page.route('**/form-event', async route => {
    const response = await route.fetch();
    received = await response.json();
    await eventBarrier;
    await route.fulfill({ response });
  });
  try {
    await form.locator('[data-ob-el="Notify"]').click();
    await expect.poll(() => received?.messages?.length).toBe(1);
    await page.evaluate(() => window.obOpenTab('/ui/page/PopupPage', 'Other'));
    await expect.poll(() => handle.evaluate(el => getComputedStyle(el).display)).toBe('none');
    releaseEvent();
    await expect(toast(page).filter({ hasText: 'same-message' })).toBeVisible();
    await expect(toast(form).filter({ hasText: 'same-message' })).toHaveCount(0);
    releasePoll();
    await page.unrouteAll({ behavior: 'wait' });
    await page.evaluate(() => window.obReloadMessages());
    const stored = await (await page.request.get('/ui/messages')).json();
    expect(stored.messages).toHaveLength(1);
    expect(stored.messages[0].id).toBe(received.messageIDs[0]);
    await expect(toast(page).filter({ hasText: 'same-message' })).toHaveCount(1);
    await expect(toast(form).filter({ hasText: 'same-message' })).toHaveCount(0);
  } finally {
    releaseEvent();
    releasePoll();
    await page.unrouteAll({ behavior: 'wait' });
  }
});
