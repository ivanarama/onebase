// Пустой пункт ссылочного списка подписан по доступности поля (задание
// 22.10). Шаблон подписывал его по состоянию на момент отрисовки: у поля,
// которое открывалось позже (readonly_when по событию формы), так и
// оставалось «—», а у соседних сразу доступных — «— выбрать —».

const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

test('пустой пункт: «—» у запертого поля, «— выбрать —» после разблокировки', async ({ page }) => {
  await login(page);
  await open(page, '/ui/document/ПустойПунктСписка/new');
  const field = page.locator('select[name="Склад"]');
  const empty = field.locator('option[value=""]');
  const stage = page.locator('input[name="Стадия"]');
  async function changeStage(value) {
    await stage.fill(value);
    const [response] = await Promise.all([
      page.waitForResponse((r) => r.url().includes('/form-event') && r.request().method() === 'POST'),
      stage.dispatchEvent('change'),
    ]);
    expect(response.ok()).toBeTruthy();
  }

  // Стадия не «открыто» — поле заперто, пустой пункт «—».
  await expect(field).toBeDisabled();
  await expect(empty).toHaveText('—');

  // Событие формы открыло поле — пустой пункт «— выбрать —».
  await changeStage('открыто');
  await expect(field).toBeEnabled();
  await expect(empty).toHaveText('— выбрать —');

  // Выбор пустого пункта очищает поле.
  const value = await field.locator('option:not([value=""])').first().getAttribute('value');
  await field.selectOption(value);
  await expect(field).toHaveValue(value);
  await field.selectOption('');
  await expect(field).toHaveValue('');

  // Снова заперто — снова «—».
  await changeStage('закрыто');
  await expect(field).toBeDisabled();
  await expect(empty).toHaveText('—');
});
