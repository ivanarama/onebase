// Exercise the real form-event response path and the page CSS in Chromium.
// A background on tr alone is hidden by the zebra/hover backgrounds on td.
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

async function openHighlightPicker(page, config = {}) {
  await login(page);
  await open(page, '/ui/document/Инвентаризация/new');
  await page.locator('[data-tabs="ОсновныеЗакладки"] [data-tab-idx="1"]').click();

  const fields = await page.evaluate(() => obManagedConfig().serviceFields || {});

  // Replace only the command response: the user clicks a real managed button,
  // and managed.js dispatches pickerData to the normal picker renderer.
  await page.route('**/form-event*', async (route) => {
    const params = new URLSearchParams(route.request().postData() || '');
    if (params.get(fields._element || '_element') !== 'КнопкаРассчитать' || params.get(fields._event || '_event') !== 'Нажатие') {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ok: true,
        pickerData: {
          columns: [{name: 'Название'}, {name: 'Количество', type: 'number', editable: true}],
          rows: [
            {id: 'highlight-odd', highlight: true, data: {Название: 'Архивная 1', Количество: 1}},
            {id: 'highlight-even', highlight: true, data: {Название: 'Архивная 2', Количество: 2}},
            {id: 'ordinary-odd', data: {Название: 'Обычная 1', Количество: 3}},
            {id: 'ordinary-even', data: {Название: 'Обычная 2', Количество: 4}},
          ],
          config: {width: 960, headerFields: [{name: 'Артикул', title: 'Артикул', type: 'string', value: 'C005'}], ...config},
        },
      }),
    });
  });
  await page.locator('[data-ob-fire-click="КнопкаРассчитать"]').click();
  const modal = page.locator('#_item-picker-modal');
  await expect(modal).toBeVisible();
  await expect(modal.locator(':scope > div')).toHaveCSS('width', '960px');
  await expect(modal.locator('._ip-hf')).toHaveValue('C005');
  return modal;
}

test('подбор сохраняет выделение нечётной и чётной строки при наведении', async ({ page }) => {
  await openHighlightPicker(page);
  const rows = page.locator('#_item-picker-modal tbody tr');
  await expect(rows).toHaveCount(4);
  await page.mouse.move(0, 0);

  async function expectHighlight(row) {
    const cells = row.locator('td');
    await expect(cells).toHaveCount(3);
    for (const cell of await cells.all()) {
      await expect(cell).toHaveCSS('background-color', 'rgb(229, 231, 235)');
    }
  }
  for (const id of ['highlight-even', 'highlight-odd']) {
    const row = page.locator(`#_item-picker-modal tbody tr[data-id="${id}"]`);
    await expectHighlight(row);
    await row.hover();
    await expectHighlight(row);
    await row.locator('input._ip-cb').check();
    await expect(row.locator('input._ip-cb')).toBeChecked();
    await page.mouse.move(0, 0);
  }
  // Ordinary rows still use the standard zebra and hover colors.
  const ordinaryEven = page.locator('#_item-picker-modal tbody tr[data-id="ordinary-even"]');
  await expect(ordinaryEven.locator('td').first()).toHaveCSS('background-color', 'rgb(241, 245, 249)');
  await ordinaryEven.hover();
  await expect(ordinaryEven.locator('td').first()).toHaveCSS('background-color', 'rgb(238, 244, 255)');
});

test('подбор без выбора сохраняет выделение вместе с шириной и полями шапки', async ({page}) => {
  const modal = await openHighlightPicker(page, {noSelect: true});
  await expect(modal.locator('input._ip-cb')).toHaveCount(0);
  await expect(modal.getByRole('button', {name: /^(Выбрать|Перенести в документ)$/})).toHaveCount(0);
  for (const id of ['highlight-even', 'highlight-odd']) {
    const row = modal.locator(`tbody tr[data-id="${id}"]`);
    await expect(row.locator('td')).toHaveCount(2);
    for (const cell of await row.locator('td').all()) {
      await expect(cell).toHaveCSS('background-color', 'rgb(229, 231, 235)');
    }
    await row.hover();
    for (const cell of await row.locator('td').all()) {
      await expect(cell).toHaveCSS('background-color', 'rgb(229, 231, 235)');
    }
  }
  await modal.getByRole('button', {name: 'Закрыть', exact: true}).click();
  await expect(modal).toHaveCount(0);
});
