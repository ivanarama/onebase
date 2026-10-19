const { test, expect } = require('@playwright/test');
const ids = JSON.parse(process.env.OB_CHOICE_IDS || '{}');
const direction = (page) => page.locator('select[name="Направление"]');
const target = (page) => page.locator('select[name="Неисправность"]');
const formURL = (id) => `/ui/document/Заявка/${id || 'new'}`;
const requests = '**/ui/_ref-options/**';

let pageErrors = [];
test.beforeEach(async ({ page }) => {
  pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
});
test.afterEach(() => expect(pageErrors).toEqual([]));

async function open(page, id) {
  await page.goto(formURL(id), { waitUntil: 'domcontentloaded' });
  await expect(target(page)).toHaveAttribute('data-ref-choice-context', /fault-picker/);
}
async function settled(page) {
  await expect(target(page)).not.toHaveAttribute('data-ob-choice-loading', '1');
  await expect(target(page)).not.toHaveAttribute('data-ob-choice-error', '1');
}
function query(source, selected, extra = {}) {
  return new URLSearchParams({
    form_entity: 'Заявка', form: 'ФормаОбъекта', element: 'fault-picker',
    sources: JSON.stringify({ 'Объект.Направление': source }),
    ...(selected ? { selected_id: selected } : {}), ...extra,
  });
}
async function options(page, params) {
  const response = await page.request.get(`/ui/_ref-options/Неисправность?${params}`);
  expect(response.status()).toBe(200);
  return response.json();
}

// Fetch the real server result first, then hold delivery behind an explicit
// barrier. A→B→A is synchronized by observed requests, never elapsed time.
async function holdResponses(page) {
  const held = [];
  await page.route(requests, async (route) => {
    const url = new URL(route.request().url());
    if (url.searchParams.get('element') !== 'fault-picker' || !JSON.parse(url.searchParams.get('sources') || '{}')['Объект.Направление']) return route.continue();
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    const data = await response.json();
    let release;
    const barrier = new Promise((resolve) => { release = resolve; });
    const entry = { source: JSON.parse(url.searchParams.get('sources'))['Объект.Направление'], data, release };
    held.push(entry);
    await barrier;
    try {
      await route.fulfill({ response });
    } catch (error) {
      // Cancellation is also an expected terminal state with AbortController.
      if (!route.request().failure()) throw error;
    }
    entry.delivered = true;
  });
  return held;
}

for (const abortAvailable of [true, false]) {
  test(`runtime A→B→A rejects both stale responses (AbortController=${abortAvailable})`, async ({ page }) => {
    await page.addInitScript((useAbort) => {
      if (!useAbort) window.AbortController = undefined;
      window.__choiceBodies = 0;
      const fetch = window.fetch;
      window.fetch = async (...args) => {
        const response = await fetch(...args);
        if (String(args[0]).includes('element=fault-picker')) {
          const json = response.json.bind(response);
          response.json = async () => { const data = await json(); window.__choiceBodies++; return data; };
        }
        return response;
      };
    }, abortAvailable);
    await open(page);
    const held = await holdResponses(page);
    try {
      await direction(page).selectOption(ids.a);
      await expect.poll(() => held.length).toBe(1);
      await direction(page).selectOption(ids.b);
      await expect.poll(() => held.length).toBe(2);
      await direction(page).selectOption(ids.a);
      await expect.poll(() => held.length).toBe(3);
      expect(held.map((entry) => entry.source)).toEqual([ids.a, ids.b, ids.a]);
      expect(held[1].data.items.map((item) => item.id)).toContain(ids.legacy);
      expect(held[2].data.total).toBe(51);
      expect(held[2].data.items.map((item) => item.id)).not.toContain(ids.hidden);
      expect(held[2].data.items.map((item) => item.id)).not.toContain(ids.folder);
      held[2].release();
      await expect(target(page).locator('option')).toContainText(['needle 001']);
      await settled(page);
      const selected = await target(page).locator('option').filter({ hasText: 'needle 001' }).getAttribute('value');
      await target(page).selectOption(selected);
      const before = await target(page).locator('option').allTextContents();
      // Reverse order: obsolete B would clear the new A choice; oldest A was
      // requested without that choice and shares its source fingerprint.
      held[1].release();
      held[0].release();
      await expect.poll(() => held.every((entry) => entry.delivered)).toBe(true);
      if (!abortAvailable) await page.waitForFunction(() => window.__choiceBodies === 3);
      await expect(target(page)).toHaveValue(selected);
      expect(await target(page).locator('option').allTextContents()).toEqual(before);
      await expect(target(page).locator(`option[value="${ids.legacy}"]`)).toHaveCount(0);
      await expect(direction(page)).toHaveValue(ids.a);
    } finally {
      held.forEach((entry) => entry.release());
    }
  });
}

