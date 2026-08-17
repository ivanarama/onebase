// Вход и защита от неавторизованного доступа (#791).

const { test, expect } = require('@playwright/test');
const { login, open, ADMIN } = require('./helpers');

test('неавторизованного пользователя не пускают в интерфейс', async ({ page }) => {
  await open(page, '/ui');
  // Проверка имеет смысл только потому, что фикстура завела пользователя: на
  // базе без пользователей аутентификация выключена целиком, и этот тест был
  // бы зелёным, ничего не проверив.
  await expect(page).toHaveURL(/\/login/);
  await expect(page.locator('input[name="password"]')).toBeVisible();
});

test('неверный пароль не пускает и не роняет сервер', async ({ page }) => {
  await open(page, '/login');
  await page.fill('input[name="login"]', ADMIN.login);
  await page.fill('input[name="password"]', 'заведомо-неверный');
  await page.click('form button[type="submit"]');
  // Остаёмся на логине; страница по-прежнему рабочая, а не пятисотка.
  await expect(page).toHaveURL(/\/login/);
  await expect(page.locator('input[name="password"]')).toBeVisible();
});

test('вход и выход работают', async ({ page }) => {
  await login(page);
  await expect(page).toHaveURL(/\/ui/);
  // Признак рабочего интерфейса — навигация по разделам конфигурации.
  await expect(page.locator('[data-navsec="Справочники"]').first()).toBeVisible();

  // Выход лежит в выпадающем меню «Система»: сначала открыть, потом нажать.
  // Цепляемся за data-ob-toggle-target и action формы — подписи переводятся.
  await page.click('[data-ob-toggle-target="sysd"]');
  await page.click('form[action="/logout"] button[type="submit"]');
  await expect(page).toHaveURL(/\/login/);
});
