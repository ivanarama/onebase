const { test, expect } = require('@playwright/test');
const path = require('node:path');

// The Go regression covers rendering and the form-event hidden map. These
// browser cases cover the next public entry point: native constraint validation
// in a real DOM, with the complete production runtime and actual required inputs.
// Visibility is applied through the production form-event client; only the
// transport is replaced. Native validation and submit use real browser APIs.
async function mount(page, { hidden = true, nested = false } = {}) {
  const inner = nested ? `
    <div class="managed-tabs" data-tabs="inner">
      <div><button type="button" class="managed-tab-btn active" data-tab-idx="0" data-ob-tab-path="0.0.0.0">Inner</button></div>
      <div class="managed-tab-content" data-tab-content="0" style="display:block">
        <input id="conditional" name="conditional" required>
      </div>
    </div>` : '<input id="conditional" name="conditional" required>';
  await page.setContent(`
    <form id="main-form">
      <div class="managed-tabs" data-tabs="outer">
        <div>
          <button id="conditional-tab" type="button" class="managed-tab-btn" data-tab-idx="0" data-ob-tab-path="0.0">Conditional</button>
          <button id="home-tab" type="button" class="managed-tab-btn active" data-tab-idx="1" data-ob-tab-path="0.1">Home</button>
          <button id="available-tab" type="button" class="managed-tab-btn" data-tab-idx="2" data-ob-tab-path="0.2">Available</button>
        </div>
        <div id="conditional-page" class="managed-tab-content" data-tab-content="0" style="display:none">${inner}</div>
        <div id="home-page" class="managed-tab-content" data-tab-content="1" style="display:block">Home</div>
        <div id="available-page" class="managed-tab-content" data-tab-content="2" style="display:none"><input id="available" name="available" required></div>
      </div>
      <button type="button" data-ob-fire-click="Change">Change state</button>
      <button type="submit">Save</button>
    </form>
    <script id="ob-managed-config" type="application/json">{"url":"/form-event"}</script>`);
  await page.evaluate(() => {
    window.submissions = [];
    document.getElementById('main-form').addEventListener('submit', (event) => {
      event.preventDefault();
      window.submissions.push(Object.fromEntries(new FormData(event.target)));
    });
    window.fetch = async () => ({ ok: true, json: async () => ({ ok: true, elementStates: window.nextStates }) });
  });
  await page.addScriptTag({ path: path.join(__dirname, '../../internal/ui/static/managed.js') });
  if (hidden) await changeState(page, { '0.0': true });
}

async function changeState(page, hidden) {
  await page.evaluate((hidden) => { window.nextStates = { hidden }; }, hidden);
  await page.locator('[data-ob-fire-click="Change"]').click();
  for (const [key, value] of Object.entries(hidden)) {
    const tab = page.locator(`[data-ob-tab-path="${key}"]`);
    if (value) await expect(tab).toBeHidden();
    else await expect(tab).toBeVisible();
  }
}

test('required stays suspended until both hidden ancestors are available', async ({ page }) => {
  await mount(page, { nested: true });
  await changeState(page, { '0.0.0.0': true });
  // The inner button is still hidden behind the outer page, so use its own
  // inline state below rather than a visibility assertion for hidden=false.
  await page.evaluate(async () => {
    window.nextStates = { hidden: { '0.0': false } };
    await window.obFire('Change', 'Нажатие');
  });
  await expect(page.locator('#conditional')).not.toHaveAttribute('required');
  await page.locator('#available-tab').click();
  await page.locator('#available').fill('complete');
  await page.locator('button[type="submit"]').click();
  expect(await page.evaluate(() => window.submissions.length)).toBe(1);

  await page.evaluate(async () => {
    window.nextStates = { hidden: { '0.0.0.0': false } };
    await window.obFire('Change', 'Нажатие');
  });
  await expect(page.locator('#conditional')).toHaveAttribute('required', '');
  await page.locator('button[type="submit"]').click();
  await expect(page.locator('#conditional')).toBeFocused();
  expect(await page.evaluate(() => window.submissions.length)).toBe(1);
});

for (const nested of [false, true]) {
  test(`native validation keeps a conditional page hidden${nested ? ' through its ancestor' : ''}`, async ({ page }) => {
    await mount(page, { nested });
    // Native validation visits the conditionally hidden input first. It must
    // still reveal and focus the later invalid input on an available tab.
    expect(await page.locator('#main-form').evaluate((form) => form.reportValidity())).toBe(false);
    await expect(page.locator('#conditional-tab')).toBeHidden();
    await expect(page.locator('#conditional-tab')).not.toHaveClass(/active/);
    await expect(page.locator('#conditional-page')).toBeHidden();
    await expect(page.locator('#available-page')).toBeVisible();
    await expect(page.locator('#available')).toBeFocused();

    // Repeat via the actual submit button in a fresh validation pass.
    await page.locator('#home-tab').click();
    await page.locator('button[type="submit"]').click();
    await expect(page.locator('#conditional-page')).toBeHidden();
    await expect(page.locator('#available')).toBeFocused();

    await page.locator('#available').fill('complete');
    await page.locator('button[type="submit"]').click();
    expect(await page.evaluate(() => window.submissions)).toEqual([{ conditional: '', available: 'complete' }]);
    await expect(page.locator('#conditional')).toBeEnabled();

    // Returning the page restores its form-local requirement. An ordinary
    // inactive page must still be opened and focused before it can be saved.
    await changeState(page, { '0.0': false });
    await page.locator('button[type="submit"]').click();
    await expect(page.locator('#conditional')).toBeFocused();
    expect(await page.evaluate(() => window.submissions.length)).toBe(1);
    await page.locator('#conditional').fill('restored');
    await page.locator('button[type="submit"]').click();
    expect(await page.evaluate(() => window.submissions.length)).toBe(2);
  });
}

test('native validation opens an ordinary inactive page and preserves the first invalid field', async ({ page }) => {
  await mount(page, { hidden: false });
  expect(await page.locator('#main-form').evaluate((form) => form.reportValidity())).toBe(false);
  await expect(page.locator('#conditional-page')).toBeVisible();
  await expect(page.locator('#conditional')).toBeFocused();
  await expect(page.locator('#available-page')).toBeHidden();

  await page.locator('#conditional').fill('complete');
  await page.locator('button[type="submit"]').click();
  await expect(page.locator('#available')).toBeFocused();
  await page.locator('#available').fill('complete');
  await page.locator('button[type="submit"]').click();
  expect(await page.evaluate(() => window.submissions.length)).toBe(1);
});

for (const reverse of [false, true]) {
  test(`changing the only visible page selects its replacement, reverse order=${reverse}`, async ({ page }) => {
    await mount(page, { hidden: false });
    await changeState(page, { '0.0': false, '0.1': true, '0.2': true });
    await expect(page.locator('#conditional-page')).toBeVisible();
    await changeState(page, reverse
      ? { '0.1': false, '0.0': true }
      : { '0.0': true, '0.1': false });
    await expect(page.locator('#home-tab')).toHaveClass(/active/);
    await expect(page.locator('#home-page')).toBeVisible();
    await expect(page.locator('#conditional-page')).toBeHidden();

    await changeState(page, { '0.1': true });
    await expect(page.locator('.managed-tab-btn.active')).toHaveCount(0);
    await changeState(page, { '0.2': false });
    await expect(page.locator('#available-tab')).toHaveClass(/active/);
    await expect(page.locator('#available-page')).toBeVisible();
  });
}
