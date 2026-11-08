const { test, expect } = require('@playwright/test');

for (const [entity, cell, field] of [
  ['Видимая', 0, 'Улица'],
  ['Скрытая', 1, 'Представление'],
]) {
  test(`Add row opens the first visible editor: ${entity}`, async ({ page }) => {
    const errors = [];
    page.on('pageerror', error => errors.push(String(error)));
    await page.goto(`/ui/catalog/${entity}/new`);
    const grid = page.locator('[data-sg-tp="Найдено"]');
    await expect(grid.locator('.slick-row')).toHaveCount(1);
    await page.locator('[data-ob-grid-add="Найдено"]').click();
    await expect(grid.locator('.slick-row')).toHaveCount(2);
    await expect.poll(() => page.evaluate(() => window._obGrids.Найдено.grid.getActiveCell()))
      .toEqual({ row: 1, cell });
    const editor = grid.locator('.slick-cell.editable input');
    await expect(editor).toBeFocused();
    // Real keyboard input must reach the new row. The actual form event
    // commits/synchronizes it before submitting native FormData.
    await page.keyboard.type('new row');
    const committedResponse = page.waitForResponse(r => r.url().includes('/form-event'));
    await page.locator('[data-ob-fire-click="Переключить"]').click();
    expect((await (await committedResponse).json()).ok).toBe(true);
    const rows = await page.evaluate(() => JSON.parse(
      new FormData(document.getElementById('main-form')).get('tp_json.Найдено')));
    expect(rows).toHaveLength(2);
    expect(rows[1][field]).toBe('new row');
    expect(rows[0].Улица).toBe('скрытое значение');
    expect(rows[0].Представление).toBe('исходная строка');

    if (entity === 'Скрытая') {
      // The same selection must follow visibility updates from HTTP/DSL,
      // rather than remembering an initial column index.
      for (const [hidden, nextCell, row] of [[true, 1, 2], [false, 0, 3]]) {
        const response = page.waitForResponse(r => r.url().includes('/form-event'));
        await page.locator('[data-ob-fire-click="Переключить"]').click();
        const result = await (await response).json();
        expect(result.ok).toBe(true);
        expect(result.elementStates.hidden['1.0']).toBe(hidden);
        await expect(grid.locator('.slick-header-column')).toHaveCount(hidden ? 1 : 2);
        await page.locator('[data-ob-grid-add="Найдено"]').click();
        await expect.poll(() => page.evaluate(() => window._obGrids.Найдено.grid.getActiveCell()))
          .toEqual({ row, cell: nextCell });
        await expect(grid.locator('.slick-cell.editable input')).toBeFocused();
        await page.keyboard.type(`new row ${row}`);
      }
    }
    expect(errors).toEqual([]);
  });
}

test('Add row remains safe when every column is hidden', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(String(error)));
  await page.goto('/ui/catalog/ВсеСкрыты/new');
  const add = page.locator('[data-ob-grid-add="Найдено"]');
  await expect.poll(() => page.evaluate(() => window._obGrids?.Найдено?.dataView.getLength()))
    .toBe(1);
  await add.click();
  await expect(add).toBeFocused();
  await expect.poll(() => page.evaluate(() => window._obGrids.Найдено.dataView.getLength())).toBe(2);
  const result = await page.evaluate(() => {
    const state = window._obGrids.Найдено;
    return {
      rows: JSON.parse(new FormData(document.getElementById('main-form')).get('tp_json.Найдено')),
      active: state.grid.getActiveCell(),
      editor: Boolean(state.grid.getCellEditor()),
    };
  });
  // Untouched blank rows are omitted from tp_json; the original row must survive.
  expect(result.rows).toHaveLength(1);
  expect(result.rows[0].Улица).toBe('скрытое значение');
  expect(result.active).toBeNull();
  expect(result.editor).toBe(false);
  expect(errors).toEqual([]);
});
