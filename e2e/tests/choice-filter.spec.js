// Live dependent reference choices (plans 170/C and 187/A): exercise the
// visible server-backed combobox and the single submitted UUID.
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

const MAIN_WAREHOUSE = 'Главный склад «Чёрная дыра»';
const DISCOUNT_WAREHOUSE = 'Склад уценёнки «Хламоприёмник»';
const MAIN_LOCATION = 'Стеллаж 1 «Покосившийся»';

test.beforeEach(async ({ page }) => {
  await login(page);
  await open(page, '/ui/document/Инвентаризация/new');
});

function reference(page, name) {
  const value = page.locator(`input[type="hidden"][name="${name}"]`);
  const control = value.locator('..');
  return {
    value,
    text: control.getByRole('combobox'),
    options: control.getByRole('option'),
    status: control.getByRole('status'),
  };
}

async function choose(ref, label) {
  await expect(ref.value).toHaveCount(1);
  await ref.text.fill(label);
  await ref.options.filter({ hasText: label }).click();
  await expect(ref.text).toHaveValue(label);
  await expect(ref.value).not.toHaveValue('');
  const id = await ref.value.inputValue();
  expect(await ref.value.evaluate(el => new FormData(el.form).getAll(el.name))).toEqual([id]);
  return id;
}

function choiceRequest(request) {
  const url = new URL(request.url());
  const sources = JSON.parse(url.searchParams.get('sources') || '{}');
  return { url, source: sources['Объект.Склад'] };
}

test('последний склад побеждает запоздавший ответ предыдущего', async ({ page }) => {
  const warehouse = reference(page, 'Склад');
  const target = reference(page, 'МестоХраненияВыбора');
  const mainID = await choose(warehouse, MAIN_WAREHOUSE);
  await expect(target.text).not.toHaveAttribute('aria-busy', 'true');
  // Deliver the old real response even when cancellation is unavailable: the
  // generation/source gate, rather than AbortController alone, must reject it.
  await page.evaluate(() => { window.AbortController = undefined; });
  let releaseMain;
  const held = new Promise(resolve => { releaseMain = resolve; });
  let mainHeld = false;
  let oldItems;
  const seenSources = [];
  await page.route('**/ui/_ref-options/**', async route => {
    const { url, source } = choiceRequest(route.request());
    if (source !== undefined) seenSources.push(source);
    if (source === mainID && url.searchParams.get('q') === 'Стеллаж') {
      const response = await route.fetch();
      oldItems = (await response.json()).items;
      mainHeld = true;
      await held;
      await route.fulfill({ response });
      return;
    }
    await route.continue();
  });
  await target.text.fill('Стеллаж');
  await expect.poll(() => mainHeld).toBe(true);
  expect(oldItems.map(item => item._label)).toContain(MAIN_LOCATION);
  try {
    const discountID = await choose(warehouse, DISCOUNT_WAREHOUSE);
    await expect.poll(() => seenSources.includes(discountID)).toBe(true);
    await expect(target.text).not.toHaveAttribute('aria-busy', 'true');
    const oldResponse = page.waitForResponse(r => {
      const { url, source } = choiceRequest(r.request());
      return source === mainID && url.searchParams.get('q') === 'Стеллаж';
    });
    await target.text.focus();
    releaseMain();
    await (await oldResponse).finished();
    // Let the delivered response's promise callbacks reach the browser DOM.
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await target.text.press('ArrowDown');
    await expect(target.options).toHaveCount(0);
    await expect(target.text).toHaveAttribute('aria-expanded', 'false');
    await expect(target.value).toHaveValue('');
    // A new user search must also be filtered by the final warehouse.
    const latest = page.waitForResponse(r => {
      const { url, source } = choiceRequest(r.request());
      return source === discountID && url.searchParams.get('q') === 'Стеллаж';
    });
    await target.text.fill('Стеллаж ');
    expect((await (await latest).json()).items || []).toEqual([]);
    await expect(target.text).not.toHaveAttribute('aria-busy', 'true');
    await expect(target.options).toHaveCount(0);
  } finally {
    releaseMain();
  }
});

