const { test, expect } = require('@playwright/test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn, execFileSync } = require('node:child_process');
const { once } = require('node:events');

// A compiled Go test hosts the actual mounted UI routes and a fresh SQLite DB.
// Only static assets are provided by Playwright. Rendering, form-event payloads,
// native validation, FormData and the final Save all use the real public path.
let work, fixture, urls;
const repo = path.join(__dirname, '../..');
test.beforeAll(async () => {
  test.setTimeout(120_000);
  work = fs.mkdtempSync(path.join(os.tmpdir(), 'ob-page-mask-'));
  const binary = path.join(work, process.platform === 'win32' ? 'ui.test.exe' : 'ui.test');
  execFileSync('go', ['test', '-c', '-o', binary, './internal/ui'], { cwd: repo, timeout: 100_000 });
  fixture = spawn(binary, ['-test.run=^TestManagedPageMaskBrowserFixture$', '-test.timeout=5m'], {
    cwd: path.join(repo, 'internal/ui'),
    env: { ...process.env, ONEBASE_PAGE_MASK_FIXTURE: '1' },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  // The fixture prints the record URLs as one marked stdout line.
  let output = '';
  fixture.stdout.on('data', (chunk) => { output += chunk; });
  fixture.stderr.on('data', (chunk) => { output += chunk; });
  const marker = /^ONEBASE_PAGE_MASK_URLS (.+)$/m;
  for (let i = 0; !marker.test(output); i++) {
    if (fixture.exitCode !== null || i > 200) throw new Error(`UI fixture did not start: ${output}`);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  urls = JSON.parse(marker.exec(output)[1]);
});
test.afterAll(async () => {
  if (fixture && fixture.exitCode === null) {
    const exited = once(fixture, 'exit');
    fixture.stdin.end();
    const [code] = await exited;
    expect(code).toBe(0);
  }
  if (work) fs.rmSync(work, { recursive: true, force: true });
});

async function open(page, name) {
  await page.route('**/static/**', async (route) => {
    if (new URL(route.request().url()).pathname === '/static/managed.js') {
      await route.fulfill({ path: path.join(repo, 'internal/ui/static/managed.js'), contentType: 'text/javascript' });
    } else await route.fulfill({ body: '', contentType: 'text/css' });
  });
  await page.goto(urls[name]);
  await expect(page.locator('#main-form')).toBeVisible();
  // Observe successful native submissions without cancelling or substituting Save.
  await page.evaluate(() => {
    window.nativeSubmissions = [];
    document.getElementById('main-form').addEventListener('submit', (event) => {
      window.nativeSubmissions.push(Array.from(new FormData(event.target).entries()));
    });
  });
}
async function event(page, name) {
  const response = page.waitForResponse((r) => r.url().endsWith('/form-event') && r.request().method() === 'POST');
  await page.locator(`[data-ob-fire-click="${name}"]`).click();
  const data = await (await response).json();
  expect(data.ok, JSON.stringify(data)).toBe(true);
  return data;
}
const masked = (page) => page.locator('.managed-tab-content input[name="Текст"]');
const tab = (page, name) => page.locator(`.managed-tab-btn[data-tab-page="${name}"]`);

for (const required of [false, true]) {
  for (const nested of [false, true]) {
    for (const active of [false, true]) {
      test(`server hide → native Save, required=${required}, nested=${nested}, active=${active}`, async ({ page }) => {
        const name = `PageMask${required}${nested}false`;
        if (nested && active) {
          // Keep the real server response but reverse its hidden-key order.
          // This covers the other serialization order without inventing states.
          await page.route('**/form-event', async (route) => {
            const response = await route.fetch();
            const data = await response.json();
            if (data.elementStates && data.elementStates.hidden) {
              data.elementStates.hidden = Object.fromEntries(Object.entries(data.elementStates.hidden).reverse());
            }
            await route.fulfill({ response, json: data });
          });
        }
        await open(page, name);
        const input = masked(page);
        await expect(input).toHaveAttribute('pattern', '[0-9]+');
        expect(await input.getAttribute('required')).toBe(required ? '' : null);
        if (active) await tab(page, 'Conditional').click();
        const data = await event(page, 'Hide');
        expect(data.values.Текст).toBe('invalid');
        expect(Object.values(data.elementStates.hidden)).toContain(true);
        await expect(input).toHaveValue('invalid');
        await expect(input).not.toHaveAttribute('required');
        await expect(input).not.toHaveAttribute('pattern');
        await expect(input).toBeEnabled();
        await expect(tab(page, 'Conditional')).toBeHidden();

        // Repeated hide/show and two hidden ancestors must preserve exact rules.
        if (nested) {
          await event(page, 'HideInner');
          await event(page, 'Show');
          await expect(input).not.toHaveAttribute('pattern');
          await event(page, 'Hide');
          await event(page, 'ShowInner');
          await expect(input).not.toHaveAttribute('pattern');
        }
        await event(page, 'Hide');
        await event(page, 'Show');
        await expect(input).toHaveAttribute('pattern', '[0-9]+');
        expect(await input.getAttribute('required')).toBe(required ? '' : null);
        // An ordinary inactive tab remains invalid and becomes available for correction.
        await tab(page, 'Home').click();
        await page.locator('#main-form').evaluate((form) => form.requestSubmit());
        await expect(input).toBeFocused();
        expect(await input.evaluate((el) => el.validity.patternMismatch)).toBe(true);
        expect(await page.evaluate(() => window.nativeSubmissions.length)).toBe(0);
        await input.fill('123');
        expect(await page.locator('#main-form').evaluate((form) => form.reportValidity())).toBe(true);
        await event(page, 'Hide');

        const formData = await page.locator('#main-form').evaluate((form) => Array.from(new FormData(form).entries()));
        expect(formData.filter(([key]) => key === 'Текст')).toEqual([['Текст', 'invalid']]);
        const saved = page.waitForResponse((r) => r.url() === urls[name] && r.request().method() === 'POST');
        await page.locator('#main-form').evaluate((form) => form.requestSubmit());
        const response = await saved;
        expect(response.status()).toBe(303);
        expect(new URLSearchParams(response.request().postData()).get('Текст')).toBe('invalid');
        await page.waitForLoadState();
        // Read the persisted object via another successful public form-event.
        const endpoint = urls[name].replace(/\/[^/]+$/, '/form-event');
        const result = await page.request.post(endpoint, { form: {
          _id: urls[name].split('/').pop(), _kind: 'object', _element: 'Show', _event: 'Нажатие',
        } });
        const stored = await result.json();
        expect(stored.ok, JSON.stringify(stored)).toBe(true);
        expect(stored.values.Текст).toBe('invalid');
        // Restore DB state for the next independent browser context.
        const reset = await page.request.post(urls[name], { form: {
          Текст: '123', Скрыть: 'false', СкрытьВнутри: 'false', action: 'save', _version: String(Number(formData.find(([key]) => key === '_version')[1]) + 1),
        }, maxRedirects: 0 });
        expect(reset.status()).toBe(303);
      });
    }
  }
}

test('visible copy keeps its own required and mask while the other copy is hidden', async ({ page }) => {
  await open(page, 'PageMasktruefalsetrue');
  await event(page, 'Hide');
  const visible = page.locator('input[name="Текст"]').filter({ visible: true });
  await expect(masked(page)).not.toHaveAttribute('pattern');
  await expect(visible).toHaveAttribute('pattern', '[a-z]+');
  await expect(visible).toHaveAttribute('required', '');
  await visible.fill('!!!');
  await page.locator('#main-form').evaluate((form) => form.requestSubmit());
  await expect(visible).toBeFocused();
  expect(await visible.evaluate((el) => el.validity.patternMismatch)).toBe(true);
  expect(await page.evaluate(() => window.nativeSubmissions.length)).toBe(0);
});


test('other native patterns on the hidden page remain enforced', async ({ page }) => {
  await open(page, 'PageMaskfalsefalsefalse');
  const number = page.locator('input[name="Число"]');
  const pattern = await number.getAttribute('pattern');
  expect(pattern).toBeTruthy();
  await event(page, 'Hide');
  await expect(number).toHaveAttribute('pattern', pattern);
  // A non-mask native rule stays outside the suspension contract, regardless
  // of its text. Use a simple valid pattern for the final validation probe.
  await number.evaluate((input) => { input.setAttribute('pattern', '[0-9]+'); });
  await event(page, 'Hide');
  await expect(number).toHaveAttribute('pattern', '[0-9]+');
  await number.evaluate((input) => { input.value = 'invalid-number'; });
  expect(await number.evaluate((input) => input.validity.patternMismatch)).toBe(true);
  await page.locator('#main-form').evaluate((form) => form.requestSubmit());
  expect(await page.evaluate(() => window.nativeSubmissions.length)).toBe(0);
  await expect(tab(page, 'Conditional')).toBeHidden();
});


test('restores exact per-control attribute values after repeated suspension', async ({ page }) => {
  await open(page, 'PageMasktruefalsefalse');
  const input = masked(page);
  await input.evaluate((el) => { el.setAttribute('required', 'required'); });
  for (let i = 0; i < 2; i++) {
    await event(page, 'Hide');
    await event(page, 'Hide');
    await expect(input).not.toHaveAttribute('required');
    await expect(input).not.toHaveAttribute('pattern');
    await event(page, 'Show');
    await expect(input).toHaveAttribute('required', 'required');
    await expect(input).toHaveAttribute('pattern', '[0-9]+');
    await expect(input).not.toHaveAttribute('data-ob-page-required');
    await expect(input).not.toHaveAttribute('data-ob-page-pattern');
  }
});
