// Live dependent reference choices (plan 170/C): the browser must never let a
// slow response for an old source overwrite the latest source, and a network
// failure must keep the last filtered set instead of exposing a full catalog.

const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

const MAIN_WAREHOUSE = 'Главный склад «Чёрная дыра»';
const DISCOUNT_WAREHOUSE = 'Склад уценёнки «Хламоприёмник»';

test.beforeEach(async ({ page }) => {
  await login(page);
  await open(page, '/ui/document/Инвентаризация/new');
});

async function optionValue(select, label) {
  const option = select.locator('option').filter({hasText: label}).first();
  await expect(option).toHaveCount(1);
  const value = await option.getAttribute('value');
  expect(value).toBeTruthy();
  return value;
}

async function waitForMainLocations(target) {
  await expect.poll(async () => (await target.locator('option').allTextContents()).join('\n'))
    .toContain('Стеллаж 1 «Покосившийся»');
}

test('последний склад побеждает запоздавший ответ предыдущего', async ({ page }) => {
  const warehouse = page.locator('select[name="Склад"]');
  const target = page.locator('select[name="МестоХраненияВыбора"]');
  const mainID = await optionValue(warehouse, MAIN_WAREHOUSE);
  const discountID = await optionValue(warehouse, DISCOUNT_WAREHOUSE);
  const seenSources = [];
  let mainFinished = false;

  await page.route('**/ui/_ref-options/**', async (route) => {
    const url = new URL(route.request().url());
    if (!url.searchParams.get('form_entity')) {
      await route.continue();
      return;
    }
    const sources = JSON.parse(url.searchParams.get('sources') || '{}');
    const source = sources['Объект.Склад'];
    seenSources.push(source);
    if (source === mainID) await new Promise((resolve) => setTimeout(resolve, 500));
    try {
      await route.continue();
    } catch (_) {
      // AbortController is expected to cancel the deliberately delayed request.
    }
    if (source === mainID) mainFinished = true;
  });

  await warehouse.selectOption(mainID);
  await warehouse.selectOption(discountID);

  await expect(target).toHaveValue('');
  await expect.poll(async () => (await target.locator('option').allTextContents()).join('\n'))
    .not.toContain('Стеллаж');
  await expect.poll(() => seenSources.includes(discountID)).toBe(true);
  await expect.poll(() => mainFinished).toBe(true);
  expect((await target.locator('option').allTextContents()).join('\n')).not.toContain('Стеллаж 1 «Покосившийся»');
  expect(seenSources).toContain(mainID);
});

test('неподходящее текущее значение очищается и отправляет change', async ({ page }) => {
  const warehouse = page.locator('select[name="Склад"]');
  const target = page.locator('select[name="МестоХраненияВыбора"]');
  const mainID = await optionValue(warehouse, MAIN_WAREHOUSE);
  const discountID = await optionValue(warehouse, DISCOUNT_WAREHOUSE);

  await warehouse.selectOption(mainID);
  await waitForMainLocations(target);
  const selected = await optionValue(target, 'Стеллаж 1 «Покосившийся»');
  await target.selectOption(selected);
  await target.evaluate((element) => {
    window.__obChoiceFilterChanges = 0;
    element.addEventListener('change', () => { window.__obChoiceFilterChanges++; });
  });

  let preservedResponseSent = false;
  const preserveOutsidePage = async (route) => {
    const url = new URL(route.request().url());
    const sources = JSON.parse(url.searchParams.get('sources') || '{}');
    if (sources['Объект.Склад'] !== discountID || !url.searchParams.get('selected_id')) {
      await route.continue();
      return;
    }
    preservedResponseSent = true;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({items: [], total: 51, selected_allowed: true}),
    });
  };
  await page.route('**/ui/_ref-options/**', preserveOutsidePage);
  await warehouse.selectOption(discountID);
  await expect.poll(() => preservedResponseSent).toBe(true);
  await expect(target).not.toHaveAttribute('data-ob-choice-loading', '1');
  await expect(target).toHaveValue(selected);
  await expect.poll(() => page.evaluate(() => window.__obChoiceFilterChanges)).toBe(0);

  await page.unroute('**/ui/_ref-options/**', preserveOutsidePage);
  await warehouse.selectOption(mainID);
  await waitForMainLocations(target);
  await warehouse.selectOption(discountID);
  await expect(target).toHaveValue('');
  await expect.poll(() => page.evaluate(() => window.__obChoiceFilterChanges)).toBe(1);
});

test('сетевая ошибка сохраняет предыдущее значение и фильтрованный список', async ({ page }) => {
  const warehouse = page.locator('select[name="Склад"]');
  const target = page.locator('select[name="МестоХраненияВыбора"]');
  const mainID = await optionValue(warehouse, MAIN_WAREHOUSE);
  const discountID = await optionValue(warehouse, DISCOUNT_WAREHOUSE);

  await warehouse.selectOption(mainID);
  await waitForMainLocations(target);
  const selected = await optionValue(target, 'Стеллаж 1 «Покосившийся»');
  await target.selectOption(selected);
  const before = await target.locator('option').allTextContents();

  await page.route('**/ui/_ref-options/**', async (route) => {
    const url = new URL(route.request().url());
    const sources = JSON.parse(url.searchParams.get('sources') || '{}');
    if (sources['Объект.Склад'] === discountID) {
      await route.abort('failed');
      return;
    }
    await route.continue();
  });
  await warehouse.selectOption(discountID);

  await expect(target).toHaveAttribute('data-ob-choice-error', '1');
  await expect(target).toHaveValue(selected);
  expect(await target.locator('option').allTextContents()).toEqual(before);
});
