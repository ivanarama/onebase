// Реальные сессии, мышь/клавиатура и общий браузерный контроллер (#1362).
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

const first = { login: process.env.OB_USER_LOGIN || 'user', password: process.env.OB_USER_PASSWORD || 'Us3r-P@ssw0rd!' };
const second = { login: process.env.OB_SECOND_USER_LOGIN || 'second-user', password: process.env.OB_SECOND_USER_PASSWORD || 'Us3r-P@ssw0rd!' };
const personal = '/ui/settings/navigation';
const common = '/ui/admin/navigation';
const boot = page => page.locator('#navigation-editor-data').evaluate(el => JSON.parse(el.textContent));
const node = (page, id) => page.locator(`[data-node-id="${id}"]`);

async function editor(page, path = personal) {
  await open(page, path);
  await expect(page.locator('#navigation-editor-data')).toHaveAttribute('data-initialized', '1');
  return boot(page);
}
async function save(page) {
  const revision = (await boot(page)).revision;
  await page.locator('#navigation-save button[type=submit]').click();
  await page.waitForURL(url => url.searchParams.get('saved') === '1');
  await expect.poll(async () => (await boot(page)).revision).not.toBe(revision);
}
async function title(page, id, value) {
  await node(page, id).click();
  await page.locator('#navigation-properties input[type=text]').fill(value);
}
async function resetAPI(page, path) {
  const state = await editor(page, path);
  const response = await page.request.post(path+'/reset', { form: { subsystem: state.subsystem, revision: state.revision } });
  expect(response.ok()).toBeTruthy();
}

test('личная папка: мышь, клавиатура, сохранение, изоляция и отдельный сброс', async ({ page, browser, baseURL }) => {
  test.setTimeout(60_000);
  const admin = await browser.newContext({ baseURL });
  const bob = await browser.newContext({ baseURL });
  try {
    const adminPage = await admin.newPage(); await login(adminPage); await resetAPI(adminPage, common);
    await login(page, first); await resetAPI(page, personal);
    const bobPage = await bob.newPage(); await login(bobPage, second); await resetAPI(bobPage, personal);
    let state = await editor(page);
    const section = state.desired.sections.find(x => x.items?.length);
    expect(section).toBeTruthy();
    const item = section.items[0];
    await node(page, section.id).click();
    await page.locator('#navigation-add-group').click();
    const groupID = await page.locator('[data-node-id^="new:"]').getAttribute('data-node-id');
    const literal = '<img src=x onerror=alert(1)> My folder';
    await title(page, groupID, literal);
    await page.locator(`[data-node-id="${item.id}"]`).locator('..').dragTo(page.locator(`[data-node-id="${groupID}"]`).locator('..'));
    await node(page, item.id).focus(); await page.keyboard.press('Alt+ArrowLeft');
    await expect.poll(async () => JSON.parse(await page.locator('#navigation-desired').inputValue()).sections.find(x => x.id === section.id).items.some(x => x.id === item.id)).toBe(true);
    await page.keyboard.press('Alt+ArrowRight');
    await save(page);
    state = await editor(page);
    const persisted = state.desired.sections.flatMap(x => x.groups || []).find(x => x.title === literal);
    expect(persisted.id).toMatch(/^usr:/); expect(persisted.items.some(x => x.id === item.id)).toBe(true);
    await expect(page.locator('#navigation-tree img')).toHaveCount(0);
    const other = await editor(bobPage);
    expect(JSON.stringify(other.desired)).not.toContain('My folder');
    // A common rename flows to Bob, while Alice's explicit title remains.
    await title(page, section.id, 'My years'); await save(page);
    const commonState = await editor(adminPage, common);
    expect(commonState.desired.sections.some(x => x.id === section.id)).toBe(true);
    await title(adminPage, section.id, 'Common years'); await save(adminPage);
    expect((await editor(bobPage)).desired.sections.find(x => x.id === section.id).title).toBe('Common years');
    expect((await editor(page)).desired.sections.find(x => x.id === section.id).title).toBe('My years');
    // Reset is a real UI submission with an explicit confirmation.
    page.once('dialog', dialog => dialog.accept());
    await page.locator('#navigation-reset button[type=submit]').click();
    await page.waitForURL(url => url.searchParams.get('saved') === '1');
    await expect.poll(async () => (await boot(page)).revision).toBe('');
    expect((await boot(page)).desired.sections.find(x => x.id === section.id).title).toBe('Common years');
  } finally {
    await admin.close(); await bob.close();
  }
});

test('две вкладки сохраняют черновик при 409 и явно загружают победителя', async ({ page }) => {
  await login(page, first); await resetAPI(page, personal);
  const state = await editor(page);
  const section = state.desired.sections[0];
  const stale = await page.context().newPage();
  try {
    await editor(stale);
    await title(page, section.id, 'Winner tab'); await save(page);
    await title(stale, section.id, 'Unsaved tab');
    const conflict = stale.waitForResponse(response => response.url().endsWith('/save') && response.status() === 409);
    await stale.locator('#navigation-save button[type=submit]').click(); await conflict;
    await expect(stale.locator('#navigation-status button')).toBeVisible();
    await expect(stale.locator('#navigation-properties input[type=text]')).toHaveValue('Unsaved tab');
    expect((await boot(stale)).revision).toBe(state.revision);
    await expect(stale.locator('#navigation-preview')).toContainText('Winner tab');
    stale.once('dialog', dialog => dialog.accept());
    await stale.locator('#navigation-status button').click();
    await expect.poll(async () => (await boot(stale)).revision).not.toBe(state.revision);
    expect((await boot(stale)).desired.sections.find(x => x.id === section.id).title).toBe('Winner tab');
  } finally { await stale.close(); }
});

test('фиксированная ссылка доступна после скрытия всего личного меню', async ({ page }) => {
  await login(page, first); await resetAPI(page, personal);
  const state = await editor(page);
  for (const section of state.desired.sections) {
    const row = node(page, section.id).locator('..');
    // Last row button is the hide/remove action, independent of UI language.
    await row.locator('button').last().click();
  }
  await save(page);
  await open(page, '/ui/');
  await expect(page.locator('a[href^="/ui/settings/navigation?"]')).toHaveCount(1);
  expect((await editor(page)).desired.sections).toEqual([]);
  await resetAPI(page, personal);
});
