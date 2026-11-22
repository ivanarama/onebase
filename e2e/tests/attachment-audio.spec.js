// Встроенный проигрыватель аудио-вложений (задание 22.10, п. 156): запись
// разговора слушают прямо в панели вложений, а не скачивают в проигрыватель
// компьютера. Источник — тот же защищённый адрес скачивания: inline, тип
// аудио, по частям (Range); у кого нет права читать запись — 403.

const { test, expect } = require('@playwright/test');
const { login, open, SAVE } = require('./helpers');

const USER = { login: process.env.OB_USER_LOGIN || 'user', password: process.env.OB_USER_PASSWORD || 'Us3r-P@ssw0rd!' };

test('аудио-вложение играет встроенный проигрыватель, права — как у скачивания', async ({ page, browser }) => {
  await login(page);
  // Справочник без права у роли «Кладовщик» — для проверки отказа ниже.
  await open(page, '/ui/catalog/ЗаявкаНаОбработку');
  await page.click('[data-ob-list-create]');
  await page.click(SAVE);
  await expect(page).not.toHaveURL(/\/new/);

  // Запись приходит как application/octet-stream: тип по расширению .ogg
  // знает не всякая системная таблица типов.
  await page.setInputFiles('#att-file-input', {
    name: 'звонок.ogg', mimeType: 'application/octet-stream', buffer: Buffer.from('OggS' + '0'.repeat(60)),
  });
  const player = page.locator('[data-ob-attachments] audio[data-ob-attachment-audio]');
  await expect(player).toHaveCount(1);
  expect(await player.evaluate((el) => el.controls && !el.autoplay && el.preload === 'none')).toBe(true);
  // Кнопка «Скачать» осталась.
  await expect(page.locator('[data-ob-attachments] a[href$="/download"]')).toHaveCount(1);

  const src = await player.evaluate((el) => el.src);
  const full = await page.request.get(src);
  expect(full.status()).toBe(200);
  expect(full.headers()['content-type']).toBe('audio/ogg');
  expect(full.headers()['content-disposition']).toMatch(/^inline/);
  const part = await page.request.get(src, { headers: { Range: 'bytes=0-3' } });
  expect(part.status()).toBe(206);
  expect(await part.text()).toBe('OggS');

  // Пользователь без права на документ получает 403, а не запись.
  const other = await browser.newContext();
  const otherPage = await other.newPage();
  await login(otherPage, USER);
  const denied = await otherPage.request.get(src);
  expect(denied.status()).toBe(403);
  await other.close();
});
