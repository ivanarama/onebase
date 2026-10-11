// Measure real list pages: a CSS string check cannot detect unused space (#1885).
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

test.use({ serviceWorkers: 'block' });

const lists = [
  { name: 'catalog list', path: '/ui/catalog/Номенклатура?view=list' },
  { name: 'catalog tree', path: '/ui/catalog/Номенклатура?view=tree' },
  { name: 'document list', path: '/ui/document/ПоступлениеТоваров?view=list' },
];

async function geometry(page) {
  return page.locator('.ob-list-content').evaluate(content => {
    const card = content.querySelector('.card');
    const panel = document.getElementById('ob-detail');
    const main = content.closest('main');
    const c = content.getBoundingClientRect();
    const r = card.getBoundingClientRect();
    const p = panel.getBoundingClientRect();
    const m = main.getBoundingClientRect();
    const stacked = getComputedStyle(content.parentElement).flexDirection === 'column';
    return {
      unused: Math.abs(c.width - r.width),
      gap: stacked ? p.top - c.bottom : p.left - r.right,
      outside: Math.max(0, r.right - m.right),
      stacked,
    };
  });
}

async function fillsColumn(page, panelVisible) {
  await expect.poll(async () => (await geometry(page)).unused).toBeLessThanOrEqual(1);
  expect((await geometry(page)).outside).toBeLessThanOrEqual(1);
  if (panelVisible) {
    await expect.poll(async () => (await geometry(page)).gap).toBeCloseTo(12, 0);
  }
}

for (const width of [1440, 3440]) {
  for (const list of lists) {
    test(`${width} ${list.name}: card fills column with hidden, open and resized details`, async ({ page }) => {
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.setViewportSize({ width, height: 1000 });
      await login(page);
      await open(page, list.path);
      await expect(page.locator('.ob-list-content table tbody tr').first()).toBeVisible();
      const panel = page.locator('#ob-detail');
      await expect(panel).toBeHidden();
      // Ensure the wide case actually exercises the old cap.
      if (width === 3440) {
        expect((await page.locator('.ob-list-content').boundingBox()).width).toBeGreaterThan(2000);
      }
      await fillsColumn(page, false);
      await page.locator('[data-ob-detail-toggle]').click();
      await expect(panel).toBeVisible();
      await fillsColumn(page, true);
      const before = (await panel.boundingBox()).width;
      const grip = await page.locator('[data-ob-detail-grip]').boundingBox();
      await page.mouse.move(grip.x + grip.width / 2, grip.y + 40);
      await page.mouse.down();
      await page.mouse.move(grip.x + grip.width / 2 - 140, grip.y + 40, { steps: 5 });
      await page.mouse.up();
      await expect.poll(async () => (await panel.boundingBox()).width).toBeGreaterThan(before + 100);
      await fillsColumn(page, true);
      await page.locator('[data-ob-detail-close]').click();
      await expect(panel).toBeHidden();
      await fillsColumn(page, false);
      expect(errors).toEqual([]);
    });
  }
}

for (const view of ['list', 'tree']) {
  test(`700 catalog ${view}: wide table scrolls inside the card and details stack below`, async ({ page }) => {
    await page.setViewportSize({ width: 700, height: 1000 });
    await login(page);
    await open(page, `/ui/catalog/Номенклатура?view=${view}`);
    const table = page.locator('.ob-list-content table');
    await expect(table.locator('tbody tr').first()).toBeVisible();
    await fillsColumn(page, false);
    await page.locator('[data-ob-detail-toggle]').click();
    await expect(page.locator('#ob-detail')).toBeVisible();
    expect((await geometry(page)).stacked).toBe(true);
    await fillsColumn(page, true);
    // Mobile CSS makes the table itself scrollable; desktop uses its wrapper.
    const scroll = await table.evaluate(table => {
      const scroller = getComputedStyle(table).overflowX === 'auto' ? table : table.parentElement;
      scroller.scrollLeft = 200;
      return {
        overflow: getComputedStyle(scroller).overflowX,
        client: scroller.clientWidth,
        total: scroller.scrollWidth,
        left: scroller.scrollLeft,
        content: document.querySelector('.ob-list-content').clientWidth,
      };
    });
    expect(scroll.overflow).toBe('auto');
    expect(scroll.total).toBeGreaterThan(scroll.client);
    expect(scroll.left).toBeGreaterThan(0);
    expect(scroll.client).toBeLessThanOrEqual(scroll.content);
  });
}
