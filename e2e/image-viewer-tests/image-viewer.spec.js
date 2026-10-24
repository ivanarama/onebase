const {test, expect} = require('@playwright/test');
const zlib = require('node:zlib');
const fs = require('node:fs/promises');
const {login, open, SAVE} = require('../tests/helpers');

// Test images are generated locally, then uploaded/saved via public UI/HTTP.
// No viewer markup or controller is injected into the page.
function png(width, height) {
  function crc32(buffer) {
    let crc = 0xffffffff;
    for (const byte of buffer) {
      crc ^= byte;
      for (let i = 0; i < 8; i++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0);
    }
    return (crc ^ 0xffffffff) >>> 0;
  }
  function chunk(type, data) {
    const name = Buffer.from(type);
    const length = Buffer.alloc(4); length.writeUInt32BE(data.length);
    const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(Buffer.concat([name, data])));
    return Buffer.concat([length, name, data, crc]);
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0); header.writeUInt32BE(height, 4);
  header[8] = 8; header[9] = 2;
  const rows = Buffer.alloc((width * 3 + 1) * height);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const pos = y * (width * 3 + 1) + 1 + x * 3;
      rows[pos] = x % 256; rows[pos + 1] = y % 256; rows[pos + 2] = 180;
    }
  }
  return Buffer.concat([Buffer.from('89504e470d0a1a0a', 'hex'), chunk('IHDR', header), chunk('IDAT', zlib.deflateSync(rows)), chunk('IEND', Buffer.alloc(0))]);
}
const large = png(1600, 1200);
const small = png(64, 48);
const portrait = png(300, 1600);
const gif = Buffer.from('R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7', 'base64');
const dialogSelector = '#ob-image-viewer';
const action = (name) => `[data-ob-viewer-action="${name}"]`;
const field = (name) => `.img-field[data-ob-image-field="${name}"]`;
const trigger = (name) => `${field(name)} [data-ob-image-view]`;

async function upload(page, name, bytes, mimeType = 'image/png') {
  const response = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/_image'));
  await page.locator(`${field(name)} input[type=file]`).first().setInputFiles({name: 'photo.' + mimeType.split('/')[1], mimeType, buffer: bytes});
  const result = await response;
  expect(result.status()).toBe(200);
  const ref = (await result.json()).ref;
  await expect(page.locator(trigger(name)).first()).toHaveAttribute('data-ob-image-ref', ref);
  await expect(page.locator(trigger(name)).first()).toBeEnabled();
  return ref;
}
async function seed(page, entity, options = {}) {
  await open(page, `/ui/catalog/${entity}/new`);
  await page.locator('input[name=Name]').fill(`viewer ${Date.now()}`);
  await page.locator('input[name=Owner]').fill(options.owner || 'user');
  const first = await upload(page, 'First', options.first || large);
  const second = await upload(page, 'Second', options.second || small, options.secondMime || 'image/png');
  let secret;
  if (options.secret) secret = await upload(page, 'Secret', portrait);
  await Promise.all([
    page.waitForURL((url) => /\/ui\/catalog\/[^/]+\/[0-9a-f-]{36}$/.test(url.pathname)),
    page.locator(SAVE).first().click(),
  ]);
  return {url: new URL(page.url()).pathname, first, second, secret};
}
async function ready(scope) {
  await expect(scope.locator(dialogSelector)).toBeVisible();
  await expect(scope.locator('.ob-image-viewport')).toHaveAttribute('data-loaded', '1');
}
async function geometry(scope) {
  return scope.locator('.ob-image-viewport').evaluate((viewport) => {
    const image = viewport.querySelector('img');
    const rect = image.getBoundingClientRect();
    return {width: rect.width, height: rect.height, naturalWidth: image.naturalWidth, naturalHeight: image.naturalHeight,
      clientWidth: viewport.clientWidth, clientHeight: viewport.clientHeight, scrollWidth: viewport.scrollWidth,
      scrollHeight: viewport.scrollHeight, left: viewport.scrollLeft, top: viewport.scrollTop};
  });
}
test.beforeEach(async ({page}) => { await login(page); });

