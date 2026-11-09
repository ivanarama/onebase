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

for (const scenario of ['unchecked', 'nested', 'local-required', 'checked']) {
  test(`hidden page checkbox → native Save, ${scenario}`, async ({ page }) => {
    const name = `PageCheckbox${scenario.replaceAll('-', '')}`;
    await open(page, name);
    const flag = page.locator('input[type="checkbox"][name="Flag"]');
    const marker = page.locator('input[name="_ob_present_Flag"]');
    await expect(flag).toBeChecked();
    await expect(marker).toBeEnabled();
    const data = await event(page, 'HidePage');
    const checked = scenario === 'checked';
    expect(data.values.Flag).toBe(checked);
    expect(Object.values(data.elementStates.hidden)).toContain(true);
    await expect(flag).toBeEnabled();
    await expect(marker).toBeEnabled();
    await expect(flag).not.toHaveAttribute('required');
    await expect(flag).toBeChecked({ checked });
    const entries = await page.locator('#main-form').evaluate((form) => Array.from(new FormData(form).entries()));
    expect(entries.filter(([key]) => key === '_ob_present_Flag')).toEqual([['_ob_present_Flag', '1']]);
    expect(entries.filter(([key]) => key === 'Flag')).toEqual(checked ? [['Flag', 'true']] : []);
    const saved = page.waitForResponse((r) => r.url() === urls[name] && r.request().method() === 'POST');
    const reloaded = page.waitForEvent('domcontentloaded');
    await page.locator('#main-form').evaluate((form) => form.requestSubmit());
    expect((await saved).status()).toBe(303);
    await reloaded;
    const stored = await page.request.post(urls[name].replace(/\/[^/]+$/, '/form-event'), { form: {
      _id: urls[name].split('/').pop(), _kind: 'object', _element: 'Read', _event: 'Нажатие',
    } });
    const result = await stored.json();
    expect(result.ok, JSON.stringify(result)).toBe(true);
    expect(result.values.Flag).toBe(checked);
  });
}

for (const scenario of ['before', 'after', 'nested']) {
  test(`checkbox controls page visibility before native Save, ${scenario}`, async ({ page }) => {
    const name = `PageCondition${scenario}`;
    await open(page, name);
    const flag = page.locator('input[type="checkbox"][name="Flag"]');
    const text = page.locator('input[name="Text"]');
    await expect(flag).toBeChecked();
    await expect(text).toHaveAttribute('required', '');
    for (let i = 0; i < 2; i++) {
      const hidden = await event(page, 'HidePage');
      expect(hidden.values.Flag).toBe(false);
      expect(Object.values(hidden.elementStates.hidden)).toContain(true);
      await expect(text).not.toHaveAttribute('required');
      await event(page, 'ShowPage');
      await expect(flag).toBeChecked();
      await expect(text).toHaveAttribute('required', '');
    }
    await event(page, 'HidePage');
    await expect(flag).not.toBeChecked();
    await expect(tab(page, 'Conditional')).toBeHidden();
    const entries = await page.locator('#main-form').evaluate((form) => Array.from(new FormData(form).entries()));
    expect(entries.filter(([key]) => key === '_ob_present_Flag')).toEqual([['_ob_present_Flag', '1']]);
    expect(entries.filter(([key]) => key === 'Flag')).toEqual([]);
    expect(entries.filter(([key]) => key === 'Text')).toEqual([['Text', '']]);
    const saved = page.waitForResponse((r) => r.url() === urls[name] && r.request().method() === 'POST');
    const reloaded = page.waitForEvent('domcontentloaded');
    await page.locator('#main-form').evaluate((form) => form.requestSubmit());
    const response = await saved;
    expect(response.status()).toBe(303);
    const submitted = new URLSearchParams(response.request().postData());
    expect(submitted.get('_ob_present_Flag')).toBe('1');
    expect(submitted.has('Flag')).toBe(false);
    await reloaded;
    const stored = await page.request.post(urls[name].replace(/\/[^/]+$/, '/form-event'), { form: {
      _id: urls[name].split('/').pop(), _kind: 'object', _element: 'Read', _event: 'Нажатие',
    } });
    const result = await stored.json();
    expect(result.ok, JSON.stringify(result)).toBe(true);
    expect(result.values.Flag).toBe(false);
    expect(result.values.Text ?? '').toBe('');
  });
}

