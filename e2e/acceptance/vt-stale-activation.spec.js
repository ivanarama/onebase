const { test, expect } = require('@playwright/test');

for (const queuedIndex of [0, 1]) {
  test(`queued result ${queuedIndex} is not sent after selection clears the table`, async ({ page }) => {
    const requests = [], replies = [], errors = [];
    page.on('pageerror', error => errors.push(String(error)));
    page.on('request', request => {
      if (request.url().includes('/form-event')) requests.push(new URLSearchParams(request.postData()));
    });
    page.on('response', async response => {
      if (response.url().includes('/form-event')) replies.push(await response.json());
    });
    let reachedFirst;
    const firstReached = new Promise(resolve => { reachedFirst = resolve; });
    let releaseFirst;
    const firstRelease = new Promise(resolve => { releaseFirst = resolve; });
    let holdFirst = true;
    await page.route('**/form-event', async route => {
      const body = new URLSearchParams(route.request().postData());
      if (body.get('_event') === 'ПриАктивизацииСтроки' && holdFirst) {
        holdFirst = false;
        // The handler really ran and cleared the rows. Delay only delivery
        // to the browser so the second user click enters the real queue.
        const response = await route.fetch();
        reachedFirst();
        await firstRelease;
        await route.fulfill({ response });
      } else await route.continue();
    });
    await page.goto('/ui/catalog/Контрагент/new');
    const body = page.locator('tbody[data-ob-vt-activate="ResultsTable"]');
    const rows = body.locator('tr[data-ob-vt-row]');
    await expect(rows).toHaveCount(2);
    await page.evaluate(() => {
      const fire = window.obFire;
      window.activationPromises = [];
      window.obFire = (...args) => {
        const pending = fire(...args);
        window.activationPromises.push(pending);
        return pending;
      };
    });
    await rows.nth(0).locator('input[name$=".Name"]').click();
    await firstReached;
    await rows.nth(queuedIndex).locator('input[name$=".Name"]').click();
    releaseFirst();
    await page.evaluate(() => Promise.all(window.activationPromises));
    await expect(rows).toHaveCount(0);
    await expect.poll(() => replies.length).toBe(requests.length);
    expect(replies.every(reply => reply.ok), JSON.stringify(replies)).toBe(true);
    expect(requests.filter(request => request.get('_event') === 'ПриАктивизацииСтроки')).toHaveLength(1);
    expect(errors).toEqual([]);
    await page.locator('[data-ob-fire-click="Reload"]').click();
    await expect(rows).toHaveCount(2);
    await rows.nth(0).locator('input[name$=".Name"]').click();
    await page.evaluate(() => Promise.all(window.activationPromises));
    await expect(rows).toHaveCount(0);
    expect(requests.filter(request => request.get('_event') === 'ПриАктивизацииСтроки')).toHaveLength(2);
    expect(replies.every(reply => reply.ok), JSON.stringify(replies)).toBe(true);
    expect(errors).toEqual([]);
  });
}