test('selected_allowed вне страницы сохраняет UUID, неподходящий выбор очищается с change', async ({ page }) => {
  const warehouse = reference(page, 'Склад');
  const target = reference(page, 'МестоХраненияВыбора');
  const mainID = await choose(warehouse, MAIN_WAREHOUSE);
  const selected = await choose(target, MAIN_LOCATION);
  await target.value.evaluate(element => {
    window.__obChoiceFilterChanges = 0;
    element.addEventListener('change', () => { window.__obChoiceFilterChanges++; });
  });

  // Typing first clears the source UUID. Preserve the selected target for
  // that intermediate check too, then prove the final source check is exact.
  const checkedSources = [];
  const preserveOutsidePage = async route => {
    const { url, source } = choiceRequest(route.request());
    if (url.searchParams.get('check_selected_id') !== selected) {
      await route.continue();
      return;
    }
    checkedSources.push(source);
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [], total: 51, selected_allowed: true }),
    });
  };
  await page.route('**/ui/_ref-options/**', preserveOutsidePage);
  const discountID = await choose(warehouse, DISCOUNT_WAREHOUSE);
  await expect.poll(() => checkedSources.includes(discountID)).toBe(true);
  await expect(target.text).not.toHaveAttribute('aria-busy', 'true');
  await expect(target.value).toHaveValue(selected);
  await expect(target.text).toHaveValue(MAIN_LOCATION);
  expect(await target.value.evaluate(el => new FormData(el.form).getAll(el.name))).toEqual([selected]);
  expect(await page.evaluate(() => window.__obChoiceFilterChanges)).toBe(0);

  await page.unroute('**/ui/_ref-options/**', preserveOutsidePage);
  // The intermediate source-clear also uses the real server. It may clear
  // the target before the final choice, but must emit exactly one change.
  await choose(warehouse, MAIN_WAREHOUSE);
  await choose(target, MAIN_LOCATION);
  await page.evaluate(() => { window.__obChoiceFilterChanges = 0; });
  await choose(warehouse, DISCOUNT_WAREHOUSE);
  await expect(target.value).toHaveValue('');
  await expect(target.text).toHaveValue('');
  await expect.poll(() => page.evaluate(() => window.__obChoiceFilterChanges)).toBe(1);
  expect(mainID).not.toBe(discountID);
});

test('ошибка проверки сохраняет UUID, скрывает варианты и блокирует запись до повтора', async ({ page }) => {
  const warehouse = reference(page, 'Склад');
  const target = reference(page, 'МестоХраненияВыбора');
  await choose(warehouse, MAIN_WAREHOUSE);
  const selected = await choose(target, MAIN_LOCATION);
  let failedChecks = 0;
  const failSelectedCheck = async route => {
    const { url } = choiceRequest(route.request());
    if (url.searchParams.get('check_selected_id') === selected) {
      failedChecks++;
      await route.abort('failed');
      return;
    }
    await route.continue();
  };
  await page.route('**/ui/_ref-options/**', failSelectedCheck);
  await choose(warehouse, DISCOUNT_WAREHOUSE);
  await expect.poll(() => failedChecks).toBeGreaterThan(0);
  await expect(target.text).not.toHaveAttribute('aria-busy', 'true');
  await expect(target.value).toHaveValue(selected);
  await expect(target.text).toHaveValue(MAIN_LOCATION);
  await expect(target.status).not.toHaveText('');
  await expect(target.options).toHaveCount(0);
  await expect(target.text).toHaveAttribute('aria-expanded', 'false');
  expect(await target.text.evaluate(el => el.checkValidity())).toBe(false);
  expect(await target.value.evaluate(el => new FormData(el.form).getAll(el.name))).toEqual([selected]);

  await page.unroute('**/ui/_ref-options/**', failSelectedCheck);
  // Explicit user re-selection recovers after a failed membership check.
  await choose(warehouse, MAIN_WAREHOUSE);
  await expect(target.text).not.toHaveAttribute('aria-busy', 'true');
  await choose(target, MAIN_LOCATION);
  await expect(target.value).toHaveValue(selected);
  expect(await target.text.evaluate(el => el.checkValidity())).toBe(true);
});
