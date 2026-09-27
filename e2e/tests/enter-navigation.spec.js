// Реальные DOM-события и vendored SlickGrid: подмена navigate/commit не ловит
// гонку capture-обработчика с редактором ссылки и implicit submit браузера.
const { test, expect } = require('@playwright/test');
const path = require('node:path');

const root = path.resolve(__dirname, '../..');

async function formFixture(page, { legacy = false, hiddenGrid = false, readonlyPlacement = false,
  domTable = false, fieldAfterDOMTable = false } = {}) {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.route('**/__enter_navigation_fixture', route => route.fulfill({
    contentType: 'text/html',
    body: '<!doctype html><html><head><meta charset="utf-8"></head><body></body></html>',
  }));
  await page.goto('/__enter_navigation_fixture');
  const columns = JSON.stringify([
    { id: 'Ref', name: 'Reference', type: 'reference', ref: 'Items' },
    { id: 'Text', name: 'Text', type: 'string' },
  ]);
  const rows = JSON.stringify([{ Ref: 'alpha', Text: 'initial' }, { Ref: 'alpha', Text: 'second' }]);
  const refs = JSON.stringify({ Ref: [{ id: 'alpha', _label: 'Alpha' }, { id: 'beta', _label: 'Beta' }] });
  const host = (id, readonly = false) => `<div id="${id}" class="ob-grid" data-sg-tp="Rows"
    ${readonly ? 'data-sg-ro="1"' : ''} style="width:600px;height:200px"
    data-sg-cols='${columns}' data-sg-rows='${rows}' data-sg-ref='${refs}'></div>`;
  const plainTable = `<table id="plain-table" data-ob-dom-table="PlainRows" data-ob-readonly="0"><tbody>
    ${[0, 1].map(row => `<tr>
      <td><input id="plain-${row}-first" name="tp.PlainRows.${row}.Text"></td>
      <td style="display:none"><input name="tp.PlainRows.${row}.Hidden"></td>
      <td><input readonly value="fixed"><input disabled><input tabindex="-1"></td>
      <td><input id="plain-${row}-last" type="number" name="tp.PlainRows.${row}.Amount"></td>
    </tr>`).join('')}
  </tbody></table>`;
  await page.setContent(`<form id="main-form" data-ob-grid-sync ${legacy ? 'data-ob-enter-submits="1"' : ''}>
    <input id="first"><input id="checkbox" type="checkbox"><input id="radio" type="radio">
    <input id="readonly" readonly value="fixed"><input id="last-header">
    ${domTable ? plainTable : ''}
    ${fieldAfterDOMTable ? '<input id="after-plain-table">' : ''}
    ${readonlyPlacement ? host('summary', true) : ''}
    <div ${hiddenGrid ? 'style="display:none"' : ''}>${host('grid')}</div>
    <input type="hidden" name="tp_json.Rows" id="tp-json-Rows">
    <input id="after"><textarea id="area"></textarea><button id="save" type="submit">Save</button>
  </form>`);
  await page.evaluate(() => {
    window.testSubmits = 0;
    document.getElementById('main-form').addEventListener('submit', event => {
      window.testSubmits++;
      event.preventDefault();
    });
  });
  await page.route('**/ui/_ref-options/Items?*', route => route.fulfill({ json: [] }));
  await page.addStyleTag({ path: path.join(root, 'internal/webassets/slickgrid/slick.grid.css') });
  await page.addScriptTag({ path: path.join(root, 'internal/ui/static/ui.js') });
  for (const script of ['core', 'interactions', 'grid', 'dataview', 'editors', 'formatters']) {
    await page.addScriptTag({ path: path.join(root, `internal/webassets/slickgrid/slick.${script}.js`) });
  }
  await page.addScriptTag({ path: path.join(root, 'internal/ui/static/managed.js') });
  await expect.poll(() => page.evaluate(() => !!window._obGrids.Rows)).toBe(true);
  expect(errors).toEqual([]);
}

