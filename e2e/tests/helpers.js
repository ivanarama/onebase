// Общие шаги браузерных сценариев (#791).
//
// Селекторы намеренно цепляются за атрибуты (name, data-ob-*), а не за текст
// кнопок: тексты переводятся (#960), и селектор по слову «Записать» отвалился
// бы при смене языка интерфейса — тест сломался бы там, где приложение
// работает.

const ADMIN = { login: process.env.OB_ADMIN_LOGIN || 'admin', password: process.env.OB_ADMIN_PASSWORD || 'Sm0ke-P@ssw0rd!' };

// goto с domcontentloaded: страница регистрирует service worker и PWA-манифест,
// поэтому ожидание полной загрузки («load») на ней зависает.
async function open(page, path) {
  await page.goto(path, { waitUntil: 'domcontentloaded' });
}

async function login(page, user = ADMIN) {
  await open(page, '/login');
  await page.fill('input[name="login"]', user.login);
  await page.fill('input[name="password"]', user.password);
  await Promise.all([
    page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 15_000 }),
    page.click('form button[type="submit"]'),
  ]);
}

// Кнопки формы объекта различаются значением _action, а не подписью:
// «» — записать, «post» — провести, «post_and_close» — провести и закрыть.
const SAVE = 'button[name="_action"][value=""]';
const POST = 'button[name="_action"][value="post"]';

module.exports = { ADMIN, open, login, SAVE, POST };
