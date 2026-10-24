// Проверяем прокрутку реальных страниц с длинным черновиком меню (#2157).
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

const user = {
  login: process.env.OB_USER_LOGIN || 'user',
  password: process.env.OB_USER_PASSWORD || 'Us3r-P@ssw0rd!',
};

for (const [name, path, credentials] of [
  ['общая', '/ui/admin/navigation', undefined],
  ['личная', '/ui/settings/navigation', user],
]) {
  test(`${name} настройка: длинное меню прокручивается на широком и узком экране`, async ({ page }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 1366, height: 768 });
    await login(page, credentials);
    await open(page, path);
    await expect(page.locator('#navigation-editor-data')).toHaveAttribute('data-initialized', '1');

    // Создаём дерево действиями пользователя, не подменяя HTML или стили.
    // Черновик не сохраняем: следующие сценарии получат прежнюю раскладку.
    const rows = page.locator('#navigation-tree .navigation-row');
    // Один скрытый пункт заполняет палитру доступных объектов справа.
    await page.locator('#navigation-tree .navigation-row[data-kind=item]').first().locator('button').last().click();
    const initialRows = await rows.count();
    for (let i = 0; i < 30; i++) {
      await page.locator('#navigation-add-section').click();
    }
    await expect(rows).toHaveCount(initialRows + 30);

    const main = page.locator('#navigation-settings');
    const preview = page.locator('#navigation-preview');
    const lastObject = page.locator('#navigation-palette > div').last();
    await expect(lastObject).toBeAttached();
    const desktop = await main.evaluate(el => ({
      clientHeight: el.clientHeight,
      scrollHeight: el.scrollHeight,
      bottom: el.getBoundingClientRect().bottom,
      viewport: innerHeight,
    }));
    expect(desktop.scrollHeight).toBeGreaterThan(desktop.clientHeight);
    expect(desktop.bottom).toBeLessThanOrEqual(desktop.viewport + 1);
    await main.hover({ position: { x: 40, y: 40 } });
    await page.mouse.wheel(0, 1200);
    await expect.poll(() => main.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
    for (const target of [rows.last(), preview, lastObject]) {
      await target.scrollIntoViewIfNeeded();
      await expect(target).toBeInViewport();
    }

    // При max-width у main остаются равные горизонтальные отступы.
    await page.setViewportSize({ width: 2200, height: 900 });
    const margins = await main.evaluate(el => {
      const style = getComputedStyle(el);
      return { left: parseFloat(style.marginLeft), right: parseFloat(style.marginRight) };
    });
    expect(margins.left).toBeGreaterThan(0);
    expect(Math.abs(margins.left - margins.right)).toBeLessThanOrEqual(1);

    // На мобильной раскладке колесо прокручивает документ целиком.
    await page.setViewportSize({ width: 800, height: 768 });
    await page.evaluate(() => scrollTo(0, 0));
    await main.hover({ position: { x: 40, y: 40 } });
    await page.mouse.wheel(0, 1200);
    await expect.poll(() => page.evaluate(() => scrollY)).toBeGreaterThan(0);
    for (const target of [rows.last(), preview, lastObject]) {
      await target.scrollIntoViewIfNeeded();
      await expect(target).toBeInViewport();
    }
  });
}
