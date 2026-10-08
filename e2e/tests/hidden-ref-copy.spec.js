const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

// hidden_when leaves reference copies in disabled fieldsets in the DOM.
// Exercise the real delegated button, picker and native FormData with both
// orders: getElementById used to select the first, hidden copy (#1914).
for (const hiddenFirst of [true, false]) {
  test(`подбор ссылки сохраняет видимую копию: скрытая ${hiddenFirst ? 'первая' : 'вторая'}`, async ({ page }) => {
    await login(page);
    await open(page, '/ui/document/Инвентаризация/new');
    const visible = page.locator('select[name="Склад"]');
    const oldID = await visible.locator('option').filter({ hasText: 'Главный склад «Чёрная дыра»' }).getAttribute('value');
    const newID = await visible.locator('option').filter({ hasText: 'Склад уценёнки «Хламоприёмник»' }).getAttribute('value');
    expect(oldID).toBeTruthy();
    expect(newID).toBeTruthy();
    await visible.selectOption(oldID);
    await visible.evaluate((select, before) => {
      const row = select.parentElement;
      row.setAttribute('data-test-visible-ref', '1');
      const hidden = document.createElement('fieldset');
      hidden.disabled = true;
      hidden.style.display = 'none';
      hidden.setAttribute('data-ob-control-fieldset', '1');
      hidden.setAttribute('data-test-hidden-ref', '1');
      hidden.appendChild(row.cloneNode(true));
      hidden.querySelector('select').value = select.value;
      hidden.querySelector('[data-test-visible-ref]').removeAttribute('data-test-visible-ref');
      row.parentElement.insertBefore(hidden, before ? row : row.nextSibling);
    }, hiddenFirst);

    const row = page.locator('[data-test-visible-ref]');
    await row.locator('[data-ob-ref-picker]').click();
    await page.locator('#_rp-list ._rp-item').filter({ hasText: 'Склад уценёнки «Хламоприёмник»' }).click();
    await expect(page.locator('#_ref-picker-modal')).toHaveCount(0);
    await expect(row.locator('select')).toHaveValue(newID);
    await expect(page.locator('[data-test-hidden-ref] select')).toHaveValue(oldID);
    expect(await page.locator('#main-form').evaluate((form) => new FormData(form).getAll('Склад'))).toEqual([newID]);

    // The current-card button shares the same select resolver.
    const opened = page.context().waitForEvent('page');
    await row.locator('[data-ob-ref-current]').click();
    const card = await opened;
    await expect(card).toHaveURL(new RegExp(newID));
    await card.close();
  });
}
