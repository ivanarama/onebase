// Вложения: загрузка и скачивание (#791).
//
// Единственный сценарий набора, который трогает файловую систему и отдачу
// бинарного содержимого. Ломается он тихо: интерфейс выглядит рабочим, а файл
// не сохраняется или скачивается пустым.

const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('файл прикрепляется к документу и скачивается обратно', async ({ page }) => {
  await open(page, '/ui/document/ПоступлениеТоваров');
  await page.locator('table a[href*="/ui/document/"]').first().click();

  const content = `смоук-вложение ${Date.now()}`;
  const fileName = 'smoke-attachment.txt';

  // Поле скрыто (его открывает кнопка), поэтому файл кладём прямо в input —
  // так делает и настоящий диалог выбора файла.
  await page.setInputFiles('#att-file-input', {
    name: fileName,
    mimeType: 'text/plain',
    buffer: Buffer.from(content, 'utf8'),
  });

  // Список вложений подтягивается скриптом — ждём появления имени файла.
  await expect(page.locator('#att-list')).toContainText(fileName, { timeout: 15_000 });

  // И проверяем, что скачивается именно то, что положили: список, показывающий
  // имя файла, ещё ничего не говорит о его содержимом.
  const link = page.locator('#att-list a[href*="/download"]').first();
  await expect(link).toBeVisible();
  const href = await link.getAttribute('href');

  const response = await page.request.get(href);
  expect(response.status()).toBe(200);
  expect(await response.text()).toBe(content);
});
