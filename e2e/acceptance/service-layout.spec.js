const { test, expect } = require('@playwright/test');

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`service layout: forms, lists and table buttons at ${viewport.width}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    expect((await page.request.post('/auth/login', {
      data: { login: 'admin', password: 'secret123' },
    })).ok()).toBe(true);
    await page.goto('/ui/app');

    async function open(url) {
      await page.evaluate(url => window.obOpenTab(url, 'Layout'), url);
      const element = page.locator('#ob-tabbody iframe.active');
      await expect(element).toBeVisible();
      const frame = await (await element.elementHandle()).contentFrame();
      await frame.waitForLoadState();
      const box = await element.boundingBox();
      expect(box.height).toBeGreaterThan(200);
      expect(box.y + box.height).toBeLessThanOrEqual(viewport.height + 1);
      expect(box.x + box.width).toBeLessThanOrEqual(viewport.width + 1);
      return frame;
    }

    // Long ordinary form: scrolling must reach the table and bottom actions.
    const form = await open('/ui/catalog/Workspace/new');
    const name = `Layout ${viewport.width}`;
    await form.locator('input[name="Наименование"]').fill(name);
    await form.locator('input[name="Field23"]').fill('Last field');
    const add = form.locator('[data-ob-add-tp-row]');
    await add.click();
    const item = form.locator('input[name="tp.Lines.0.Item"]');
    await item.fill('Line at bottom');
    const save = form.getByRole('button', { name: 'Записать', exact: true }).last();
    await save.scrollIntoViewIfNeeded();
    await expect(save).toBeInViewport();
    await save.click();
    await expect(form.locator('input[name="Наименование"]')).toHaveValue(name);
    await expect(form.locator('input[name="Field23"]')).toHaveValue('Last field');
    await expect(form.locator('input[name="tp.Lines.0.Item"]')).toHaveValue('Line at bottom');

    const list = await open('/ui/catalog/Workspace');
    await expect(list.getByText(name, { exact: true }).first()).toBeVisible();
    const service = await open('/ui/page/Service');
    const composer = service.frameLocator('.ob-service-frame').locator('#composer');
    await composer.fill('Draft');
    await expect(composer).toBeInViewport();
    const composerBox = await composer.boundingBox();
    expect(composerBox.y + composerBox.height).toBeLessThanOrEqual(viewport.height + 1);
    expect(page.context().pages()).toHaveLength(1);
    expect(errors).toEqual([]);
  });
}
