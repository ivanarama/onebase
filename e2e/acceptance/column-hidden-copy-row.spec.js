const { test, expect } = require('@playwright/test');

async function sendForm(page) {
  const request = page.waitForRequest(r => r.url().includes('/form-event'));
  const response = page.waitForResponse(r => r.url().includes('/form-event'));
  await page.locator('[data-ob-fire-click="Переключить"]').click();
  const body = new URLSearchParams((await request).postData());
  expect((await (await response).json()).ok).toBe(true);
  return JSON.parse(body.get('tp_json.Найдено'));
}

async function selectSource(page, grid) {
  await grid.getByText('исходная строка', { exact: true }).first().dblclick();
  await page.keyboard.press('Escape');
}

for (const [entity, initialCell, initialField] of [
  ['Видимая', 0, 'Улица'],
  ['Скрытая', 1, 'Представление'],
]) {
  test(`F9 opens the first available editor and submits the copy: ${entity}`, async ({ page }) => {
    const errors = [];
    page.on('pageerror', error => errors.push(String(error)));
    await page.goto(`/ui/catalog/${entity}/new`);
    const grid = page.locator('[data-sg-tp="Найдено"]');
    await expect(grid.locator('.slick-row')).toHaveCount(1);
    const states = entity === 'Скрытая'
      ? [[initialCell, initialField], [0, 'Улица'], [1, 'Представление']]
      : [[initialCell, initialField]];
    for (const [index, [cell, field]] of states.entries()) {
      await selectSource(page, grid);
      await page.keyboard.press('F9');
      await expect(grid.locator('.slick-row')).toHaveCount(index + 2);
      await expect.poll(() => page.evaluate(() => window._obGrids.Найдено.grid.getActiveCell()))
        .toEqual({ row: 1, cell });
      await expect(grid.locator('.slick-cell.editable input')).toBeFocused();
      await page.keyboard.type(`copy ${index}`);
      const rows = await sendForm(page);
      expect(rows).toHaveLength(index + 2);
      expect(rows[0].Улица).toBe('скрытое значение');
      expect(rows[0].Представление).toBe('исходная строка');
      expect(rows[1][field]).toBe(`copy ${index}`);
      const unchangedField = field === 'Улица' ? 'Представление' : 'Улица';
      expect(rows[1][unchangedField]).toBe(rows[0][unchangedField]);
      for (let previous = 0; previous < index; previous++) {
        expect(rows[index - previous + 1][states[previous][1]]).toBe(`copy ${previous}`);
      }
    }
    expect(errors).toEqual([]);
  });
}

test('F9 copies columns without editors safely', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(String(error)));
  await page.goto('/ui/catalog/НетРедакторов/new');
  const grid = page.locator('[data-sg-tp="Найдено"]');
  await expect(grid.locator('.slick-row')).toHaveCount(1);
  // Use the native public SlickGrid column API to exercise an embedding
  // without any cell editors; the product's focus/edit checks stay intact.
  await page.evaluate(() => {
    const grid = window._obGrids.Найдено.grid;
    const columns = grid.getColumns();
    for (const column of columns) column.editor = null;
    grid.setColumns(columns);
  });
  await grid.getByText('исходная строка', { exact: true }).click();
  await page.keyboard.press('F9');
  await expect(grid.locator('.slick-row')).toHaveCount(2);
  expect(await page.evaluate(() => Boolean(window._obGrids.Найдено.grid.getCellEditor()))).toBe(false);
  const rows = await sendForm(page);
  expect(rows).toHaveLength(2);
  expect(rows[1]).toEqual(rows[0]);
  expect(rows[0].Улица).toBe('скрытое значение');
  expect(rows[0].Представление).toBe('исходная строка');
  expect(errors).toEqual([]);
});

test('F9 preserves a readonly table', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(String(error)));
  await page.goto('/ui/catalog/ТолькоЧтение/new');
  const grid = page.locator('[data-sg-tp="Найдено"]');
  await grid.getByText('исходная строка', { exact: true }).click();
  await page.keyboard.press('F9');
  await expect(grid.locator('.slick-row')).toHaveCount(1);
  expect(await page.evaluate(() => Boolean(window._obGrids.Найдено.grid.getCellEditor()))).toBe(false);
  expect(errors).toEqual([]);
});
