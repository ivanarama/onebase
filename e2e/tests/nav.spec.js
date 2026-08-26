// Панель объектов: схлопывание на широком экране (#1122) и мобильная шторка.
//
// Единственная проверка, которая действительно жмёт кнопку: Go-тесты смотрят на
// CSS и текст ui.js, а значит `nav-collapsed` у них применяется по описанию, а
// не браузером. Тут — настоящий движок с настоящим брейкпоинтом.

const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

const TOGGLE = '[data-ob-nav-toggle]';
const NAV = '#ob-nav';

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('на широком экране гамбургер схлопывает панель и состояние переживает переход', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await open(page, '/ui/');

  // Исходно панель раскрыта, кнопка видима (раньше на десктопе её не было вовсе).
  await expect(page.locator(NAV)).toBeVisible();
  await expect(page.locator(TOGGLE)).toBeVisible();
  await expect(page.locator(TOGGLE)).toHaveAttribute('aria-expanded', 'true');

  await page.click(TOGGLE);
  await expect(page.locator(NAV)).toBeHidden();
  await expect(page.locator('html')).toHaveClass(/nav-collapsed/);
  await expect(page.locator(TOGGLE)).toHaveAttribute('aria-expanded', 'false');

  // Режим открытия форм по умолчанию — «Страницы», то есть каждый переход это
  // полная перезагрузка. Панель обязана остаться схлопнутой и не мигнуть:
  // класс ставится синхронно в <head>, поэтому он есть уже на первой отрисовке.
  await open(page, '/ui/catalog/Номенклатура');
  await expect(page.locator('html')).toHaveClass(/nav-collapsed/);
  await expect(page.locator(NAV)).toBeHidden();

  // И возвращается тем же переключателем.
  await page.click(TOGGLE);
  await expect(page.locator(NAV)).toBeVisible();
  await expect(page.locator('html')).not.toHaveClass(/nav-collapsed/);

  await open(page, '/ui/');
  await expect(page.locator(NAV)).toBeVisible();
});

test('схлопнутая на десктопе панель не запирает мобильную шторку', async ({ page }) => {
  // Ровно тот сценарий, в котором два режима могли схлестнуться: схлопнул на
  // широком экране, сузил окно. `nav-collapsed` остаётся на <html>, но на узком
  // экране он не адресован ни одним правилом — шторкой командует `nav-open`.
  await page.setViewportSize({ width: 1280, height: 720 });
  await open(page, '/ui/');
  await page.click(TOGGLE);
  await expect(page.locator('html')).toHaveClass(/nav-collapsed/);

  await page.setViewportSize({ width: 390, height: 780 });
  await expect(page.locator(TOGGLE)).toBeVisible();

  // Шторка закрыта — она уехана за левый край, а не скрыта display:none:
  // если бы nav-collapsed сюда дотянулся, box стал бы null.
  const navX = async () => {
    const box = await page.locator(NAV).boundingBox();
    return box ? box.x : null;
  };
  await expect.poll(navX).toBeLessThan(0);

  await page.click(TOGGLE);
  await expect(page.locator('body')).toHaveClass(/nav-open/);
  await expect.poll(navX).toBeGreaterThanOrEqual(0);

  // И закрывается по Escape, как и раньше.
  await page.keyboard.press('Escape');
  await expect(page.locator('body')).not.toHaveClass(/nav-open/);
});