for (const entity of ['AutoPhoto', 'ManagedPhoto']) {
  test(`${entity}: gallery, keyboard, fit, zoom, drag, focus and download`, async ({page, context}) => {
    const record = await seed(page, entity);
    const title = entity === 'ManagedPhoto' ? '<b>First camera</b>' : 'First camera';
    await page.locator(trigger('First')).first().focus();
    await page.keyboard.press('Enter');
    await ready(page);
    await expect(page.locator('#ob-image-viewer-title')).toHaveText(title);
    await expect(page.locator('#ob-image-viewer-title b')).toHaveCount(0);
    await expect(page.locator(action('imageClose'))).toBeFocused();
    let fitted = await geometry(page);
    expect(fitted.width).toBeLessThanOrEqual(1600);
    expect(fitted.width).toBeLessThanOrEqual(fitted.clientWidth + 1);
    expect(fitted.height).toBeLessThanOrEqual(fitted.clientHeight + 1);
    await expect(page.locator(action('imagePrevious'))).toBeDisabled();
    await page.keyboard.press('ArrowRight');
    await ready(page);
    await expect(page.locator('#ob-image-viewer-title')).toHaveText('Second camera');
    const second = await geometry(page);
    expect(second.width).toBe(64); expect(second.height).toBe(48);
    // Managed form repeats First on an inactive page, but there are only two
    // gallery positions. Empty and masked fields never become positions.
    await expect(page.locator(action('imageNext'))).toBeDisabled();
    await page.keyboard.press('ArrowLeft');
    await ready(page);
    await page.locator(action('imageOriginal')).click();
    let original = await geometry(page);
    expect(original.width).toBe(1600); expect(original.height).toBe(1200);
    expect(original.scrollWidth).toBeGreaterThan(original.clientWidth);
    const viewport = page.locator('.ob-image-viewport');
    const area = await viewport.boundingBox();
    await page.mouse.move(area.x + area.width / 2, area.y + area.height / 2);
    await page.mouse.wheel(0, -100);
    await expect.poll(async () => (await geometry(page)).width).toBe(2000);
    await page.mouse.move(area.x + area.width / 2, area.y + area.height / 2);
    await page.mouse.down();
    await page.mouse.move(area.x + 20, area.y + 20, {steps: 5});
    await page.mouse.up();
    const dragged = await geometry(page);
    expect(dragged.left).toBeGreaterThan(0); expect(dragged.top).toBeGreaterThan(0);
    await expect(page.locator(dialogSelector)).toBeVisible();
    await viewport.focus();
    await page.keyboard.press('End');
    await page.keyboard.press('PageDown');
    // Actual scrollable dimensions make the far corner reachable without a
    // transform that merely paints outside an unscrollable layout box.
    await viewport.evaluate((node) => {node.scrollLeft = node.scrollWidth; node.scrollTop = node.scrollHeight;});
    const corner = await geometry(page);
    expect(corner.left).toBe(corner.scrollWidth - corner.clientWidth);
    expect(corner.top).toBe(corner.scrollHeight - corner.clientHeight);
    const beforeResize = await geometry(page);
    await page.setViewportSize({width: 900, height: 640});
    expect((await geometry(page)).width).toBe(beforeResize.width);
    await page.locator(action('imageFit')).click();
    fitted = await geometry(page);
    expect(fitted.width).toBeLessThanOrEqual(fitted.clientWidth + 1);
    expect(fitted.height).toBeLessThanOrEqual(fitted.clientHeight + 1);
    await page.setViewportSize({width: 1100, height: 760});
    await expect.poll(async () => (await geometry(page)).width).toBeGreaterThan(fitted.width);
    const downloadPromise = page.waitForEvent('download');
    await page.locator(action('imageDownload')).click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe(`image-${record.first}.png`);
    expect(await fs.readFile(await download.path())).toEqual(large);
    const popupPromise = context.waitForEvent('page');
    await page.locator(action('imageNewTab')).click();
    const popup = await popupPromise;
    await popup.waitForLoadState();
    expect(new URL(popup.url()).pathname).toBe(`/ui/_image/${record.first}`);
    expect(await popup.evaluate(() => window.opener)).toBeNull();
    await popup.close();
    await page.keyboard.press('Escape');
    await expect(page.locator(dialogSelector)).toHaveCount(0);
    await expect(page.locator(trigger('First')).first()).toBeFocused();
    await page.keyboard.press('Space');
    await ready(page);
    await page.locator(action('imageClose')).click();
    await page.locator(trigger('First')).first().click();
    await ready(page);
    // Clicking the native backdrop closes the viewer without a form submit.
    const bounds = await page.locator(dialogSelector).boundingBox();
    await page.mouse.click(bounds.x - 3, bounds.y + 3);
    await expect(page.locator(dialogSelector)).toHaveCount(0);
    expect(new URL(page.url()).pathname).toBe(record.url);
  });
}

