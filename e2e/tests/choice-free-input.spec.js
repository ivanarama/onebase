const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

// choice_free_input: строка ввода со своим списком вариантов. Выбор мышью,
// выбор с клавиатуры и вписанный текст одинаково доходят до ПриИзменении.
test('choice free input: pick, keyboard and own text', async ({ page }) => {
  await login(page);
  await open(page, '/ui/document/СписокСВводом/new');
  const input = page.locator('[data-ob-el="ПолеВремя"] input[name="ВремяВизита"]');
  const list = page.locator('[data-ob-el="ПолеВремя"] .ob-free-choice-list');
  const mark = page.locator('[data-ob-el="ПолеОтметка"] input');
  await expect(input).toHaveAttribute('autocomplete', 'off');
  await expect(list).toBeHidden();

  await page.locator('[data-ob-el="ПолеВремя"] .ob-free-choice-btn').click();
  await expect(list).toBeVisible();
  await list.getByText('14-20', { exact: true }).click();
  await expect(list).toBeHidden();
  await expect(input).toHaveValue('14-20');
  await expect(mark).toHaveValue('изменено: 14-20');

  await input.focus();
  await page.keyboard.press('ArrowDown');
  await expect(list).toBeVisible();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(input).toHaveValue('ЗМ в течение часа');
  await expect(mark).toHaveValue('изменено: ЗМ в течение часа');

  await page.keyboard.press('ArrowDown');
  await expect(list).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(list).toBeHidden();

  await input.fill('после 18:00');
  await page.keyboard.press('Tab');
  await expect(input).toHaveValue('после 18:00');
  await expect(mark).toHaveValue('изменено: после 18:00');
});
