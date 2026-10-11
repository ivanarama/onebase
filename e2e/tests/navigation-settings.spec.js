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

test('общий предпросмотр показывает иконки раздела, папки и пункта до сохранения', async ({ page }) => {
  await login(page); await resetAPI(page, common);
  const state = await editor(page, common);
  const section = state.desired.sections.find(x => x.items?.length);
  expect(section).toBeTruthy();
  const item = section.items[0];
  const preview = page.locator('#navigation-preview');
  const sprite = state.iconSprite;
  async function icon(id, name, target) {
    await node(page, id).click();
    const response = page.waitForResponse(r => r.url().endsWith('/navigation/preview') && r.status() === 200);
    await page.locator('#navigation-properties select').first().selectOption(name);
    await response;
    await expect(target.locator('svg use')).toHaveAttribute('href', sprite+'#'+name);
    await expect(target.locator('svg')).toHaveAttribute('aria-hidden', 'true');
    expect(await target.locator('svg').evaluate(el => el.namespaceURI)).toBe('http://www.w3.org/2000/svg');
    await expect.poll(() => target.locator('svg use').evaluate(el => el.getBBox().width)).toBeGreaterThan(0);
  }
  await title(page, section.id, 'Section icon preview');
  await icon(section.id, 'house', preview.locator('h3').filter({ hasText: 'Section icon preview' }));
  await page.locator('#navigation-add-group').click();
  const group = await page.locator('[data-node-id^="new:"]').getAttribute('data-node-id');
  // Empty folders are omitted by the production preview resolver.
  await node(page, item.id).click();
  await page.locator('#navigation-properties select').nth(1).selectOption(group);
  const literal = '<img src=x onerror=alert(1)> Folder icon preview';
  await title(page, group, literal);
  await icon(group, 'book-open', preview.locator('summary').filter({ hasText: literal }));
  const itemPreview = state.preview.flatMap(s => s.items || []).find(x => x.id === item.id);
  expect(itemPreview).toBeTruthy();
  await icon(item.id, 'shopping-cart', preview.locator('a').filter({ hasText: itemPreview.label }));
  await expect(preview.locator('img')).toHaveCount(0);
  await expect(preview).toContainText(literal);
  // Preview requests must leave the persisted revision and layout untouched.
  const fresh = await page.request.get(common);
  expect(fresh.ok()).toBeTruthy();
  const html = await fresh.text();
  const persisted = JSON.parse(html.match(/id="navigation-editor-data">([^<]+)<\/script>/)[1]);
  expect(persisted.revision).toBe(state.revision);
  expect(persisted.desired).toEqual(state.desired);
});

test('предпросмотр безопасно нормализует имена иконок и отвергает устаревший ответ', async ({ page }) => {
  await login(page); await resetAPI(page, common);
  let requests = 0, release;
  await page.route('**/ui/admin/navigation/preview', async route => {
    const response = await route.fetch();
    expect(response.ok()).toBeTruthy();
    const stale = ++requests === 1;
    if (stale) await new Promise(resolve => { release = resolve; });
    await route.fulfill({ response, json: { preview: [{
      title: stale ? 'Stale' : '<img src=x onerror=alert(1)> Safe', icon: stale ? 'book-open' : ' __HOME-- ',
      items: [{ label: 'Unknown', url: '/ui/', icon: 'https://evil.invalid/x#house' }, { label: 'Empty', url: '/ui/', icon: ' _-- ' }],
      groups: [{ title: 'Normalized', icon: ' Shopping__Cart ', items: [{ label: '<svg onload=alert(1)>', url: '/ui/', icon: '"/><image href="https://evil.invalid/x"' }] }]
    }] } });
  });
  const state = await editor(page, common);
  await title(page, state.desired.sections[0].id, 'First request');
  await expect.poll(() => !!release).toBe(true);
  await title(page, state.desired.sections[0].id, 'Second request');
  const preview = page.locator('#navigation-preview');
  await expect(preview.locator('h3 use')).toHaveAttribute('href', state.iconSprite+'#house');
  await expect(preview.locator('summary use')).toHaveAttribute('href', state.iconSprite+'#shopping-cart');
  await expect(preview.locator('a').filter({ hasText: 'Unknown' }).locator('use')).toHaveAttribute('href', state.iconSprite+'#square');
  await expect(preview.locator('a').filter({ hasText: 'Empty' }).locator('svg')).toHaveCount(0);
  await expect(preview.locator('details a use')).toHaveAttribute('href', state.iconSprite+'#square');
  await expect(preview.locator('img,image,script')).toHaveCount(0);
  const stale = page.waitForResponse(r => r.url().endsWith('/navigation/preview'));
  release(); await stale;
  // Let the fetch body and renderer settle before asserting the retained icon.
  await page.evaluate(() => new Promise(resolve => setTimeout(resolve, 50)));
  await expect(preview.locator('h3 use')).toHaveAttribute('href', state.iconSprite+'#house');
  await expect(preview.locator('h3')).toHaveText('<img src=x onerror=alert(1)> Safe');
});
