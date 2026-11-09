const { test, expect } = require('@playwright/test');
const ids = JSON.parse(process.env.OB_CHOICE_SOURCE_IDS || '{}');
const sources = page => page.locator('select[name="Направление"]');
const targets = ['Неисправность', 'ЛокальнаяНеисправность'];
let errors = [];
test.beforeEach(async ({ page }) => {
  errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(`/ui/document/Заявка/${ids.owner}`);
  await expect(sources(page)).toHaveCount(2);
  await expect(page.locator('select[name="Неисправность"]')).toHaveAttribute('data-ref-choice-context', /fault-picker/);
  await expect(page.locator('select[name="ЛокальнаяНеисправность"]')).not.toHaveAttribute('data-ref-choice-context');
  await expect(sources(page).first()).toBeVisible();
  await expect(sources(page).last()).toBeDisabled();
});
test.afterEach(() => expect(errors).toEqual([]));

async function fire(page, name) {
  const [response] = await Promise.all([
    page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/form-event')),
    page.getByRole('button', { name, exact: true }).click(),
  ]);
  expect(response.ok()).toBe(true);
  const result = await response.json();
  expect(result.ok).toBe(true);
  expect(result.error || '').toBe('');
}

// Observe the actual requests and real server responses; no endpoint, client
// function, selected_allowed or HTTP body is replaced by the test.
async function refresh(page, expected) {
  const replies = [];
  const listener = r => {
    if (decodeURI(r.url()).includes('/ui/_ref-options/Неисправность?')) replies.push(r);
  };
  page.on('response', listener);
  try {
    await sources(page).first().dispatchEvent('change');
    await expect.poll(() => replies.length).toBe(2);
    expect(replies.filter(r => new URL(r.url()).searchParams.has('sources'))).toHaveLength(1);
    for (const response of replies) {
      expect(response.ok()).toBe(true);
      const url = new URL(response.url());
      expect(JSON.parse(url.searchParams.get('flt')).Владелец).toBe(expected);
      if (url.searchParams.has('sources')) {
        expect(JSON.parse(url.searchParams.get('sources'))['Объект.Направление']).toBe(expected);
      }
      const body = await response.json();
      if (!expected) expect(body.items).toEqual([]);
      else {
        expect(body.items.length).toBeGreaterThan(0);
        for (const item of body.items) {
          expect(item.Владелец).toBe(expected);
          expect(item.id).not.toBe(ids.hidden);
        }
      }
    }
    for (const name of targets) {
      const select = page.locator(`select[name="${name}"]`);
      if (!expected) await expect(select.locator('option')).toHaveCount(1);
      else await expect.poll(() => select.locator('option').count()).toBeGreaterThan(1);
    }
  } finally { page.off('response', listener); }
}

async function values(page) {
  await sources(page).evaluateAll((controls, ids) => {
    controls[0].value = ids.a;
    controls[1].value = ids.b;
  }, ids);
}

test('active source follows both visibility directions after real form events', async ({ page }) => {
  for (const [mode, expected] of [['first', ids.a], ['second', ids.b], ['first', ids.a]]) {
    await page.locator('input[name="Режим"]').fill(mode);
    await fire(page, 'Применить');
    await values(page);
    await refresh(page, expected);
  }
});

for (const mode of ['both', 'none']) {
  test(`${mode} source closes both dependent lists`, async ({ page }) => {
    await page.locator('input[name="Режим"]').fill(mode);
    await fire(page, 'Применить');
    await values(page);
    await refresh(page, '');
    await pickerContext(page, '');
  });
}

test('readonly source uses its enabled hidden mirror and choice_context', async ({ page }) => {
  await sources(page).first().selectOption(ids.b);
  await fire(page, 'Заблокировать');
  await expect(sources(page).first()).toBeDisabled();
  const values = await page.locator('#main-form').evaluate(f => new FormData(f).getAll('Направление'));
  expect(values).toEqual([ids.b]);
  await refresh(page, ids.b);
  await pickerContext(page, ids.b);
});

async function pickerContext(page, expected) {
  const target = page.locator('select[name="Неисправность"]');
  const [response] = await Promise.all([
    page.waitForResponse(r => r.request().method() === 'POST' && decodeURI(r.url()).endsWith('/ui/_ref-options/Неисправность/page')),
    target.locator('..').locator('[data-ob-ref-picker]').click(),
  ]);
  expect(response.ok()).toBe(true);
  expect(response.request().postDataJSON().context.Направление).toBe(expected);
  expect(response.request().postDataJSON().filters.Владелец).toBe(expected);
  const body = await response.json();
  if (!expected) {
    expect(body.items).toEqual([]);
    await expect(page.locator('#_rp-list ._rp-item')).toHaveCount(0);
  } else {
    expect(body.items.length).toBeGreaterThan(0);
    await expect(page.locator('#_rp-list ._rp-item').first()).toBeVisible();
  }
  await page.locator('#_rp-cancel').click();
}