test('upload/clear gives a fresh gallery and preserves dirty managed values', async ({page}) => {
  await seed(page, 'ManagedPhoto');
  await page.locator('input[name=Name]').fill('unsaved viewer name');
  await page.locator(trigger('First')).first().click();
  await ready(page);
  await page.keyboard.press('Control+s');
  await page.keyboard.press('Escape');
  await expect(page.locator(dialogSelector)).toHaveCount(0);
  await expect(page.locator('#ob-managed-close-confirm')).toHaveCount(0);
  await expect(page.locator('input[name=Name]')).toHaveValue('unsaved viewer name');
  await upload(page, 'First', portrait);
  await page.locator(trigger('First')).first().click();
  await ready(page);
  expect((await geometry(page)).naturalHeight).toBe(1600);
  await page.keyboard.press('Escape');
  await page.locator(`${field('First')} [data-ob-image-clear]`).first().click();
  await expect(page.locator(trigger('First')).first()).toBeDisabled();
  await page.locator('.managed-tab-btn').nth(1).click();
  await page.locator(trigger('Second')).click();
  await ready(page);
  await expect(page.locator(action('imagePrevious'))).toBeDisabled();
  await expect(page.locator(action('imageNext'))).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(page.locator('input[name=Name]')).toHaveValue('unsaved viewer name');
});

test('stale loads and errors cannot replace the latest photo; GIF/JPEG decode', async ({page}) => {
  const record = await seed(page, 'AutoPhoto', {first: portrait, second: gif, secondMime: 'image/gif'});
  await page.route(`**/ui/_image/${record.first}`, async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 300));
    await route.continue().catch(() => {});
  });
  await page.locator(trigger('First')).click();
  await page.locator(action('imageNext')).click();
  await ready(page);
  await expect(page.locator('#ob-image-viewer-title')).toHaveText('Second camera');
  expect((await geometry(page)).naturalWidth).toBe(1);
  await page.waitForTimeout(350);
  expect((await geometry(page)).naturalWidth).toBe(1);
  await page.keyboard.press('Escape');
  await page.unroute(`**/ui/_image/${record.first}`);
  await page.route(`**/ui/_image/${record.first}`, (route) => route.fulfill({status: 403, body: 'Forbidden'}));
  await page.locator(trigger('First')).click();
  await expect(page.locator('.ob-image-status')).toHaveText('Не удалось загрузить изображение');
  await page.locator(action('imageNext')).click();
  await ready(page);
  await page.keyboard.press('Escape');
  await page.unroute(`**/ui/_image/${record.first}`);
  const jpeg = await page.evaluate(() => {
    const canvas = document.createElement('canvas');
    canvas.width = 32; canvas.height = 24;
    return canvas.toDataURL('image/jpeg').split(',')[1];
  });
  await upload(page, 'First', Buffer.from(jpeg, 'base64'), 'image/jpeg');
  await page.locator(trigger('First')).click();
  await ready(page);
  expect((await geometry(page)).naturalWidth).toBe(32);
  await page.keyboard.press('Escape');
});

for (const entity of ['AutoPhoto', 'ManagedPhoto']) {
  test(`${entity}: authenticated read-only, masking and denied RLS`, async ({page, browser}) => {
    const record = await seed(page, entity, {secret: true});
    const userContext = await browser.newContext();
    const userPage = await userContext.newPage();
    await login(userPage, {login: 'user', password: 'Us3r-P@ssw0rd!'});
    await open(userPage, record.url);
    await expect(userPage.locator('[data-ob-image-upload]')).toHaveCount(0);
    await expect(userPage.locator('[data-ob-image-clear]')).toHaveCount(0);
    const deniedUpload = await userPage.request.post(`/ui/catalog/${entity}/_image`, {
      multipart: {file: {name: 'forbidden.png', mimeType: 'image/png', buffer: small}},
    });
    expect(deniedUpload.status()).toBe(403);
    expect(await userPage.locator('body').innerHTML()).not.toContain(record.secret);
    await userPage.locator(trigger('First')).first().click();
    await ready(userPage);
    await userPage.locator(action('imageNext')).click();
    await ready(userPage);
    await expect(userPage.locator(action('imageNext'))).toBeDisabled();
    await userPage.keyboard.press('Escape');
    for (const query of ['', '?download=1']) {
      const response = await userPage.request.get(`/ui/_image/${record.first}${query}`);
      expect(response.status()).toBe(200); expect(await response.body()).toEqual(large);
    }
    const deniedContext = await browser.newContext();
    const deniedPage = await deniedContext.newPage();
    await login(deniedPage, {login: 'second-user', password: 'Us3r-P@ssw0rd!'});
    for (const query of ['', '?download=1']) {
      const denied = await deniedPage.request.get(`/ui/_image/${record.first}${query}`);
      expect(denied.status()).toBe(403);
      const missing = await deniedPage.request.get('/ui/_image/00000000-0000-0000-0000-000000000001' + query);
      expect(missing.status()).toBe(404);
    }
    await deniedPage.goto(record.url);
    await expect(deniedPage.locator('[data-ob-image-view]')).toHaveCount(0);
    await userContext.close(); await deniedContext.close();
  });
}

