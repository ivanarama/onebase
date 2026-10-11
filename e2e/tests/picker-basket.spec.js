// Real managed form, DOM and obFire; only the form-event HTTP responses are
// controlled so successive search pages have a deterministic selection.
const { test, expect } = require('@playwright/test');
const { login, open } = require('./helpers');

const columns = [
  {name: 'Товар', title: 'Товар', type: 'string'},
  {name: 'Количество', title: 'Количество', type: 'number', editable: true},
];
const first = {id: 'a', data: {Товар: '<Первый>', Количество: 2}};
const second = {id: 'b', data: {Товар: 'Второй', Количество: 3}};

async function picker(page, config = {}) {
  await login(page);
  await open(page, '/ui/document/РеализацияТоваров/new');
  const fields = await page.evaluate(() => obManagedConfig().serviceFields || {});
  let transferred;
  const requests = [];
  await page.route('**/form-event', async (route) => {
    const params = new URLSearchParams(route.request().postData());
    const value = (name) => params.get(fields[name] || name);
    const event = value('_event');
    requests.push(event);
    if (event === 'Выбор') {
      transferred = JSON.parse(value('_pick_result'));
      await route.fulfill({json: {ok: true}});
      return;
    }
    const query = value('_pick_query');
    const rows = query === 'second' ? [second] : query === 'empty' ? [] : [first];
    await route.fulfill({json: {ok: true, pickerData: {
      columns, rows, config: {title: 'Подбор', qtyField: 'Количество', serverSearch: true, ...config},
    }}});
  });
  await page.locator('.managed-tab-btn').getByText('Товары', {exact: true}).click();
  await page.locator('[data-ob-fire-click="КнопкаПересчитать"]').click();
  const modal = page.locator('#_item-picker-modal');
  await expect(modal).toBeVisible();
  const resultTable = modal.locator('table').first();
  const basket = modal.locator('table').nth(1).locator('tbody tr');
  async function search(query, id) {
    await modal.locator('input[type="text"]').first().fill(query);
    if (id) await expect(resultTable.locator(`tbody tr[data-id="${id}"]`)).toBeVisible();
    else await expect(resultTable.locator('tbody tr')).toHaveCount(0);
  }
  async function expectBasket(items) {
    await expect(basket).toHaveCount(items.length);
    for (let i = 0; i < items.length; i++) {
      await expect(basket.nth(i).locator('td')).toHaveText(items[i]);
    }
    await expect(modal.getByText(items.length ? `${items.length} поз.` : 'пусто', {exact: true})).toBeVisible();
    await expect(modal.getByText(`Выбрано: ${items.length}`, {exact: true})).toBeVisible();
  }
  return {modal, resultTable, basket, search, expectBasket, requests, transferred: () => transferred};
}

test('корзина и Перенести сохраняют выбор, порядок и количество между выдачами', async ({page}) => {
  const ctx = await picker(page);
  const firstRow = ctx.resultTable.locator('tr[data-id="a"]');
  await firstRow.locator('._ip-val').fill('5');
  await ctx.expectBasket([['<Первый>', '5']]);
  await ctx.search('second', 'b');
  await ctx.expectBasket([['<Первый>', '5']]);
  await ctx.resultTable.locator('._ip-val').fill('7');
  await ctx.expectBasket([['<Первый>', '5'], ['Второй', '7']]);
  // A returning row must preserve its edited quantity and never duplicate.
  await ctx.search('first', 'a');
  await expect(firstRow.locator('._ip-val')).toHaveValue('5');
  await ctx.expectBasket([['<Первый>', '5'], ['Второй', '7']]);
  await firstRow.locator('._ip-cb').uncheck();
  await ctx.expectBasket([['Второй', '7']]);
  await firstRow.locator('._ip-cb').check();
  await ctx.expectBasket([['Второй', '7'], ['<Первый>', '5']]);
  await firstRow.locator('._ip-val').fill('4');
  await ctx.expectBasket([['Второй', '7'], ['<Первый>', '4']]);
  await ctx.search('empty');
  await ctx.expectBasket([['Второй', '7'], ['<Первый>', '4']]);
  await ctx.modal.getByRole('button', {name: 'Перенести в документ', exact: true}).click();
  await expect.poll(ctx.transferred).toEqual([
    {id: 'b', Товар: 'Второй', Количество: '7'},
    {id: 'a', Товар: '<Первый>', Количество: '4'},
  ]);
  expect(ctx.requests).toContain('Поиск');
});

test('checkAll и Выбрать всё меняют только текущую выдачу полной корзины', async ({page}) => {
  const ctx = await picker(page, {checkAll: true});
  await ctx.expectBasket([['<Первый>', '2']]);
  await ctx.search('second', 'b');
  await ctx.expectBasket([['<Первый>', '2'], ['Второй', '3']]);
  const all = ctx.resultTable.locator('thead input[type="checkbox"]');
  // The header initially starts unchecked even when rows came from checkAll.
  await all.check();
  await all.uncheck();
  await ctx.expectBasket([['<Первый>', '2']]);
  await all.check();
  await ctx.expectBasket([['<Первый>', '2'], ['Второй', '3']]);
  await ctx.resultTable.locator('._ip-val').fill('0');
  await ctx.expectBasket([['<Первый>', '2']]);
});

test('локальная корзина продолжает показывать положительные количества видимых строк', async ({page}) => {
  const ctx = await picker(page, {serverSearch: false});
  await expect(ctx.basket.locator('td')).toHaveText(['<Первый>', '2']);
  await ctx.modal.locator('input[type="text"]').first().fill('другое');
  await expect(ctx.basket).toHaveCount(0);
  await ctx.modal.locator('input[type="text"]').first().fill('Первый');
  await expect(ctx.basket.locator('td')).toHaveText(['<Первый>', '2']);
  expect(ctx.requests).toEqual(['Нажатие']);
});
