// Проведение документа и движения регистра (#791).
//
// Самый ценный сценарий набора: проведение — это то, ради чего платформа
// существует, и оно проходит через DSL-модуль проведения, коллектор движений,
// запись в регистр и пересчёт итогов. Ни один из этих слоёв браузерные
// проверки раньше не трогали.

const { test, expect } = require('@playwright/test');
const { login, open, SAVE, POST } = require('./helpers');

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('документ проводится, движения попадают в регистр', async ({ page }) => {
  await open(page, '/ui/document/ПоступлениеТоваров');

  // Копия берёт все ссылки и табличную часть из демо-данных, но
  // сохраняется как новый непроведённый документ. Так предусловие теста не
  // зависит от состояния исходных демо-документов.
  const source = page.locator('[data-ob-list-row][data-copy-url]').first();
  await expect(source).toBeVisible();
  const copyURL = await source.getAttribute('data-copy-url');
  expect(copyURL).toBeTruthy();
  await open(page, copyURL);
  await page.click(SAVE);
  await expect(page).not.toHaveURL(/\/new/);

  const number = await page.locator('input[name="Номер"]').inputValue();
  expect(number).not.toEqual('');
  const documentURL = page.url();

  // До нажатия кнопки фиксируем оба предусловия: флаг проведения снят и
  // движений с новым номером ещё нет.
  await expect(page.getByText('Не проведён', { exact: true })).toBeVisible();
  await open(page, '/ui/register/ОстаткиТоваров');
  await expect(page.locator('table').getByText(number, { exact: false })).toHaveCount(0);

  await open(page, documentURL);
  await expect(page.getByText('Не проведён', { exact: true })).toBeVisible();

  await page.click(POST);

  // После действия доказываем именно переход, а не просто отсутствие ошибки:
  // тот же документ стал проведённым, а его движения появились в регистре.
  await expect(page.locator('input[name="Номер"]')).toHaveValue(number);
  await expect(page.getByText('✓ Проведён', { exact: true })).toBeVisible();

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
