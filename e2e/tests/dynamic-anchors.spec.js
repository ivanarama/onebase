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
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
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
  const pictures = [
    page.locator('[data-ob-el="КартинкаСФайлом"]'),
    page.locator('[data-ob-el="КартинкаБезФайла"]'),
  ];
  const checkbox = page.locator('[data-ob-el="ФлажокСрочно"]');
  const panel = page.locator('[data-ob-el="ПанельКоманд"]');
  const panelButtons = panel.locator('button');
  expect(await panelButtons.count()).toBeGreaterThan(0);
  async function expectDraft() {
    for (const decoration of [label, ...pictures, checkbox]) {
      await expect(decoration).toBeVisible();
    }
    await expect(panel).toBeVisible();
    for (const anchor of [checkbox, panel]) {
      await expect.poll(() => anchor.evaluate((element) => element.style.display)).toBe('flex');
    }
    for (const button of await panelButtons.all()) await expect(button).toBeEnabled();
  }
  await expectDraft();

  // Первый ответ содержит false: он тоже должен сохранить inline display.
  // Уникальное свойство window доказывает отсутствие перезагрузки документа.
  const documentToken = `anchors-${Date.now()}`;
  await page.evaluate((token) => { window.dynamicAnchorDocumentToken = token; }, documentToken);
  const stage = page.locator('input[name="Стадия"]');
  async function changeStage(value) {
    await stage.fill(value);
    const [response] = await Promise.all([
      page.waitForResponse((response) => response.url().includes('/form-event') && response.request().method() === 'POST'),
      stage.dispatchEvent('change'),
    ]);
    expect(response.ok()).toBeTruthy();
    await expect.poll(() => page.evaluate(() => window.dynamicAnchorDocumentToken)).toBe(documentToken);
  }
  await changeStage('Черновик');
  await expectDraft();

  // Стадия = «Принята» + ПриИзменении: сервер пересчитал состояния, клиент
  // скрыл украшения и погасил кнопки панели без перезагрузки страницы.
  await changeStage('Принята');
  for (const decoration of [label, ...pictures, checkbox]) {
    await expect(decoration).toBeHidden();
  }
  await expect(panel).toBeVisible();
  for (const button of await panelButtons.all()) await expect(button).toBeDisabled();

  // Обратное условие приезжает той же картой состояний: false снимает
  // скрытие, возвращает инлайновый display:flex и отпирает панель.
  await changeStage('Черновик');
  await expectDraft();

  // Отдельное скрытие панели проверяет восстановление её display:flex.
  await changeStage('Скрыта');
  await expect(panel).toBeHidden();
  await changeStage('Черновик');
  await expectDraft();
  expect(pageErrors).toEqual([]);
});
