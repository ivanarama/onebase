const { test, expect } = require('@playwright/test');

for (const [scope, session] of [['admin', process.env.OB_NAV_ADMIN_SESSION], ['settings', process.env.OB_NAV_PERSONAL_SESSION]]) {
  for (const width of [1280, 390]) {
    test(`${scope}: palette states, hierarchy and long titles at ${width}px`, async ({ page, context, baseURL }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      await context.addCookies([{ name: 'onebase_session', value: session, url: baseURL }]);
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      const url = `/ui/${scope}/navigation?subsystem=School`;
      await page.goto(url);
      const boot = () => page.locator('#navigation-editor-data').evaluate(el => JSON.parse(el.textContent));
      await expect(page.locator('#navigation-editor-data')).toHaveAttribute('data-initialized', '1');
      const state = await boot();
      // Reset only this fixture's layer, then exercise the real controller.
      const form = { subsystem: state.subsystem, revision: state.revision };
      const reset = await page.request.post(`/ui/${scope}/navigation/reset`, { form });
      expect(reset.ok()).toBe(true);
      await page.goto(url);
      await expect(page.locator('#navigation-editor-data')).toHaveAttribute('data-initialized', '1');
      const current = await boot();
      const palette = page.locator('#navigation-palette');
      const search = page.getByRole('searchbox', { name: 'Поиск среди неразмещённых объектов' });
      await expect(search).toHaveAttribute('aria-describedby', 'navigation-palette-hint');
      await expect(page.locator('#navigation-palette-hint')).toContainText('которых нет в структуре меню');
      await expect(palette).toHaveText('Все доступные объекты уже размещены');
      const group = current.desired.sections.flatMap(s => s.groups || []).find(g => g.items?.length);
      const id = group.items.at(-1).id;
      const item = page.locator(`[data-node-id="${id}"]`).locator('..');
      const title = await item.locator('button').first().textContent();
      await search.fill(title.toUpperCase());
      await expect(palette).toHaveText('Все доступные объекты уже размещены');
      await item.locator('button').last().click();
      const restore = page.locator(`[data-restore-id="${id}"]`);
      await expect(restore).toBeVisible();
      await search.fill('НетТакогоНазванияXYZ');
      await expect(palette).toHaveText('Ничего не найдено');
      await search.fill('');
      await expect(restore).toBeVisible();
      await search.fill(title.toUpperCase());
      await restore.click();
      await expect(search).toHaveValue(title.toUpperCase());
      await expect(palette).toHaveText('Все доступные объекты уже размещены');
      const draft = await page.locator('#navigation-desired').inputValue();
      expect(JSON.parse(draft)).toEqual(current.desired);
      await search.fill('');

      // Rename through the public UI, including unbroken text that must wrap.
      const long = 'ОченьДлинноеНазвание'.repeat(7);
      for (const kind of ['section', 'group', 'item']) {
        await page.locator(`#navigation-tree [data-kind=${kind}] button`).first().click();
        const response = page.waitForResponse(r => r.url().endsWith('/navigation/preview') && r.status() === 200);
        await page.locator('#navigation-properties input[type=text]').fill(`${kind}: ${long}`);
        await response;
      }
      await expect(page.locator('#navigation-preview h3').first()).toContainText(long);
      await expect(page.locator('#navigation-preview summary').first()).toContainText(long);
      const dimensions = await page.evaluate(() => {
        const style = selector => {
          const el = document.querySelector(selector), css = getComputedStyle(el), box = el.getBoundingClientRect();
          return { size: parseFloat(css.fontSize), weight: parseInt(css.fontWeight), left: box.left, right: box.right };
        };
        return {
          section: style('#navigation-tree [data-kind=section] button'),
          group: style('#navigation-tree [data-kind=group] button'),
          item: style('#navigation-tree [data-kind=item] button'),
          previewSection: style('#navigation-preview h3'),
          previewGroup: style('#navigation-preview summary'),
          previewItem: style('#navigation-preview a'),
          palette: style('#navigation-palette'),
          filter: style('#navigation-filter'),
          tree: style('#navigation-tree'),
        };
      });
      for (const prefix of ['', 'preview']) {
        const [s, g, i] = prefix ? [dimensions.previewSection, dimensions.previewGroup, dimensions.previewItem] : [dimensions.section, dimensions.group, dimensions.item];
        expect(s.size).toBeGreaterThanOrEqual(i.size);
        expect(g.size).toBeGreaterThanOrEqual(i.size);
        expect(s.weight).toBeGreaterThan(i.weight);
        expect(g.weight).toBeGreaterThan(i.weight);
      }
      expect(dimensions.section.left).toBeLessThan(dimensions.group.left);
      expect(dimensions.group.left).toBeLessThan(dimensions.item.left);
      for (const key of ['section', 'group', 'item']) {
        expect(dimensions[key].right).toBeLessThanOrEqual(dimensions.tree.right + 1);
      }
      expect(dimensions.filter.right).toBeLessThanOrEqual(dimensions.palette.right + 1);
      await page.screenshot({ path: testInfo.outputPath(`${scope}-${width}.png`), fullPage: true });
      const save = page.waitForURL(u => u.searchParams.get('saved') === '1');
      await page.locator('#navigation-save button[type=submit]').click();
      await save;
      await expect(page.locator('#navigation-tree [data-kind=section] button').first()).toContainText(long);
      await expect(palette).toHaveText('Все доступные объекты уже размещены');
      expect(errors).toEqual([]);
    });
  }
}
