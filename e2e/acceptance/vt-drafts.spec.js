const { test, expect } = require('@playwright/test');

async function fixture(page, entity = 'Draft') {
  const requests = [], responses = [], errors = [];
  page.on('pageerror', e => errors.push(String(e)));
  await page.addInitScript(() => {
    window.vtReplies = [];
    const original = window.fetch;
    window.fetch = async (...args) => {
      const response = await original(...args);
      if (String(args[0]).includes('/form-event')) {
        const json = response.json.bind(response);
        response.json = async () => {
          const value = await json();
          setTimeout(() => window.vtReplies.push(value), 0);
          return value;
        };
      }
      return response;
    };
  });
  page.on('request', request => {
    if (request.url().includes('/form-event')) requests.push(new URLSearchParams(request.postData()));
  });
  page.on('response', async response => {
    if (response.url().includes('/form-event')) responses.push(await response.json());
  });
  await page.goto(`/ui/catalog/${entity}/new`);
  await expect.poll(() => page.evaluate(() => window.vtReplies.length)).toBe(1);
  const body = page.locator(entity === 'Readonly' ? 'tbody[data-vt-fields]' : 'tbody[data-vt-fields]:not([data-ob-table-readonly="1"])');
  const rows = body.locator('tr');
  const name = index => rows.nth(index).locator('input[name$=".Name"]');
  const add = async () => page.locator('[data-ob-add-vt="Results"]').click();
  const click = index => entity === 'Readonly' ? rows.nth(index).locator('td').first().click({position: {x: 1, y: 1}}) : name(index).click();
  async function silent(index) {
    const count = requests.length;
    await click(index);
    await page.waitForTimeout(200);
    expect(responses.every(r => r.ok), JSON.stringify(responses)).toBe(true);
    expect(requests).toHaveLength(count);
    expect(errors).toEqual([]);
  }
  async function activate(index, normalized, value, total) {
    const count = requests.length;
    await click(index);
    await expect.poll(() => page.evaluate(() => window.vtReplies.length)).toBe(count + 1);
    expect(requests).toHaveLength(count + 1);
    expect(requests.at(-1).get('_tp_row')).toBe(String(normalized));
    expect(responses.at(-1).ok, JSON.stringify(responses.at(-1))).toBe(true);
    expect(responses.at(-1).messages).toEqual([value, String(total)]);
    // Native FormData must still have one successful source per key.
    const payload = await page.evaluate(() => [...new FormData(document.getElementById('main-form'))].filter(([k]) => k.startsWith('vt.')));
    expect(new Set(payload.map(([k]) => k)).size).toBe(payload.length);
    await silent(normalized);
    return responses.at(-1);
  }
  return { rows, name, add, silent, activate, requests, responses };
}

test('empty draft click before fill preserves input and sends no activation', async ({ page }) => {
  const f = await fixture(page);
  await expect(f.rows).toHaveCount(1);
  await f.add();
  await f.silent(1);
  await expect(f.rows).toHaveCount(2);
  await expect(f.name(1)).toBeFocused();
  await f.name(1).fill('new row');
  await f.activate(1, 1, 'new row', 2);
});

test('initially empty table and clearing a draft do not activate empty values', async ({ page }) => {
  const f = await fixture(page, 'Empty');
  await expect(f.rows).toHaveCount(0);
  await f.add();
  await f.silent(0);
  await f.name(0).fill('discard');
  await f.name(0).fill('');
  await f.silent(0);
  await expect(f.rows).toHaveCount(1);
  await f.name(0).fill('first');
  await f.activate(0, 0, 'first', 1);
  await f.name(0).fill('');
  // A formerly active row that is now empty must not remain selected.
  await f.silent(0);
  await f.name(0).fill('restored');
  await f.activate(0, 0, 'restored', 1);
});

test('sparse drafts use normalized indices and preserve selection through echo', async ({ page }) => {
  const f = await fixture(page, 'Empty');
  for (let i = 0; i < 5; i++) await f.add();
  await f.silent(0);
  await f.silent(2);
  await f.silent(4);
  await f.name(1).fill('between');
  await f.name(3).fill('after');
  const r = await f.activate(3, 1, 'after', 2);
  expect(r.formTables.Results.map(row => row.Name)).toEqual(['between', 'after']);
  await f.rows.first().locator('[data-ob-remove-row]').click();
  await f.activate(0, 0, 'after', 1);
});

for (const value of ['0', 'false', '   ']) {
  test(`nonempty string ${JSON.stringify(value)} survives normalization`, async ({ page }) => {
    const f = await fixture(page, 'Empty');
    await f.add();
    await f.add();
    await f.name(1).fill(value);
    await f.activate(1, 0, value, 1);
  });
}

test('unchecked absent bool does not make a draft nonempty', async ({ page }) => {
  const f = await fixture(page, 'Scalars');
  await f.add();
  await f.silent(1);
  await f.add();
  await f.name(2).fill('after unchecked');
  const reply = await f.activate(2, 1, 'after unchecked', 2);
  expect(reply.formTables.Results[1].Flag).toBe(false);
});

test('hidden ID readonly zero and explicit false carrier retain the real row', async ({ page }) => {
  const f = await fixture(page, 'Protected');
  await f.name(0).fill('');
  let reply = await f.activate(0, 0, '', 1);
  expect(reply.formTables.Results[0]).toMatchObject({ ID: 'hidden', Locked: 0, Flag: false });
  await f.add();
  // The protected bool carrier explicitly submits false even in a new row.
  reply = await f.activate(1, 1, '', 2);
  expect(reply.formTables.Results[1].Flag).toBe(false);
  expect(f.requests.at(-1).get('vt.Results.1.Flag')).toBe('false');
  expect(f.requests.at(-1).getAll('vt.Results.1.Flag')).toHaveLength(1);
});


test('unknown columns cannot make an empty draft activate', async ({ page }) => {
  const f = await fixture(page, 'Empty');
  await f.add();
  await page.evaluate(() => {
    const field = document.createElement('input');
    field.type = 'hidden'; field.name = 'vt.Results.0.Unknown'; field.value = 'ignored';
    document.getElementById('main-form').appendChild(field);
  });
  await f.silent(0);
  await f.name(0).fill('declared');
  await f.activate(0, 0, 'declared', 1);
});

test('a display-only placement stays outside payload and the server rejects its event', async ({ page }) => {
  const f = await fixture(page, 'Readonly');
  await expect(f.rows).toHaveCount(1);
  const count = f.requests.length;
  await f.rows.first().locator('td').first().click({position: {x: 1, y: 1}});
  await expect.poll(() => page.evaluate(() => window.vtReplies.length)).toBe(count + 1);
  expect(f.responses.at(-1).ok).toBe(false);
  expect(f.responses.at(-1).error).toContain('доступен только для чтения');
  expect([...f.requests.at(-1).keys()].filter(k => k.startsWith('vt.'))).toEqual([]);
});
