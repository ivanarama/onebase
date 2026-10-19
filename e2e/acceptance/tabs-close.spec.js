const { test, expect } = require('@playwright/test');
const ROOT = '/ui/catalog/ПриемкаЗакрытия/';
const frames = page => page.locator('#ob-tabbody iframe');
let errors = [], closes = [];

test.beforeEach(async ({ page }) => {
  errors = []; closes = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('response', async response => {
    if (new URL(response.url()).pathname.endsWith('/form-close-intent')) {
      closes.push(await response.json());
    }
  });
  // Observe real SSE and real postMessage traffic. The last scenario delays
  // delivery of one observed URL message, retaining its actual WindowProxy.
  await page.addInitScript(() => {
    window.closeAcceptance = { commands: [], urls: [], held: null, holdURL: false };
    window.addEventListener('onebase:ui.закрытьФорму', e => {
      window.closeAcceptance.commands.push({ data: e.detail, at: Date.now() });
    });
    window.addEventListener('message', e => {
      if (e.data?.source !== 'obFrameURLChanged') return;
      window.closeAcceptance.urls.push(e.data.url);
      if (window.closeAcceptance.holdURL) {
        window.closeAcceptance.held = { data: e.data, source: e.source, origin: e.origin, document: e.source.document };
        e.stopImmediatePropagation();
      }
    }, true);
  });
  const login = await page.request.post('/auth/login', { data: { login: 'admin', password: 'secret123' } });
  expect(login.ok()).toBe(true);
  await page.goto('/ui/app', { waitUntil: 'domcontentloaded' });
  await expect.poll(() => page.evaluate(() => window.__obEvents?.readyState)).toBe(1);
});
test.afterEach(() => expect(errors).toEqual([]));

async function openForm(page, suffix = 'new') {
  // Use the shell's public open command, with the full production frame DOM.
  await page.evaluate(url => window.obOpenTab(url, 'Приемка'), ROOT + suffix);
  const handle = await frames(page).last().elementHandle();
  const frame = await handle.contentFrame();
  await expect(frame.locator('input[name="Наименование"]')).toBeVisible();
  return frame;
}

async function holdSave(page, frame, name, deny = false) {
  await frame.locator('input[name="Наименование"]').fill(name);
  if (deny) await frame.locator('select[name="Запрет"]').selectOption('true');
  let release;
  const barrier = new Promise(resolve => { release = resolve; });
  let data;
  const handler = async route => {
    // Execute the real DSL handler now. Deliver its bytes only after SSE has
    // reached the real shell; neither SSE nor form-event response is fabricated.
    const response = await route.fetch();
    data = await response.json();
    expect(data.ok).toBe(true);
    expect(data.savedId).toMatch(/^[0-9a-f-]{36}$/);
    expect(data.version).toBe(1);
    await barrier;
    try { await route.fulfill({ response }); }
    catch (e) { if (!route.request().failure()) throw e; }
  };
  await page.route('**/form-event', handler);
  try {
    await frame.locator('[data-ob-el="ЗаписатьЗакрыть"]').click();
    await expect.poll(() => data?.savedId).toBeTruthy();
    await expect.poll(() => page.evaluate(() => window.closeAcceptance.commands.length)).toBe(1);
    expect(await page.evaluate(() => window.closeAcceptance.commands[0].data.id)).toBe(data.savedId);
    expect(new URL(frame.url()).pathname).toMatch(/\/new$/);
    expect(await page.evaluate(() => window.closeAcceptance.urls)).toEqual([]);
    expect(closes).toEqual([]);
    return { data, release, async finish() { release(); await page.unroute('**/form-event', handler); } };
  } catch (error) {
    release();
    await page.unroute('**/form-event', handler);
    throw error;
  }
}

async function persisted(page, id, name) {
  const response = await page.request.get(ROOT + id);
  expect(response.ok()).toBe(true);
  expect(await response.text()).toContain(name);
}

test('tab close: early SSE closes only the saved form once and a reopened object stays open', async ({ page }) => {
  const first = await openForm(page);
  const held = await holdSave(page, first, 'saved once');
  try {
    const other = await openForm(page, 'new?other=1');
    held.release();
    await expect(frames(page)).toHaveCount(1);
    expect(other.isDetached()).toBe(false);
    expect(new URL(other.url()).pathname).toMatch(/\/new$/);
    await expect.poll(() => closes.length).toBe(1);
    expect(closes[0].close.allowed).toBe(true);
    await persisted(page, held.data.savedId, 'saved once');
    const reopened = await openForm(page, held.data.savedId);
    await expect(reopened.locator('input[name="Наименование"]')).toHaveValue('saved once');
    // This roundtrip drains the frame's load/URL messages, rather than sleeping
    // and assuming a duplicate deferred command would already have happened.
    await reopened.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await expect(frames(page)).toHaveCount(2);
    expect(closes).toHaveLength(1);
  } finally { await held.finish(); }
});