test('hidden managed field is excluded even on an inactive tab', async ({page}) => {
  const record = await seed(page, 'ManagedPhoto');
  await page.locator('select[name=HideSecond]').selectOption('true');
  await Promise.all([page.waitForNavigation({waitUntil: 'domcontentloaded'}), page.locator(SAVE).first().click()]);
  await page.locator(trigger('First')).first().click();
  await ready(page);
  await expect(page.locator(action('imageNext'))).toBeDisabled();
  await page.keyboard.press('Escape');
  expect(new URL(page.url()).pathname).toBe(record.url);
});

test('iframe Esc closes only viewer and preserves popup and unsaved data', async ({page}) => {
  await open(page, '/ui/catalog/RefHost/new');
  await page.locator('[data-ob-ref-picker]').click();
  // Inline create is a real picker action, not a direct controller invocation.
  await page.locator('#_ref-picker-modal').getByText('Создать', {exact: false}).click();
  const iframe = page.frameLocator('#_ref-create-modal iframe');
  await iframe.locator('input[name=Name]').fill('unsaved popup');
  const response = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/_image'));
  await iframe.locator(`${field('First')} input[type=file]`).first().setInputFiles({name: 'photo.png', mimeType: 'image/png', buffer: large});
  expect((await response).status()).toBe(200);
  await iframe.locator(trigger('First')).first().click();
  await ready(iframe);
  await iframe.locator(action('imageClose')).press('Escape');
  await expect(iframe.locator(dialogSelector)).toHaveCount(0);
  await expect(page.locator('#_ref-create-modal')).toBeVisible();
  await expect(iframe.locator('input[name=Name]')).toHaveValue('unsaved popup');
  await expect(iframe.locator('#ob-managed-close-confirm')).toHaveCount(0);
});

test('same UUID in different fields keeps both captions and duplicate element uses clicked caption', async ({page}) => {
  const record = await seed(page, 'ManagedPhoto');
  const version = await page.locator('input[name=_version]').inputValue();
  const saved = await page.request.post(record.url, {form: {
    Name: 'same image, two cameras', Owner: 'user', First: record.first,
    Second: record.first, _version: version, HideSecond: 'false',
  }});
  expect(saved.ok()).toBe(true);
  await open(page, record.url);
  await page.locator('.managed-tab-btn').nth(1).click();
  await page.locator(trigger('First')).nth(1).click();
  await ready(page);
  await expect(page.locator('#ob-image-viewer-title')).toHaveText('First again');
  await page.locator(action('imageNext')).click();
  await ready(page);
  await expect(page.locator('#ob-image-viewer-title')).toHaveText('Second camera');
  await expect(page.locator(action('imageNext'))).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(page.locator(trigger('First')).nth(1)).toBeFocused();
});

test('touch viewport, focus trap and zoom limit use real geometry', async ({browser}) => {
  const touchContext = await browser.newContext({viewport: {width: 390, height: 700}, isMobile: true, hasTouch: true});
  const page = await touchContext.newPage();
  await login(page);
  await seed(page, 'AutoPhoto', {first: portrait});
  await page.locator(trigger('First')).click();
  await ready(page);
  const initial = await geometry(page);
  expect(initial.height).toBeLessThanOrEqual(initial.clientHeight + 1);
  for (let i = 0; i < 20; i++) {
    await page.keyboard.press('Tab');
    expect(await page.locator(dialogSelector).evaluate((node) => node.contains(document.activeElement))).toBe(true);
  }
  await page.locator(action('imageOriginal')).click();
  await page.locator('.ob-image-viewport').dispatchEvent('wheel', {deltaY: -100, ctrlKey: true});
  expect((await geometry(page)).width).toBe(300);
  const area = await page.locator('.ob-image-viewport').boundingBox();
  const beforePan = await geometry(page);
  const touch = await touchContext.newCDPSession(page);
  const x = area.x + area.width / 2, y = area.y + area.height / 2;
  await touch.send('Input.dispatchTouchEvent', {type: 'touchStart', touchPoints: [{x, y, id: 1}]});
  await touch.send('Input.dispatchTouchEvent', {type: 'touchMove', touchPoints: [{x, y: y - 60, id: 1}]});
  await touch.send('Input.dispatchTouchEvent', {type: 'touchEnd', touchPoints: []});
  expect((await geometry(page)).top).toBeGreaterThan(beforePan.top);
  await touch.detach();
  while (await page.locator(action('imageZoomIn')).isEnabled()) await page.locator(action('imageZoomIn')).click();
  expect((await geometry(page)).width).toBe(2400);
  await page.locator(action('imageFit')).click();
  await expect(page.locator(action('imageZoomOut'))).toBeDisabled();
  await page.keyboard.press('Escape');
  await touchContext.close();
});
