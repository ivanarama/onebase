// Exercise the actual picker click and fetch with the complete ui.js and a real DOM.
const { test, expect } = require('@playwright/test');
const path = require('node:path');

for (const owner of ['Заявка', 'НаправлениеОбслуживания']) {
  for (const filtered of [false, true]) {
    test(`preview source is form owner ${owner}, choice_filter=${filtered}`, async ({ page }) => {
      const errors = [];
      page.on('pageerror', (error) => errors.push(error.message));
      const requests = [];
      const target = 'НаправлениеОбслуживания';
      await page.route('**/preview-fixture', (route) => route.fulfill({
        contentType: 'text/html; charset=utf-8',
        body: `<form>
          <input name="Филиал" value="old-branch">
          <input name="Контрагент" value="owner-id">
          <select id="ref-direction" data-ref-entity="${target}"
            data-ref-source-entity="${owner}" data-ref-element="ПолеНаправление"
            data-ref-context='{"Филиал":"Объект.Филиал"}'
            data-ref-filter='{"Владелец":{"from":"Контрагент","value":"owner-id"}}'
            ${filtered ? `data-ref-choice-context='{"form_entity":"${owner}","form":"ФормаОбъекта","element":"direction","sources":{"Объект.Филиал":"Филиал"}}'` : ''}>
            <option value="">— выбрать —</option>
          </select>
          <button type="button" data-ob-ref-picker="ref-direction">…</button>
        </form>`,
      }));
      await page.route('**/ui/_ref-options/**', async (route) => {
        const request = route.request();
        if (request.method() !== 'POST') {
          await route.fulfill({contentType: 'application/json', body: JSON.stringify({items: [{id: 'direction-id', _label: 'Ремонт'}], total: 1, selected_allowed: true})});
          return;
        }
        const body = request.postDataJSON();
        requests.push({method: request.method(), url: request.url(), body});
        // Model the endpoint contract; sending the target as owner cannot show preview.
        await route.fulfill({
          status: body.source.entity === owner ? 200 : 400,
          contentType: 'application/json',
          body: JSON.stringify({items: [{id: 'direction-id', _label: 'Ремонт', _preview: body.context.Филиал}], total: 1, preview: '_preview'}),
        });
      });
      await page.goto('/preview-fixture');
      await page.addScriptTag({path: path.resolve(__dirname, '../../internal/ui/static/ui.js')});
      // Change without dispatching change: POST must read the current unsaved value.
      await page.locator('[name="Филиал"]').evaluate((input) => { input.value = 'current-branch'; });
      await page.locator('[data-ob-ref-picker]').click();
      await expect.poll(() => requests.length).toBeGreaterThan(0);
      expect(requests[0].method).toBe('POST');
      expect(new URL(requests[0].url).pathname).toBe('/ui/_ref-options/' + encodeURIComponent(target) + '/page');
      for (const request of requests) expect(request.body).toEqual({
        q: '', limit: 50, offset: 0,
        source: {entity: owner, element: 'ПолеНаправление'},
        context: {Филиал: 'current-branch'},
        filters: {Владелец: 'owner-id'},
      });
      await expect(page.locator('#_rp-preview')).toHaveText('current-branch');
      const initialCount = requests.length;
      await page.locator('#_rp-search').fill('рем');
      await expect.poll(() => requests.length).toBeGreaterThan(initialCount);
      expect(requests[requests.length - 1].body.source).toEqual({entity: owner, element: 'ПолеНаправление'});
      expect(requests[requests.length - 1].body.q).toBe('рем');
      await page.locator('#_rp-search').press('Enter');
      await expect(page.locator('#ref-direction')).toHaveValue('direction-id');
      await expect(page.locator('#_ref-picker-modal')).toHaveCount(0);
      expect(errors).toEqual([]);
    });
  }
}
