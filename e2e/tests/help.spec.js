const { test, expect } = require('@playwright/test');
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { login, open } = require('./helpers');

test('справка из управляемой формы и поиск по руководству', async ({ page, context }) => {
  await login(page);
  await open(page, '/ui/document/РеализацияТоваров/new');
  const help = page.locator('[data-ob-context-help]');
  await expect(help).toBeVisible();
  const [manual] = await Promise.all([context.waitForEvent('page'), help.click()]);
  await manual.waitForLoadState('domcontentloaded');
  await expect(manual.locator('.help-content')).toContainText('проведение');
  await expect(manual.locator('.help-content img')).toBeVisible();
  await expect.poll(() => manual.locator('.help-content img').evaluate(img => img.naturalWidth)).toBeGreaterThan(0);
  await manual.locator('form[action="/ui/help"] input[name="q"]').fill('продажу');
  await manual.locator('form[action="/ui/help"] button').click();
  await expect(manual.locator('main nav')).toContainText('Реализация товаров');
  await manual.locator('form[action="/ui/help"] input[name="q"]').fill('нет-такой-главы-123');
  await manual.locator('form[action="/ui/help"] button').click();
  await expect(manual.locator('main nav')).toContainText('Ничего не найдено');
});

test('сценарий съёмки создаёт настоящую PNG-иллюстрацию', async ({ page, baseURL }, testInfo) => {
  await login(page);
  const dir = testInfo.outputPath('capture-project');
  fs.mkdirSync(dir, { recursive: true });
  const state = testInfo.outputPath('private-session.json');
  await page.context().storageState({ path: state });
  const scenario = path.join(dir, 'screenshots.json');
  fs.writeFileSync(scenario, JSON.stringify({ base_url: baseURL, screenshots: [{
    file: 'sale-new.png', path: '/ui/document/РеализацияТоваров/new',
    wait_for: '[data-ob-context-help]', selector: 'main',
  }] }));
  const config = { scenario: JSON.parse(fs.readFileSync(scenario)), storageState: state,
    module: require.resolve('@playwright/test'), output: path.join(dir, 'docs/help/images') };
  // Run the exact embedded runner in a separate process, as docs capture does.
  const runner = fs.readFileSync(path.resolve(__dirname, '../../internal/cli/help_capture.cjs'), 'utf8');
  execFileSync(process.execPath, ['-e', runner + '\nmodule.exports.run();'], {
    input: JSON.stringify(config), timeout: 30000,
  });
  const image = fs.readFileSync(path.join(config.output, 'sale-new.png'));
  expect(image.subarray(0, 8)).toEqual(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
  expect(image.length).toBeGreaterThan(1000);
  await testInfo.attach('sale-new.png', { body: image, contentType: 'image/png' });
  fs.rmSync(state);
});
