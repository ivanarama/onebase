const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

// В оболочке вкладок ни одна ссылка верхней панели, ни поиск, ни «Назад» не
// уводят саму страницу: уход выгрузил бы все вкладки, и у формы с правками
// Chrome спрашивал «Закрыть приложение?» (окно --app у оператора КЦ).
// beforeunload остаётся только на настоящее закрытие окна и выход.

const user = { login: process.env.OB_USER_LOGIN || 'user', password: process.env.OB_USER_PASSWORD || 'Us3r-P@ssw0rd!' };

async function shellWithDraft(page, dialogs) {
  page.on('dialog', d => { dialogs.push(d.type()); d.dismiss().catch(() => {}); });
  await login(page, user);
  await open(page, '/ui/app?home=1');
  await page.evaluate(() => {
    window.shellRun = true;
    window.obOpenTab('/ui/catalog/Номенклатура/new', 'Draft');
  });
  const field = page.frameLocator('#ob-tabbody iframe').locator('input[name="Наименование"]');
  await field.fill('Unsaved draft');
  await field.press('Tab');
  await expect(page.locator('.ob-tab.dirty')).toHaveCount(1);
}

async function expectSameShell(page, dialogs, what) {
  await page.waitForTimeout(300);
  expect(new URL(page.url()).pathname, what).toBe('/ui/app');
  expect(await page.evaluate(() => window.shellRun), what + ': страница оболочки сменилась').toBe(true);
  expect(dialogs, what + ': показан диалог').toEqual([]);
}

test('ссылки верхней панели открываются в оболочке, а не уводят окно', async ({ page }) => {
  const dialogs = [];
  await shellWithDraft(page, dialogs);
  const links = await page.$$eval('.topbar a[href]', as => as
    .filter(a => !a.target || a.target === '_self')
    .map(a => a.getAttribute('href')));
  expect(links.length).toBeGreaterThan(2);
  for (const href of links) {
    // Ссылки меню «Система» свёрнуты; a.click() даёт тот же обработчик щелчка
    // оболочки, а неперехваченный щелчок уводит окно — это и проверяется.
    await page.locator('.topbar a[href="' + href + '"]').first().evaluate(a => a.click());
    await expectSameShell(page, dialogs, href);
  }
  // Логотип — раздел на месте, вкладка с правкой жива.
  await expect(page.locator('.ob-tab.dirty')).toHaveCount(1);
});

test('«Поиск по базе» открывает результаты во вкладке', async ({ page }) => {
  const dialogs = [];
  await shellWithDraft(page, dialogs);
  await page.fill('.topbar-search input[name="q"]', 'Draft');
  await page.press('.topbar-search input[name="q"]', 'Enter');
  await expectSameShell(page, dialogs, 'поиск');
  await expect(page.locator('#ob-tabbody iframe[src*="/ui/search?q="]')).toHaveCount(1);
});

test('«Назад» не уводит со страницы оболочки', async ({ page }) => {
  const dialogs = [];
  await shellWithDraft(page, dialogs);
  for (let i = 0; i < 3; i++) {
    await page.evaluate(() => history.back());
    await expectSameShell(page, dialogs, 'history.back №' + (i + 1));
  }
  await expect(page.locator('.ob-tab.dirty')).toHaveCount(1);
});