async function gridState(page) {
  return page.evaluate(() => {
    const { grid, dataView } = window._obGrids.Rows;
    return { active: grid.getActiveCell(), editing: !!grid.getCellEditor(),
      ref: dataView.getItem(0).Ref, text: dataView.getItem(0).Text, rows: dataView.getLength() };
  });
}

test('Enter выбирает подсказку ссылки до коммита и навигации', async ({ page }) => {
  await formFixture(page);
  await page.locator('#grid .slick-row').first().locator('.slick-cell').nth(0).dblclick();
  const editor = page.locator('#grid input');
  await editor.fill('');
  await editor.press('ArrowDown');
  await editor.press('ArrowDown');
  await editor.press('Enter');
  await expect(editor).toHaveValue('Beta');
  expect(await gridState(page)).toMatchObject({ active: { row: 0, cell: 0 }, editing: true, ref: 'alpha' });
  await editor.press('Enter');
  expect(await gridState(page)).toMatchObject({ active: { row: 0, cell: 1 }, editing: false, ref: 'beta' });
  expect(await page.evaluate(() => window.testSubmits)).toBe(0);
});

for (const field of ['checkbox', 'radio', 'readonly']) {
  test(`Enter на ${field} не отправляет форму`, async ({ page }) => {
    await formFixture(page);
    await page.locator(`#${field}`).focus();
    await page.keyboard.press('Enter');
    expect(await page.evaluate(() => window.testSubmits)).toBe(0);
    await expect(page.locator(field === 'checkbox' ? '#radio' : '#last-header')).toBeFocused();
  });
}

test('переход из текстового поля на флажок не создаёт путь к неявной записи', async ({ page }) => {
  await formFixture(page);
  await page.locator('#first').press('Enter');
  await expect(page.locator('#checkbox')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('#radio')).toBeFocused();
  expect(await page.evaluate(() => window.testSubmits)).toBe(0);
  await expect(page.locator('#checkbox')).not.toBeChecked();
});

for (const keyCode of [229, 13]) {
  test(`IME Enter (${keyCode}) сохраняет открытый редактор и значение`, async ({ page }) => {
    await formFixture(page);
    await page.locator('#grid .slick-row').first().locator('.slick-cell').nth(1).dblclick();
    const editor = page.locator('#grid input');
    await editor.fill('未確定');
    const prevented = await editor.evaluate((input, code) => {
      const event = new KeyboardEvent('keydown', { key: 'Enter', keyCode: code, which: code,
        isComposing: true, bubbles: true, cancelable: true });
      input.dispatchEvent(event);
      return event.defaultPrevented;
    }, keyCode);
    expect(prevented).toBe(false);
    await expect(editor).toHaveValue('未確定');
    await expect(editor).toBeFocused();
    expect(await gridState(page)).toMatchObject({ active: { row: 0, cell: 1 }, editing: true, text: 'initial' });
  });
}

test('флаг совместимости возвращает вход в редактор и коммит без перехода', async ({ page }) => {
  await formFixture(page, { legacy: true });
  await page.locator('#grid .slick-row').first().locator('.slick-cell').nth(1).click();
  await page.keyboard.press('Enter');
  const editor = page.locator('#grid input');
  await expect(editor).toBeFocused();
  await editor.fill('changed');
  await editor.press('Enter');
  expect(await gridState(page)).toMatchObject({ active: { row: 0, cell: 1 }, editing: false, text: 'changed' });
  await page.locator('#first').press('Enter');
  expect(await page.evaluate(() => window.testSubmits)).toBe(1);
});

test('скрытая табличная часть пропускается при переходе из шапки', async ({ page }) => {
  await formFixture(page, { hiddenGrid: true });
  await page.locator('#last-header').press('Enter');
  await expect(page.locator('#after')).toBeFocused();
  expect((await gridState(page)).active).toBeNull();
  expect(await page.evaluate(() => window.testSubmits)).toBe(0);
});

