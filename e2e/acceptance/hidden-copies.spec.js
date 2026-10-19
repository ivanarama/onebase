const { test, expect } = require('@playwright/test');
const ids = JSON.parse(process.env.OB_HIDDEN_COPY_IDS || '{}');
const eventURL = '**/form-event';
let pageErrors;
test.beforeEach(async ({ page }) => {
  pageErrors = [];
  page.on('pageerror', e => pageErrors.push(e.message));
});
test.afterEach(() => expect(pageErrors).toEqual([]));
const copies = (page) => page.locator('input[name="Текст"]');
const visible = (page) => page.locator('input[name="Текст"]:visible');

for (const name of Object.keys(ids)) {
  const url = `/ui/catalog/${name}/${ids[name]}?lang=ru`;
  async function open(page) {
    await page.goto(url);
    await expect(copies(page)).toHaveCount(2);
    await expect(visible(page)).toHaveCount(1);
  }
  async function submitted(page, value) {
    await expect(visible(page)).toHaveValue(value);
    expect(await page.locator('#main-form').evaluate(f => new FormData(f).getAll('Текст'))).toEqual([value]);
  }
  async function fire(page, button) {
    const [response] = await Promise.all([
      page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/form-event')),
      page.getByRole('button', { name: button, exact: true }).click(),
    ]);
    const data = await response.json();
    expect(data.ok).toBe(true);
    expect(data.error || '').toBe('');
    return data;
  }
  async function saveAndRead(page, value) {
    await submitted(page, value);
    const [response] = await Promise.all([
      page.waitForResponse(r => r.request().method() === 'POST' && !r.url().endsWith('/form-event') && decodeURI(r.url()).includes(`/ui/catalog/${name}/`)),
      page.locator('button[name="_action"][value=""]').first().click(),
    ]);
    expect(response.status()).toBeLessThan(400);
    await open(page);
    await submitted(page, value);
  }

  test(`${name}: echo survives both directions and persists`, async ({ page }) => {
    await open(page);
    for (const value of ['Новый ввод', 'Обратный ввод']) {
      await visible(page).fill(value);
      const data = await fire(page, 'Переключить');
      expect(data.values.Текст).toBe(value);
      await submitted(page, value);
      await expect(copies(page).nth(0)).toHaveValue(value);
      await expect(copies(page).nth(1)).toHaveValue(value);
      await saveAndRead(page, value);
    }
    // A handler's authoritative replacement must still win.
    await visible(page).fill('Ввод перед заменой');
    const data = await fire(page, 'Серверный');
    expect(data.values.Текст).toBe('Серверное');
    await saveAndRead(page, 'Серверное');
  });

  test(`${name}: real typing while response is delayed reaches the shown copy`, async ({ page }) => {
    await open(page);
    const modeBefore = await page.locator('input[name="Режим"]').inputValue();
    await visible(page).fill('Отправлено');
    let release;
    let arrived;
    const responseReady = new Promise(resolve => { arrived = resolve; });
    const held = new Promise(resolve => { release = resolve; });
    await page.route(eventURL, async route => {
      const response = await route.fetch();
      const data = await response.json();
      expect(data.ok).toBe(true);
      expect(data.values.Текст).toBe('Отправлено');
      arrived();
      await held;
      await route.fulfill({ response });
    });
    try {
      await page.getByRole('button', {name: 'Переключить', exact: true}).click();
      await responseReady;
      // Physical input events after snapshot, while the old copy is still visible.
      await visible(page).press('End');
      await visible(page).pressSequentially(' и дописано');
      release();
      await expect(page.locator('input[name="Режим"]')).not.toHaveValue(modeBefore);
      await saveAndRead(page, 'Отправлено и дописано');
    } finally { release(); }
  });
}
