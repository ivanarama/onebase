const {test, expect} = require('@playwright/test');
const ids = JSON.parse(process.env.OB_LIST_IDS || '{}');
const list = page => page.locator('[data-ob-list-id="history"]');
const source = page => page.locator('[name="Секрет"][data-ob-concealed]');
async function open(page) {
  await page.goto(`/ui/catalog/${ids.entity}/${ids.owner}`);
  await expect(list(page)).toHaveAttribute('data-ob-list-state', 'denied');
  await expect(source(page)).toBeDisabled();
  await expect(list(page).locator('tbody tr')).toHaveCount(0);
}
async function reveal(page) {
  await page.locator('[name="Причина"]').fill('проверена');
  await page.getByRole('button', {name: 'Показать', exact: true}).click();
  await expect(source(page)).toBeEnabled();
  await expect(list(page)).toHaveAttribute('data-ob-list-state', 'ready');
  await expect(list(page).locator('tbody')).toContainText('строка-секрет');
}
for (const abort of [true, false]) {
  test(`revoke rejects a delayed disclosed list, AbortController=${abort}`, async ({page}) => {
    await page.addInitScript(enabled => {if (!enabled) window.AbortController = undefined;}, abort);
    await open(page);
    await reveal(page);
    const held = [];
    const intercept = async route => {
      const response = await route.fetch();
      const body = await response.body();
      let release;
      const barrier = new Promise(resolve => {release = resolve;});
      const entry = {release, done: false}; held.push(entry);
      await barrier;
      try {await route.fulfill({response, body});} catch (err) {if (!route.request().failure()) throw err;}
      entry.done = true;
    };
    await page.route('**/form-list-refresh', intercept);
    try {
      await source(page).fill('другой');
      await expect.poll(() => held.length).toBe(1);
      await source(page).fill('секрет');
      await expect.poll(() => held.length).toBe(2);
      await page.getByRole('button', {name: 'Отозвать', exact: true}).click();
      await expect(source(page)).toBeDisabled();
      await expect(list(page)).toHaveAttribute('data-ob-list-state', 'denied');
      for (const entry of [...held].reverse()) {
        entry.release();
        await expect.poll(() => entry.done).toBe(true);
      }
      await expect(list(page).locator('tbody tr')).toHaveCount(0);
      await expect(source(page)).toHaveValue('');
    } finally {
      held.forEach(entry => entry.release());
      await page.unroute('**/form-list-refresh', intercept);
    }
    await reveal(page);
  });
}
test('expiry clears dependent rows without input', async ({page}) => {
  await page.clock.install();
  await open(page);
  await reveal(page);
  await page.clock.fastForward(10 * 60 * 1000 + 1000);
  await expect(source(page)).toBeDisabled();
  await expect(source(page)).toHaveValue('');
  await expect(list(page)).toHaveAttribute('data-ob-list-state', 'denied');
  await expect(list(page).locator('tbody tr')).toHaveCount(0);
});

for (const answer of [false, true]) test(`revealed question remains usable after ${answer ? 'answer' : 'dismissal'}`, async ({page}) => {
  await open(page);
  await page.getByRole('button', {name: 'Вопрос', exact: true}).click();
  const question = page.locator('#_question-modal');
  await expect(question).toBeVisible();
  await expect(source(page)).toBeEnabled();
  await expect(list(page)).toHaveAttribute('data-ob-list-state', 'ready');
  if (answer) await question.getByRole('button', {name: 'Да', exact: true}).click();
  else await question.click({position: {x: 2, y: 2}});
  await expect(question).toHaveCount(0);
  await expect(source(page)).toHaveValue('секрет');
  await expect(list(page).locator('tbody')).toContainText('строка-секрет');
});
test('renderer failure revokes the displayed source and dependent list', async ({page}) => {
  await open(page);
  await reveal(page);
  await page.evaluate(() => {window.applyTableParts = () => {throw new Error('test renderer failure');};});
  await page.getByRole('button', {name: 'Показать', exact: true}).click();
  await expect(source(page)).toBeDisabled();
  await expect(list(page)).toHaveAttribute('data-ob-list-state', 'denied');
  await expect(list(page).locator('tbody tr')).toHaveCount(0);
});
