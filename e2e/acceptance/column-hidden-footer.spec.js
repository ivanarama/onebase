const { test, expect } = require('@playwright/test');

for (const entity of ['Число', 'Текст']) {
  test(`hidden ${entity} column preserves the visible footer total`, async ({ page }) => {
    const errors = [];
    page.on('pageerror', error => errors.push(String(error)));
    await page.goto(`/ui/catalog/${entity}/new`);
    const grid = page.locator('[data-sg-tp="Найдено"]');
    const headers = grid.locator('.slick-header-column');
    const footer = grid.locator('.slick-footerrow-column');
    await expect(headers).toHaveCount(3);
    await expect(footer).toHaveCount(3);
    await expect(footer.nth(2)).toHaveText('11');
    await expect(footer.nth(1)).toHaveText(entity === 'Число' ? '7' : '');

    for (let round = 0; round < 2; round++) {
      const hiddenResponse = page.waitForResponse(response => response.url().includes('/form-event'));
      await page.locator('[data-ob-fire-click="Переключить"]').click();
      const hidden = await (await hiddenResponse).json();
      expect(hidden.ok).toBe(true);
      expect(hidden.elementStates.hidden['1.1']).toBe(true);
      await expect(headers).toHaveCount(2);
      await expect(headers.nth(1)).toContainText('Сумма');
      await expect(footer).toHaveCount(2);
      await expect(footer.nth(0)).toHaveText('Итого');
      await expect(footer.nth(1)).toHaveText('11');
      // Hidden cells remain in the actual submission, with their original values.
      const rows = await page.evaluate(() => {
        const data = new FormData(document.getElementById('main-form'));
        return JSON.parse(data.get('tp_json.Найдено'));
      });
      expect(rows).toHaveLength(1);
      expect(Number(rows[0].Представление)).toBe(7);
      expect(Number(rows[0].Сумма)).toBe(11);

      const shownResponse = page.waitForResponse(response => response.url().includes('/form-event'));
      await page.locator('[data-ob-fire-click="Переключить"]').click();
      const shown = await (await shownResponse).json();
      expect(shown.ok).toBe(true);
      expect(shown.elementStates.hidden['1.1']).toBe(false);
      await expect(headers).toHaveCount(3);
      await expect(footer).toHaveCount(3);
      await expect(footer.nth(1)).toHaveText(entity === 'Число' ? '7' : '');
      await expect(footer.nth(2)).toHaveText('11');
    }
    expect(errors).toEqual([]);
  });
}
