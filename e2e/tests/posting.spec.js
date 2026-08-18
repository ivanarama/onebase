// Проведение документа и движения регистра (#791).
//
// Самый ценный сценарий набора: проведение — это то, ради чего платформа
// существует, и оно проходит через DSL-модуль проведения, коллектор движений,
// запись в регистр и пересчёт итогов. Ни один из этих слоёв браузерные
// проверки раньше не трогали.

const { test, expect } = require('@playwright/test');
const { login, open, POST } = require('./helpers');

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('документ проводится, движения попадают в регистр', async ({ page }) => {
  await open(page, '/ui/document/ПоступлениеТоваров');

  // Берём документ из демо-данных: собрать его с нуля через UI — это отдельный
  // длинный сценарий (контрагент, склад, табличная часть), а проверяем мы здесь
  // проведение, а не заполнение.
  const first = page.locator('table a[href*="/ui/document/"]').first();
  await expect(first).toBeVisible();
  await first.click();

  const number = await page.locator('input[name="Номер"]').inputValue();
  expect(number).not.toEqual('');

  await page.click(POST);

  // Проведение не должно оборваться ошибкой: страница осталась формой документа.
  await expect(page.locator('input[name="Номер"]')).toHaveValue(number);

  // И главное — движения действительно появились в регистре.
  await open(page, '/ui/register/ОстаткиТоваров');
  await expect(page.locator('table').getByText(number, { exact: false }).first()).toBeVisible();
});

test('в регистре остатков есть данные после проведения демо-документов', async ({ page }) => {
  await open(page, '/ui/register/ОстаткиТоваров');
  // Пустой регистр означал бы, что проведение демо-базы молча не сработало, —
  // именно так выглядела бы поломка коллектора движений.
  const rows = page.locator('table tbody tr');
  await expect(rows.first()).toBeVisible();
  expect(await rows.count()).toBeGreaterThan(0);
});