// Combined regression: these are Server.Mount-rendered tabs and real DSL
// form-events, with the entire production managed.js and native browser APIs.
for (const reverse of [false, true]) {
  test(`nested hidden pages preserve each group's selection, reverse=${reverse}`, async ({ page }) => {
    await open(page, 'NestedHiddenTabs');
    const outer = tab(page, 'Conditional');
    const inner = tab(page, 'InnerConditional');
    const innerHome = tab(page, 'InnerHome');
    const input = masked(page);
    const innerPage = page.locator('.managed-tabs[data-tabs="Inner"] > .managed-tab-content[data-tab-content="1"]');
    await outer.click();
    await inner.click();
    await tab(page, 'Home').click();
    await outer.click();
    await expect(inner).toHaveClass(/active/);
    await expect(innerPage).toBeVisible();
    // The same pathname and data-tabs keys restore both groups independently.
    await page.reload();
    await expect(outer).toHaveClass(/active/);
    await expect(inner).toHaveClass(/active/);
    await expect(innerPage).toBeVisible();
    for (let cycle = 0; cycle < 2; cycle++) {
      await event(page, reverse ? 'HideInner' : 'Hide');
      await event(page, reverse ? 'Hide' : 'HideInner');
      await expect(outer).toBeHidden();
      await expect(inner).not.toHaveClass(/active/);
      await expect(innerHome).toHaveClass(/active/);
      await expect(input).not.toHaveAttribute('required');
      await expect(input).not.toHaveAttribute('pattern');
      // Both groups have their own first visible page, with the same index 0.
      await expect(tab(page, 'Home')).toHaveClass(/active/);
      await event(page, reverse ? 'Show' : 'ShowInner');
      await expect(input).not.toHaveAttribute('pattern');
      await event(page, reverse ? 'ShowInner' : 'Show');
      await expect(input).toHaveAttribute('required', '');
      await expect(input).toHaveAttribute('pattern', '[0-9]+');
      // Showing an available page preserves an already available active page.
      await expect(tab(page, 'Home')).toHaveClass(/active/);
      await outer.click();
      await expect(innerHome).toHaveClass(/active/);
      await inner.click();
      await expect(innerPage).toBeVisible();
    }
    await event(page, 'Hide');
    await event(page, 'HideInner');
    await event(page, 'HideRemaining');
    expect(await page.locator('.managed-tab-btn.active').count()).toBe(0);
    expect(await page.locator('.managed-tab-content').evaluateAll((nodes) => nodes.every((node) => node.style.display === 'none'))).toBe(true);
    await event(page, 'ShowInner');
    await expect(input).not.toHaveAttribute('pattern');
    await event(page, 'Show');
    await expect(outer).toHaveClass(/active/);
    await expect(inner).toHaveClass(/active/);
    await expect(input).toHaveAttribute('pattern', '[0-9]+');
    await event(page, 'ShowRemaining');
    await expect(inner).toHaveClass(/active/);
    await expect(innerPage).toBeVisible();
  });
}

test('nested native validation skips hidden ancestors and reveals the next available invalid', async ({ page }) => {
  await open(page, 'NestedHiddenTabs');
  const input = masked(page);
  const next = page.locator('input[name="Следующий"]');
  await tab(page, 'Conditional').click();
  await tab(page, 'InnerConditional').click();
  await event(page, 'Hide');
  await expect(input).not.toHaveAttribute('pattern');
  // A separate native rule is deliberately outside form-local suspension.
  await input.evaluate((el) => el.setCustomValidity('still invalid'));
  expect(await page.locator('#main-form').evaluate((form) => form.reportValidity())).toBe(false);
  await expect(tab(page, 'Conditional')).toBeHidden();
  await expect(next).toBeFocused();
  await expect(tab(page, 'Available')).toHaveClass(/active/);
  await next.fill('ready');
  await input.evaluate((el) => el.setCustomValidity(''));
  await event(page, 'Show');
  await tab(page, 'Home').click();
  await page.locator('#main-form').evaluate((form) => form.requestSubmit());
  await expect(input).toBeFocused();
  await expect(tab(page, 'Conditional')).toHaveClass(/active/);
  await expect(tab(page, 'InnerConditional')).toHaveClass(/active/);
  await expect(input).toHaveAttribute('required', '');
  await expect(input).toHaveAttribute('pattern', '[0-9]+');
  expect(await page.evaluate(() => window.nativeSubmissions.length)).toBe(0);
  await input.fill('123');
  expect(await page.locator('#main-form').evaluate((form) => form.reportValidity())).toBe(true);
});
