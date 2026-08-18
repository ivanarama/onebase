// Справочник: создание и изменение элемента (#791).
//
// Путь, которым пользователь пользуется чаще всего: список → создать →
// заполнить → записать → элемент виден в списке → открыть → изменить.

const { test, expect } = require('@playwright/test');
const { login, open, SAVE } = require('./helpers');

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('элемент справочника создаётся и виден в списке', async ({ page }) => {
  const name = `Смоук-товар ${Date.now()}`;

  await open(page, '/ui/catalog/Номенклатура');
  await page.click('[data-ob-list-create]');

  await page.fill('input[name="Наименование"]', name);
  await page.click(SAVE);

  // После записи форма открыта на записанном объекте: у него появился адрес с
  // идентификатором вместо /new.
  await expect(page).not.toHaveURL(/\/new/);
  await expect(page.locator('input[name="Наименование"]')).toHaveValue(name);

  // И элемент действительно попал в список, а не только в форму.
  await open(page, '/ui/catalog/Номенклатура');
  await page.fill('input[name="q"]', name);
  await expect(page.locator('table').getByText(name).first()).toBeVisible();
});

test('изменение элемента сохраняется', async ({ page }) => {
  const name = `Смоук-правка ${Date.now()}`;
  const renamed = `${name} (изменён)`;

  await open(page, '/ui/catalog/Номенклатура');
  await page.click('[data-ob-list-create]');
  await page.fill('input[name="Наименование"]', name);
  await page.click(SAVE);
  const url = page.url();

  await page.fill('input[name="Наименование"]', renamed);
  await page.click(SAVE);

  // Перечитываем страницу: проверяем, что изменение легло в базу, а не осталось
  // в полях формы.
  await open(page, url);
  await expect(page.locator('input[name="Наименование"]')).toHaveValue(renamed);
});
