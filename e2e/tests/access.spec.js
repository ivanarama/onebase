// Отказ по правам (#791).
//
// Проверяется обеими сторонами: обычному пользователю закрыто, администратору
// открыто. Без контрольной половины тест был бы зелёным и от опечатки в адресе
// — «страница не открылась» неотличимо от «доступ закрыт».

const { test, expect } = require('@playwright/test');
const { login, open, ADMIN } = require('./helpers');

const USER = {
  login: process.env.OB_USER_LOGIN || 'user',
  password: process.env.OB_USER_PASSWORD || 'Us3r-P@ssw0rd!',
};

const ADMIN_ONLY = ['/ui/admin/users', '/ui/admin/roles', '/ui/admin/scheduled'];

test('обычному пользователю закрыты административные разделы', async ({ page }) => {
  await login(page, USER);

  for (const path of ADMIN_ONLY) {
    const response = await page.goto(path, { waitUntil: 'domcontentloaded' });
    expect(response.status(), `${path} должен быть закрыт`).toBe(403);
  }
});

test('обычный пользователь при этом работает с данными своей роли', async ({ page }) => {
  await login(page, USER);
  // Отказ должен быть точечным: закрыта администрация, а не приложение целиком.
  // Роль «Кладовщик» даёт Номенклатуру и ПоступлениеТоваров — их и проверяем.
  for (const path of ['/ui/catalog/Номенклатура', '/ui/document/ПоступлениеТоваров']) {
    const response = await page.goto(path, { waitUntil: 'domcontentloaded' });
    expect(response.status(), `${path} входит в роль и должен открываться`).toBe(200);
  }
});

test('данные вне роли пользователю закрыты', async ({ page }) => {
  await login(page, USER);
  // Это важнее отказа в администрировании: проверяется, что права режут доступ
  // к прикладным данным. Заказы в роль «Кладовщик» не входят вовсе, тогда как
  // ПоступлениеТоваров входит — то есть отказ именно по составу роли, а не
  // «пользователю закрыто всё».
  for (const path of ['/ui/document/ЗаказПокупателя', '/ui/document/ЗаказПоставщику']) {
    const response = await page.goto(path, { waitUntil: 'domcontentloaded' });
    expect(response.status(), `${path} вне роли и должен быть закрыт`).toBe(403);
  }
});

test('администратору те же разделы открыты', async ({ page }) => {
  await login(page, ADMIN);

  for (const path of ADMIN_ONLY) {
    const response = await page.goto(path, { waitUntil: 'domcontentloaded' });
    expect(response.status(), `${path} должен быть открыт администратору`).toBe(200);
  }
});