test('readonly-представление не перенаправляет фокус в скрытый writable host той же ТЧ', async ({ page }) => {
  await formFixture(page, { hiddenGrid: true, readonlyPlacement: true });
  await page.locator('#last-header').press('Enter');
  await expect(page.locator('#after')).toBeFocused();
  expect((await gridState(page)).active).toBeNull();
});

test('вход в видимую ТЧ, переход между строками и конец таблицы', async ({ page }) => {
  await formFixture(page);
  await page.locator('#last-header').press('Enter');
  expect((await gridState(page)).active).toEqual({ row: 0, cell: 0 });
  await page.keyboard.press('Enter');
  expect((await gridState(page)).active).toEqual({ row: 0, cell: 1 });
  await page.keyboard.press('Enter');
  expect((await gridState(page)).active).toEqual({ row: 1, cell: 0 });
  await page.keyboard.press('Enter');
  await page.keyboard.press('Enter');
  expect(await gridState(page)).toMatchObject({ active: { row: 1, cell: 1 }, rows: 2 });
  expect(await page.evaluate(() => window.testSubmits)).toBe(0);
});

test('вход из шапки коммитит незавершённую правку ячейки до смены активной ячейки', async ({ page }) => {
  await formFixture(page);
  await page.locator('#grid .slick-row').first().locator('.slick-cell').nth(1).dblclick();
  const editor = page.locator('#grid input');
  await editor.fill('draft from keyboard');
  await page.locator('#last-header').click();
  await expect(editor).toHaveValue('draft from keyboard');
  expect(await gridState(page)).toMatchObject({ editing: true, text: 'initial' });
  await page.keyboard.press('Enter');
  expect(await gridState(page)).toMatchObject({ active: { row: 0, cell: 0 }, editing: false,
    text: 'draft from keyboard' });
  expect(await page.evaluate(() => window.testSubmits)).toBe(0);
  await page.locator('#save').click();
  const rows = JSON.parse(await page.locator('#tp-json-Rows').inputValue());
  expect(rows[0].Text).toBe('draft from keyboard');
  expect(await page.evaluate(() => window.testSubmits)).toBe(1);
});

test('вход из шапки сохраняет невалидную ссылку и возвращает фокус в редактор', async ({ page }) => {
  await formFixture(page);
  await page.locator('#grid .slick-row').first().locator('.slick-cell').nth(0).dblclick();
  const editor = page.locator('#grid input');
  await editor.fill('unknown reference');
  await page.locator('#last-header').click();
  await expect(editor).toHaveValue('unknown reference');
  await page.keyboard.press('Enter');
  expect(await gridState(page)).toMatchObject({ active: { row: 0, cell: 0 }, editing: true, ref: 'alpha' });
  await expect(editor).toHaveValue('unknown reference');
  await expect(editor).toBeFocused();
  await expect(page.locator('#grid .slick-cell.invalid')).toHaveCount(1);
  expect(await page.evaluate(() => window.testSubmits)).toBe(0);
});

for (const fieldAfterDOMTable of [true, false]) {
  test(`Enter в no_grid остаётся в таблице перед ${fieldAfterDOMTable ? 'полем' : 'следующей ТЧ'}`, async ({ page }) => {
    await formFixture(page, { domTable: true, fieldAfterDOMTable });
    await page.locator('#last-header').press('Enter');
    await expect(page.locator('#plain-0-first')).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.locator('#plain-0-last')).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.locator('#plain-1-first')).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.locator('#plain-1-last')).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.locator('#plain-1-last')).toBeFocused();
    await expect(page.locator('#plain-table tbody tr')).toHaveCount(2);
    expect((await gridState(page)).active).toBeNull();
    expect(await page.evaluate(() => window.testSubmits)).toBe(0);
  });
}

test('textarea и явная кнопка записи сохраняют назначение Enter', async ({ page }) => {
  await formFixture(page);
  await page.locator('#area').press('Enter');
  await expect(page.locator('#area')).toHaveValue('\n');
  expect(await page.evaluate(() => window.testSubmits)).toBe(0);
  await page.locator('#save').press('Enter');
  expect(await page.evaluate(() => window.testSubmits)).toBe(1);
});