for (const age of [14999, 15000]) {
  test(`tab close: pending command age ${age} ms`, async ({ page }) => {
    // Date is fixed; native timers, networking and close lifecycle still run.
    // Advance only the shell's wall clock, with no 15-second timing flake.
    await page.clock.setFixedTime(new Date('2026-10-18T12:00:00Z'));
    const frame = await openForm(page);
    const held = await holdSave(page, frame, `boundary ${age}`);
    try {
      const at = await page.evaluate(() => window.closeAcceptance.commands[0].at);
      await page.clock.setFixedTime(new Date(at + age));
      held.release();
      if (age === 14999) {
        await expect(frames(page)).toHaveCount(0);
        await expect.poll(() => closes.length).toBe(1);
        expect(closes[0].close.allowed).toBe(true);
      } else {
        await expect.poll(() => frame.url()).toContain('/' + held.data.savedId);
        await expect.poll(() => page.evaluate(() => window.closeAcceptance.urls.length)).toBe(1);
        await frame.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        await expect(frames(page)).toHaveCount(1);
        expect(closes).toEqual([]);
      }
      await persisted(page, held.data.savedId, `boundary ${age}`);
    } finally { await held.finish(); }
  });
}

test('tab close: server refusal, dirty cancel and confirmed save use the real close protocol', async ({ page }) => {
  const frame = await openForm(page);
  const held = await holdSave(page, frame, 'denied', true);
  try {
    held.release();
    await expect.poll(() => closes.length).toBe(1);
    expect(closes[0].close.allowed).toBe(false);
    await expect(frames(page)).toHaveCount(1);
    await expect.poll(() => frame.url()).toContain('/' + held.data.savedId);
    await frame.locator('select[name="Запрет"]').selectOption('false');
    await frame.locator('input[name="Наименование"]').fill('confirmed save');
    await page.locator('.ob-tab-close').click();
    await frame.locator('[data-ob-close-choice="cancel"]').click();
    await expect(frame.locator('#ob-managed-close-confirm')).toHaveCount(0);
    await expect(frames(page)).toHaveCount(1);
    expect(closes).toHaveLength(1);
    await page.locator('.ob-tab-close').click();
    await frame.locator('[data-ob-close-choice="save"]').click();
    await expect(frames(page)).toHaveCount(0);
    await expect.poll(() => closes.length).toBe(2);
    expect(closes[1].close.allowed).toBe(true);
    expect(closes[1].close.saved).toBe(true);
    await persisted(page, held.data.savedId, 'confirmed save');
  } finally { await held.finish(); }
});

for (const sameURL of [false, true]) {
  test(`tab close: delayed URL message cannot close a replacement Document (${sameURL ? 'same URL' : 'different URL'})`, async ({ page }) => {
    const frame = await openForm(page);
    const held = await holdSave(page, frame, 'old document');
    try {
      await page.evaluate(() => { window.closeAcceptance.holdURL = true; });
      held.release();
      await expect.poll(() => page.evaluate(() => !!window.closeAcceptance.held)).toBe(true);
      await frame.goto(ROOT + (sameURL ? held.data.savedId : 'new?replacement=1'), { waitUntil: 'domcontentloaded' });
      await expect(frame.locator('input[name="Наименование"]')).toHaveValue(sameURL ? 'old document' : '');
      const result = await page.evaluate(() => {
        const state = window.closeAcceptance;
        const e = state.held;
        state.holdURL = false;
        const sameWindow = document.querySelector('#ob-tabbody iframe').contentWindow === e.source;
        // Re-deliver precisely the data/source/origin of the production message
        // held above, after a new Document has occupied the same WindowProxy.
        window.dispatchEvent(new MessageEvent('message', { data: e.data, source: e.source, origin: e.origin }));
        return { sameWindow, documentChanged: e.document !== e.source.document };
      });
      expect(result).toEqual({ sameWindow: true, documentChanged: true });
      await frame.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      await expect(frames(page)).toHaveCount(1);
      expect(closes).toEqual([]);
      if (sameURL) expect(new URL(frame.url()).pathname).toContain('/' + held.data.savedId);
      else expect(new URL(frame.url()).search).toContain('replacement=1');
      await persisted(page, held.data.savedId, 'old document');
    } finally { await held.finish(); }
  });
}