test('runtime open picker keeps current A options through A→B→A delivery without abort', async ({ page }) => {
  await page.addInitScript(() => {
    window.AbortController = undefined;
    window.__choiceBodies = 0;
    const fetch = window.fetch;
    window.fetch = async (...args) => {
      const response = await fetch(...args);
      const url = new URL(String(args[0]), location.origin);
      if (url.searchParams.get('element') === 'fault-picker' && JSON.parse(url.searchParams.get('sources') || '{}')['Объект.Направление']) {
        const json = response.json.bind(response);
        response.json = async () => { const data = await json(); window.__choiceBodies++; return data; };
      }
      return response;
    };
  });
  await open(page);
  const initial = page.waitForResponse((response) => response.url().includes('element=fault-picker'));
  await target(page).locator('..').locator('[data-ob-ref-picker="closest"]').click();
  await (await initial).finished();
  await expect(page.locator('#_rp-status')).not.toHaveText('Загрузка...');
  const held = await holdResponses(page);
  try {
    await direction(page).selectOption(ids.a);
    await expect.poll(() => held.length).toBe(2); // select + open picker
    await direction(page).selectOption(ids.b);
    await expect.poll(() => held.length).toBe(4);
    await direction(page).selectOption(ids.a);
    await expect.poll(() => held.length).toBe(6);
    expect(held.map((entry) => entry.source)).toEqual([ids.a, ids.a, ids.b, ids.b, ids.a, ids.a]);
    held.slice(4).forEach((entry) => entry.release());
    await settled(page);
    await expect(page.locator('#_rp-list ._rp-item')).toHaveCount(50);
    const selected = await target(page).locator('option').filter({ hasText: 'needle 001' }).getAttribute('value');
    await target(page).selectOption(selected);
    const before = await page.locator('#_rp-list ._rp-item').allTextContents();
    held.slice(2, 4).forEach((entry) => entry.release());
    await page.waitForFunction(() => window.__choiceBodies === 4);
    await expect(target(page)).toHaveValue(selected);
    expect(await page.locator('#_rp-list ._rp-item').allTextContents()).toEqual(before);
    held.slice(0, 2).forEach((entry) => entry.release());
    await page.waitForFunction(() => window.__choiceBodies === 6);
    await expect(target(page)).toHaveValue(selected);
    expect(await page.locator('#_rp-list ._rp-item').allTextContents()).toEqual(before);
    await expect(page.locator('#_rp-list')).not.toContainText(/legacy|other B|hidden|folder/);
  } finally { held.forEach((entry) => entry.release()); }
});

test('runtime legacy choice survives open/save and clears only after an authoritative source change', async ({ page }) => {
  await open(page, ids.owner);
  await expect(target(page)).toHaveValue(ids.legacy);
  await expect(target(page).locator(`option[value="${ids.legacy}"]`)).toHaveAttribute('data-ob-choice-outside-filter', '1');
  // Use the actual form submit and persisted reload, not a direct DB assertion.
  const save = page.locator('button[name="_action"][value=""]').first();
  const [saved] = await Promise.all([page.waitForResponse((r) => r.request().method() === 'POST' && decodeURI(new URL(r.url()).pathname).includes('/Заявка/')), save.click()]);
  expect(saved.status()).toBeLessThan(400);
  await open(page, ids.owner);
  await expect(target(page)).toHaveValue(ids.legacy);
  const held = await holdResponses(page);
  try {
    await direction(page).selectOption(ids.b);
    await expect.poll(() => held.length).toBe(1);
    await expect(target(page)).toHaveValue(ids.legacy);
    expect(held[0].data.selected_allowed).toBe(true);
    held[0].release();
    await settled(page);
    await expect(target(page)).toHaveValue(ids.legacy);
    await target(page).evaluate((select) => {
      window.__choiceChanges = 0;
      select.addEventListener('change', () => window.__choiceChanges++);
    });
    await direction(page).selectOption(ids.a);
    await expect.poll(() => held.length).toBe(2);
    expect(held[1].data.selected_allowed).toBe(false);
    await expect(target(page)).toHaveValue(ids.legacy);
    held[1].release();
    await expect(target(page)).toHaveValue('');
    await expect.poll(() => page.evaluate(() => window.__choiceChanges)).toBe(1);
  } finally { held.forEach((entry) => entry.release()); }
});

