// Real list GETs and DOM replacement, including the footer and feed lifecycle.
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

test.use({ serviceWorkers: 'block' });

const entity = 'Номенклатура';
const liveKey = 'catalog/номенклатура';
const liveSelector = `[data-ob-live="${liveKey}"]`;
const apiPath = `/catalogs/${encodeURIComponent(entity)}`;

async function fixture(page) {
  // The trade catalog is static. Opt this test's real HTML into the same
  // declaration that list_refresh_on renders, without changing the demo config.
  await page.route('**/ui/catalog/**', async route => {
    if (route.request().method() !== 'GET') return route.continue();
    const response = await route.fetch();
    const body = (await response.text()).replace(
      `data-ob-live="${liveKey}"`,
      `data-ob-live="${liveKey}" data-ob-refresh-on="данные.номенклатура"`
    );
    await route.fulfill({ response, body });
  });
  // Keep loading under the user's control so the first page is observable.
  await page.addInitScript(() => { delete window.IntersectionObserver; });
  await login(page);
  const prefix = `Live-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const ids = [];
  const headers = { Origin: new URL(page.url()).origin };
  async function add(count) {
    const added = [];
    for (let i = 0; i < count; i++) {
      const response = await page.request.post(apiPath, {
        headers, data: { Наименование: `${prefix}-${ids.length}`, Активный: true }
      });
      expect(response.status(), await response.text()).toBe(201);
      const id = (await response.json()).id;
      ids.push(id);
      added.push(id);
    }
    return added;
  }
  async function remove(items) {
    for (const id of items) {
      const response = await page.request.delete(`${apiPath}/${id}`, { headers });
      expect(response.status(), await response.text()).toBe(204);
      ids.splice(ids.indexOf(id), 1);
    }
  }
  function url(view, mode, pageNumber = 1) {
    return `/ui/catalog/${encodeURIComponent(entity)}?` + new URLSearchParams({
      q: prefix, limit: '2', view, lm: mode, page: String(pageNumber),
      sort: 'Наименование', dir: 'asc', activity: 'all'
    });
  }
  return { add, remove, ids, url, prefix };
}

async function refresh(page, reconnect = false) {
  await expect(page.locator(liveSelector)).toHaveAttribute('data-ob-refresh-on', 'данные.номенклатура');
  const response = page.waitForResponse(r => r.request().headers()['x-requested-with'] === 'obLiveList');
  await page.evaluate(reconnect => window.dispatchEvent(new CustomEvent(
    reconnect ? 'onebase:__oblive_refresh_all__' : 'onebase:данные.номенклатура'
  )), reconnect);
  await response;
}

for (const view of ['list', 'tiles']) {
  test(`${view}: live totals and links follow growth, last-page deletion and empty results`, async ({ page }) => {
    const f = await fixture(page);
    try {
      const initial = await f.add(4);
      await open(page, f.url(view, 'pages', 2));
      const live = page.locator(liveSelector);
      await expect(live.locator('[data-ob-list-row]')).toHaveCount(2);
      await expect(live).toContainText('Стр. 2 из 2 (4 записей)');
      await page.evaluate(() => { window.__oldDetail = document.getElementById('ob-detail'); });

      await f.remove(initial.slice(2));
      await refresh(page, true);
      await expect(live).toContainText('Всего: 2');
      await expect(live.locator('[data-ob-list-row]')).toHaveCount(2);
      await expect(live.locator('a').filter({ hasText: 'Назад' })).toHaveCount(0);
      await expect(live.locator('a').filter({ hasText: 'Вперёд' })).toHaveCount(0);
      expect(await page.evaluate(() => window.__oldDetail === document.getElementById('ob-detail'))).toBe(true);
      await expect(page.locator('[data-ob-list-search]')).toHaveValue(f.prefix);

      await f.add(3);
      await refresh(page);
      await expect(live).toContainText('Стр. 2 из 3 (5 записей)');
      await expect(live.locator('[data-ob-list-row]')).toHaveCount(2);
      const next = live.locator('a').filter({ hasText: 'Вперёд' });
      const target = new URL(await next.getAttribute('href'), page.url());
      expect(target.searchParams.get('page')).toBe('3');
      expect(target.searchParams.get('q')).toBe(f.prefix);
      expect(target.searchParams.get('view')).toBe(view);
      expect(target.searchParams.get('sort')).toBe('Наименование');

      await f.remove([...f.ids]);
      await refresh(page);
      await expect(live.locator('[data-ob-list-row]')).toHaveCount(0);
      await expect(live).toContainText('Ничего не найдено');
      await expect(live).not.toContainText('Всего:');
      await expect(live).not.toContainText('Стр.');
    } finally {
      await f.remove([...f.ids]).catch(() => {});
    }
  });

  test(`${view}: refreshed feed rebinds loading and keeps selection and focus`, async ({ page }) => {
    const f = await fixture(page);
    try {
      await f.add(2);
      await open(page, f.url(view, 'feed'));
      const live = page.locator(liveSelector);
      await expect(live.locator('#feed-loaded')).toHaveText('2');
      const first = live.locator('[data-ob-list-row]').first();
      await first.click();
      const selectedID = await first.getAttribute('data-ob-entity-id');
      await f.add(3);
      await refresh(page);
      await expect(live.locator('#feed-loaded')).toHaveText('2');
      await expect(live).toContainText('из 5');
      const selected = live.locator(`[data-ob-entity-id="${selectedID}"]`);
      await expect(selected).toHaveAttribute('aria-selected', 'true');
      await expect(selected).toBeFocused();
      await live.locator('#feed-more a').click();
      await expect(live.locator('[data-ob-list-row]')).toHaveCount(5);
      await expect(live.locator('#feed-loaded')).toHaveText('5');
      await expect(live.locator('#feed-more')).toHaveCount(0);

      await f.remove(f.ids.slice(1));
      await refresh(page, true);
      await expect(live.locator('[data-ob-list-row]')).toHaveCount(1);
      await expect(live.locator('#feed-loaded')).toHaveText('1');
      await expect(live).toContainText('из 1');
      await expect(live.locator('#feed-more')).toHaveCount(0);
      await f.remove([...f.ids]);
      await refresh(page);
      await expect(live.locator('[data-ob-list-row]')).toHaveCount(0);
      await expect(live.locator('#feed-loaded')).toHaveCount(0);
    } finally {
      await f.remove([...f.ids]).catch(() => {});
    }
  });
}

test('late response from an old feed cannot change a refreshed feed', async ({ page }) => {
  const f = await fixture(page);
  let release;
  try {
    await f.add(5);
    await open(page, f.url('list', 'feed'));
    let held;
    const captured = new Promise(resolve => { held = resolve; });
    const resume = new Promise(resolve => { release = resolve; });
    let delayed = false;
    await page.route('**/ui/catalog/**', async route => {
      const request = route.request();
      const url = new URL(request.url());
      if (!delayed && url.searchParams.get('page') === '2' && !request.headers()['x-requested-with']) {
        delayed = true;
        const response = await route.fetch();
        held();
        await resume;
        await route.fulfill({ response });
      } else await route.fallback();
    });
    await page.locator('#feed-more a').click();
    await captured;
    await f.add(1);
    await refresh(page, true);
    await expect(page.locator('#feed-loaded')).toHaveText('2');
    // Wait for the complete response body before checking the refreshed DOM.
    const completed = page.waitForResponse(r => r.request().url().includes('page=2') && !r.request().headers()['x-requested-with']);
    release();
    await (await completed).finished();
    await page.evaluate(async () => { await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
    await expect(page.locator(`${liveSelector} [data-ob-list-row]`)).toHaveCount(2);
    await expect(page.locator('#feed-loaded')).toHaveText('2');
    await page.locator('#feed-more a').click();
    await expect(page.locator(`${liveSelector} [data-ob-list-row]`)).toHaveCount(6);
    await expect(page.locator('#feed-loaded')).toHaveText('6');
  } finally {
    if (release) release();
    await f.remove([...f.ids]).catch(() => {});
  }
});
