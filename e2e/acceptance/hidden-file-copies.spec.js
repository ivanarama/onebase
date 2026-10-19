const { test, expect } = require('@playwright/test');
const ids = JSON.parse(process.env.OB_HIDDEN_COPY_IDS || '{}');
let pageErrors;
test.beforeEach(async ({ page }) => {
  pageErrors = [];
  page.on('pageerror', e => pageErrors.push(e.message));
});
test.afterEach(() => expect(pageErrors).toEqual([]));
const visible = page => page.locator('input[name="Текст"]:visible');
async function currentIDs(page) {
  return visible(page).evaluate(p => ({path: p.id, content: p.parentElement.querySelector('textarea').id,
    pick: p.parentElement.querySelector('input[type="file"]').id}));
}
async function pick(page, text, name = 'review.txt') {
  const id = await currentIDs(page);
  const chooser = page.waitForEvent('filechooser');
  await page.locator(`[data-ob-file-trigger="${id.pick}"]`).click();
  await (await chooser).setFiles({name, mimeType: 'text/plain', buffer: Buffer.from(text)});
}
async function nativeValue(page, value) {
  expect(await page.locator('#main-form').evaluate(f => new FormData(f).getAll('_fc_Текст'))).toEqual([value]);
}
async function fire(page, button, expected) {
  const [r] = await Promise.all([
    page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/form-event')),
    page.getByRole('button', {name: button, exact: true}).click(),
  ]);
  const sent = new URLSearchParams(r.request().postData());
  expect(sent.getAll('Текст')).toEqual([expected]);
  expect(sent.has('_fc_Текст')).toBe(false);
  const data = await r.json();
  expect(data.ok).toBe(true);
  expect(data.error || '').toBe('');
  if (button === 'Проверить' || button === 'Сохранить') {
    expect(data.values.Получено).toBe(expected);
    await expect(page.locator('[name="Получено"]')).toHaveValue(expected);
  }
  return data;
}
async function saveAndRead(page, url, expected) {
  const data = await fire(page, 'Сохранить', expected);
  expect(data.version).toBeGreaterThan(1);
  await page.goto(url);
  await expect(page.locator('[name="Получено"]')).toHaveValue(expected);
}

for (const name of Object.keys(ids)) {
  const url = `/ui/catalog/${name}/${ids[name]}?lang=ru`;
  async function open(page) {
    await page.goto(url);
    await expect(page.locator('input[name="Текст"]')).toHaveCount(2);
    await expect(visible(page)).toHaveCount(1);
  }
  test(`${name}: actual file event, empty file, reverse switch and persisted handler value`, async ({page}) => {
    await open(page);
    for (const value of ['Текст проверки\nВторая строка', '', 'После обратного переключения']) {
      await pick(page, value);
      await expect.poll(async () => page.evaluate(id => document.getElementById(id)._obFileReadState?.loading, (await currentIDs(page)).content)).toBe(false);
      await nativeValue(page, value);
      await fire(page, 'Проверить', value);
      // Switching sends this copy before disabling it; the next iteration must
      // use the other copy even though this stale backing content stays in DOM.
      await fire(page, 'Переключить', value);
    }
    // Re-read this visible copy, save through the normal public form, then GET.
    await pick(page, 'Записанное содержимое');
    await fire(page, 'Проверить', 'Записанное содержимое');
    await saveAndRead(page, url, 'Записанное содержимое');
  });

  test(`${name}: pending/latest reader and errors control real event dispatch`, async ({page}) => {
    await open(page);
    await page.evaluate(() => {
      const Native = window.FileReader;
      window.__reads = [];
      window.FileReader = class {
        readAsArrayBuffer(file) {
          window.__reads.push({file, finish: () => {
            const r = new Native();
            r.onload = () => { this.result = r.result; this.onload(); };
            r.readAsArrayBuffer(file);
          }, fail: () => { this.error = new Error('test read failure'); this.onerror(); }});
        }
      };
      window.__eventPosts = 0;
      const fetch = window.fetch;
      window.fetch = (...args) => {
        if (String(args[0]).endsWith('/form-event')) window.__eventPosts++;
        return fetch(...args);
      };
    });
    await pick(page, 'Устаревшее', 'old.txt');
    await page.getByRole('button', {name:'Проверить', exact:true}).click();
    await expect.poll(() => page.evaluate(() => window.__eventPosts)).toBe(0);
    await pick(page, 'Актуальное', 'current.txt');
    const response = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/form-event'));
    await page.evaluate(() => window.__reads[1].finish());
    const r = await response;
    expect(new URLSearchParams(r.request().postData()).getAll('Текст')).toEqual(['Актуальное']);
    expect((await r.json()).values.Получено).toBe('Актуальное');
    await page.evaluate(() => window.__reads[0].finish());
    await expect(visible(page)).toHaveValue('current.txt');
    // A read failure vetoes dispatch. A later successful empty file recovers.
    await pick(page, 'Ошибка', 'broken.txt');
    await page.evaluate(() => window.__reads[2].fail());
    await page.getByRole('button', {name:'Проверить', exact:true}).click();
    await expect(page.locator('#ob-fmevt-banner')).toContainText('test read failure');
    expect(await page.evaluate(() => window.__eventPosts)).toBe(1);
    await pick(page, '', 'empty.txt');
    await page.evaluate(() => window.__reads[3].finish());
    await fire(page, 'Проверить', '');
    await nativeValue(page, '');
  });

  test(`${name}: manual/desktop paths, cancel, disabled and readonly copies, close snapshot`, async ({page}) => {
    await open(page);
    const id = await currentIDs(page);
    await visible(page).fill('/manual/path.txt');
    await fire(page, 'Проверить', '/manual/path.txt');
    // Desktop WebView supplies file.path instead of browser contents.
    await page.evaluate(id => window.obFilePick({files:[{path:'/desktop/path.txt'}]},id.path,id.content), id);
    await fire(page, 'Проверить', '/desktop/path.txt');
    await page.evaluate(id => window.obFilePick({files:[]},id.path,id.content), id);
    await fire(page, 'Проверить', '');
    await pick(page, 'Для закрытия');
    await fire(page, 'Проверить', 'Для закрытия');
    const closeResponse = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/form-close-intent'));
    const result = await page.evaluate(() => window.obRequestFormClose({reason:'close', mode:'discard'}));
    const close = await closeResponse;
    expect(new URLSearchParams(close.request().postData()).getAll('Текст')).toEqual(['Для закрытия']);
    expect(result.allowed).toBe(false); // real BeforeClose handler vetoes closing
    await expect(page.locator('[name="Получено"]')).toHaveValue('Для закрытия');
    // A disabled helper must not substitute a path with stale contents.
    await page.evaluate(id => {document.getElementById(id.content).disabled=true; document.getElementById(id.path).value='/readonly/path'; document.getElementById(id.path).readOnly=true;}, id);
    await fire(page, 'Проверить', '/readonly/path');
    await expect(visible(page)).toHaveAttribute('readonly', '');
  });
}
