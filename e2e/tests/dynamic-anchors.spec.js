// Динамические якоря управляемой формы (#1587).
//
// Проверка идёт по пользовательскому пути в настоящем браузере: изменение поля
// дёргает ПриИзменении, сервер пересчитывает hidden_when/readonly_when, и
// клиент применяет готовые состояния к живой форме. Настоящий DOM вместо
// самописной заглушки удалённого managed_dynamic_anchor_behavior_test.js:
// селекторы не ограничены подмножеством, а поведение — то, что отрисует
// Chromium. Серверную отрисовку якорей продолжает сторожить Go-тест
// TestManagedDynamicAnchorsRenderThroughPublicForm.
//
// Фикстура — справочник ЗаявкаНаОбработку в examples/trade: по условию
// «Стадия = Принята» сервер скрывает надпись, картинки и флажок
// (hidden_when) и гасит командную панель (readonly_when).

const { test, expect } = require('@playwright/test');
const { login, open, SAVE } = require('./helpers');

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('hidden_when и readonly_when применяются после события формы', async ({ page }) => {
  await open(page, '/ui/catalog/ЗаявкаНаОбработку');
  await page.click('[data-ob-list-create]');
  // Пустой черновик: у управляемой формы фикстуры нет обязательных полей, а
  // Стадию оставляем пустой, чтобы первое ПриИзменении не случилось на
  // несохранённой записи.
  await page.click(SAVE);
  await expect(page).not.toHaveURL(/\/new/);

  // Черновик: украшения отрисованы, командная панель живая. Флажок и панель
  // держат раскладку инлайновым display:flex — клиент обязан сохранять его
  // при откате условия, а не затирать пустой строкой.
  const label = page.locator('[data-ob-el="НадписьСтатуса"]');
  const picture = page.locator('[data-ob-el="КартинкаБезФайла"]');
  const checkbox = page.locator('[data-ob-el="ФлажокСрочно"]');
  const panelButton = page.locator('[data-ob-el="ПанельКоманд"] button').first();
  await expect(label).toBeVisible();
  await expect(picture).toBeVisible();
  await expect(checkbox).toBeVisible();
  await expect(checkbox).toHaveAttribute('style', /display:\s*flex/);
  await expect(panelButton).toBeEnabled();

  // Стадия = «Принята» + ПриИзменении: сервер пересчитал состояния, клиент
  // скрыл украшения и погасил кнопки панели без перезагрузки страницы.
  const stage = page.locator('input[name="Стадия"]');
  await stage.fill('Принята');
  await stage.dispatchEvent('change');
  await expect(label).toBeHidden();
  await expect(picture).toBeHidden();
  await expect(checkbox).toBeHidden();
  await expect(panelButton).toBeDisabled();

  // Обратное условие приезжает той же картой состояний: false снимает
  // скрытие, возвращает инлайновый display:flex и отпирает панель.
  await stage.fill('Черновик');
  await stage.dispatchEvent('change');
  await expect(label).toBeVisible();
  await expect(checkbox).toBeVisible();
  await expect(checkbox).toHaveAttribute('style', /display:\s*flex/);
  await expect(panelButton).toBeEnabled();
});