test('runtime selected_allowed preserves a real selection beyond page 50 and search', async ({ page }) => {
  await open(page, ids.pageTwoOwner);
  await expect(target(page)).toHaveValue(ids.selected);
  const data = await options(page, query(ids.a, ids.selected, { limit: '50', q: 'element' }));
  expect(data.selected_allowed).toBe(true);
  expect(data.items.map((item) => item.id)).not.toContain(ids.selected);
  const held = await holdResponses(page);
  try {
    await direction(page).selectOption(ids.b);
    await expect.poll(() => held.length).toBe(1);
    await direction(page).selectOption(ids.a);
    await expect.poll(() => held.length).toBe(2);
    expect(held[1].data.selected_allowed).toBe(true);
    expect(held[1].data.items.map((item) => item.id)).not.toContain(ids.selected);
    held[1].release();
    await settled(page);
    await expect(target(page)).toHaveValue(ids.selected);
    held[0].release();
    await expect.poll(() => held[0].delivered).toBe(true);
    await expect(target(page)).toHaveValue(ids.selected);
  } finally { held.forEach((entry) => entry.release()); }
});

test('runtime hierarchy, eq, folder scope and RLS remain ANDed in select and picker', async ({ page }) => {
  await open(page);
  expect((await options(page, query(''))).total).toBe(0);
  await direction(page).selectOption(ids.a);
  await expect.poll(async () => target(page).locator('option').count()).toBe(51); // empty + first 50
  await settled(page);
  const filtered = await options(page, query(ids.a, ids.hidden, { limit: '100' }));
  expect(filtered.total).toBe(51);
  expect(filtered.items).toHaveLength(51);
  expect(filtered.selected_allowed).toBe(false);
  for (const forbidden of [ids.legacy, ids.hidden, ids.folder]) expect(filtered.items.map((item) => item.id)).not.toContain(forbidden);
  const exact = await options(page, query(ids.a, undefined, { element: 'exact-picker', limit: '100' }));
  expect(exact.total).toBe(26); // root items only; child A.1 is excluded by eq
  // Click the actual picker button beside the target select.
  await target(page).locator('..').locator('[data-ob-ref-picker="closest"]').click();
  await expect(page.locator('#_rp-list ._rp-item')).toHaveCount(50);
  await page.locator('#_rp-search').fill('needle');
  await expect(page.locator('#_rp-list ._rp-item')).toHaveCount(2);
  await expect(page.locator('#_rp-list')).not.toContainText(/hidden|folder|legacy|other B/);
  await page.locator('#_rp-search').fill('hidden');
  await expect(page.locator('#_rp-list ._rp-item')).toHaveCount(0);
  const forged = await page.request.get(`/ui/_ref-options/Неисправность?${query(ids.a, undefined, { sources: JSON.stringify({ 'Объект.Направление': ids.a, injected: ids.b }) })}`);
  expect(forged.status()).toBe(400);
});

test('runtime network failure retains the filtered options and current value', async ({ page }) => {
  await open(page, ids.pageTwoOwner);
  const before = await target(page).locator('option').allTextContents();
  await page.route(requests, (route) => route.abort('failed'));
  await direction(page).selectOption(ids.b);
  await expect(target(page)).toHaveAttribute('data-ob-choice-error', '1');
  await expect(target(page)).toHaveValue(ids.selected);
  expect(await target(page).locator('option').allTextContents()).toEqual(before);
});
