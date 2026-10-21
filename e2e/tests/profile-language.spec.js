const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

const first = { login: process.env.OB_USER_LOGIN || 'user', password: process.env.OB_USER_PASSWORD || 'Us3r-P@ssw0rd!' };
const second = { login: process.env.OB_SECOND_USER_LOGIN || 'second-user', password: process.env.OB_SECOND_USER_PASSWORD || 'Us3r-P@ssw0rd!' };

async function languageForm(page) {
  if (!await page.locator('#sysd').isVisible()) {
    await page.locator('[data-ob-toggle-target="sysd"]').click();
  }
  const form = page.locator('form[action="/ui/profile/lang"]');
  const group = page.locator('#sysd details').filter({ has: form });
  if (await group.getAttribute('open') === null) await group.locator('summary').click();
  await expect(form).toBeVisible();
  return form;
}

test('обычный пользователь выбирает личный язык, сохраняет его между входами и возвращает язык базы', async ({ page, browser, baseURL }) => {
  test.setTimeout(60_000);
  const other = await browser.newContext({ baseURL, locale: 'ru-RU' });
  const fresh = await browser.newContext({ baseURL, locale: 'ru-RU' });
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  try {
    await login(page, first);
    let form = await languageForm(page);
    await expect(form.locator('select')).toHaveValue('');
    await expect(form.locator('option[value="en"]')).toHaveText('English');
    await expect(form.locator('option[value="de"]')).toHaveText('Deutsch');
    await form.locator('select').selectOption('en');
    await form.locator('button[type=submit]').click();
    form = await languageForm(page);
    await expect(form.locator('label')).toHaveText('Interface language');
    await expect(form.locator('select')).toHaveValue('en');

    const otherPage = await other.newPage();
    await login(otherPage, second);
    const otherForm = await languageForm(otherPage);
    await expect(otherForm.locator('label')).toHaveText('Язык интерфейса');
    await expect(otherForm.locator('select')).toHaveValue('');

    const freshPage = await fresh.newPage();
    await login(freshPage, first);
    const freshForm = await languageForm(freshPage);
    await expect(freshForm.locator('label')).toHaveText('Interface language');
    await expect(freshForm.locator('select')).toHaveValue('en');

    await form.locator('select').selectOption('');
    await form.locator('button[type=submit]').click();
    form = await languageForm(page);
    await expect(form.locator('label')).toHaveText('Язык интерфейса');
    await expect(form.locator('select')).toHaveValue('');
    await open(freshPage, '/ui/');
    await expect((await languageForm(freshPage)).locator('select')).toHaveValue('');
    expect(errors).toEqual([]);
  } finally {
    // Keep the common e2e fixture in its original state for subsequent tests.
    await page.request.post('/ui/profile/lang', { form: { lang: '' } });
    await other.close();
    await fresh.close();
  }
});
