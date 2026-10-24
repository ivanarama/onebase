const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

const first = { login: process.env.OB_USER_LOGIN || 'user', password: process.env.OB_USER_PASSWORD || 'Us3r-P@ssw0rd!' };
const second = { login: process.env.OB_SECOND_USER_LOGIN || 'second-user', password: process.env.OB_SECOND_USER_PASSWORD || 'Us3r-P@ssw0rd!' };

async function draft(page) {
  await login(page, first);
  await open(page, '/ui/app?home=1');
  await page.evaluate(() => {
    window.shellRun = true;
    window.obOpenTab('/ui/catalog/Номенклатура/new', 'Draft');
  });
  const field = page.frameLocator('#ob-tabbody iframe').locator('input[name="Наименование"]');
  await field.fill('Unsaved draft');
  await field.press('Tab');
  await expect(page.locator('.ob-tab.dirty')).toHaveCount(1);
  return { field, frame: await page.locator('#ob-tabbody iframe').elementHandle() };
}

async function sectionLink(page) {
  const link = page.locator('.subsys-bar a[href*="subsystem="]').first();
  await expect(link).toBeVisible();
  return link;
}

test('same shell ownership preserves the draft through section history', async ({ page }) => {
  const { field, frame } = await draft(page);
  const link = await sectionLink(page);
  const initialURL = page.url();
  await link.click();
  await expect(page).not.toHaveURL(initialURL);
  const sectionURL = page.url();
  await page.goBack();
  await expect(page).toHaveURL(initialURL);
  await page.goForward();
  await expect(page).toHaveURL(sectionURL);
  await expect.poll(() => page.evaluate(() => window.shellRun)).toBe(true);
  expect(await frame.evaluate(el => el === document.querySelector('#ob-tabbody iframe'))).toBe(true);
  // Section navigation selects the dashboard, leaving the form mounted.
  await page.locator('.ob-tab').click();
  await expect(field).toHaveValue('Unsaved draft');
});

test('section history ignores a superseded response', async ({ page }) => {
  const { field, frame } = await draft(page);
  const initialURL = page.url();
  await (await sectionLink(page)).click();
  await expect(page).not.toHaveURL(initialURL);
  const sectionURL = page.url();
  const dashboard = await page.locator('#ob-tabhome').innerText();
  const sections = await page.locator('.subsys-bar').innerHTML();
  let release, pending = false;
  const delayed = new Promise(resolve => { release = resolve; });
  await page.route(initialURL, async route => {
    const response = await route.fetch();
    pending = true;
    await delayed;
    await route.fulfill({ response });
  });
  await page.goBack();
  await expect.poll(() => pending).toBe(true);
  await page.goForward();
  await expect(page).toHaveURL(sectionURL);
  const received = page.waitForResponse(initialURL);
  release();
  await received;
  // Let the response and the two following rendering turns finish.
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await expect.poll(() => page.locator('#ob-tabhome').innerText()).toBe(dashboard);
  await expect.poll(() => page.locator('.subsys-bar').innerHTML()).toBe(sections);
  expect(await frame.evaluate(el => el === document.querySelector('#ob-tabbody iframe'))).toBe(true);
  expect(await page.evaluate(() => window.shellRun)).toBe(true);
  await page.locator('.ob-tab').click();
  await expect(field).toHaveValue('Unsaved draft');
});

test('section navigation initializes the current shell ownership', async ({ page }) => {
  await draft(page);
  const other = await page.context().newPage();
  try {
    await login(other, second);
    await open(other, '/ui/app?home=1');
    const expectedScope = await other.evaluate(() => sessionStorage.getItem('obTabsScope'));
    expect(expectedScope).toBeTruthy();
    const previousScope = await page.evaluate(() => sessionStorage.getItem('obTabsScope'));
    expect(expectedScope).not.toBe(previousScope);
    page.on('dialog', dialog => dialog.accept());
    const link = await sectionLink(page);
    // Only a new document emits DOMContentLoaded; a partial update does not.
    await Promise.all([
      page.waitForEvent('domcontentloaded', { timeout: 10_000 }),
      link.click(),
    ]);
    await expect.poll(() => page.evaluate(() => window.shellRun === undefined)).toBe(true);
    await expect(page.locator('#ob-shell-main')).toHaveAttribute('data-ob-tab-scope', expectedScope);
    await expect(page.locator('#ob-tabbody iframe')).toHaveCount(0);
    const stored = await page.evaluate(() => ({
      scope: sessionStorage.getItem('obTabsScope'),
      tabs: JSON.parse(sessionStorage.getItem('obTabs') || '[]'),
      active: sessionStorage.getItem('obTabsActive'),
    }));
    expect(stored.scope).toBe(expectedScope);
    expect(stored.tabs).toEqual([]);
    expect(stored.active || '').not.toContain('/new');
  } finally {
    await other.close();
  }
});

test('a response without shell ownership uses full initialization', async ({ page }) => {
  await draft(page);
  await page.route('**/ui/app?**', async route => {
    if (route.request().resourceType() !== 'fetch') return route.continue();
    const response = await route.fetch();
    const body = (await response.text()).replace(/ data-ob-tab-scope="[^"]*"/, '');
    await route.fulfill({ response, body });
  });
  page.on('dialog', dialog => dialog.accept());
  const link = await sectionLink(page);
  // Arm the full-document wait before the click can initiate navigation.
  await Promise.all([
    page.waitForEvent('domcontentloaded', { timeout: 10_000 }),
    link.click(),
  ]);
  await expect.poll(() => page.evaluate(() => window.shellRun === undefined)).toBe(true);
  await expect(page.locator('#ob-shell-main')).toHaveAttribute('data-ob-tab-scope', /.+/);
});
