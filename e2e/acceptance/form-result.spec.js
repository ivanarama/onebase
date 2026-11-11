const { test, expect } = require('@playwright/test');
const origins = page => page.locator('#ob-tabbody iframe');
const results = page => page.locator('.ob-result-form iframe');
let errors;

test.beforeEach(async ({ page }) => {
  errors = [];
  page.on('pageerror', error => errors.push(error.message));
  expect((await page.request.post('/auth/login', { data: { login: 'admin', password: 'secret123' } })).ok()).toBe(true);
  await page.goto('/ui/app', { waitUntil: 'domcontentloaded' });
});
test.afterEach(() => expect(errors).toEqual([]));

async function order(page, number = 'REAL-42', comment = '') {
  const initial = await page.evaluate(() => {
    const tab = window.obOpenTab('/ui/document/Orders/new', 'RAW NUMBER MUST NOT FLASH', {allowDup:true});
    return {label:tab.label.textContent, title:tab.title, tooltip:tab.btn.title};
  });
  expect(Object.values(initial).every(value => !value.includes('RAW NUMBER MUST NOT FLASH'))).toBe(true);
  const frame = await (await origins(page).last().elementHandle()).contentFrame();
  await frame.locator('input[name="Номер"]').fill(number);
  await frame.locator('input[name="Комментарий"]').fill(comment);
  await expect(page.locator('.ob-tab-label').last()).toHaveText('MASK***');
  await expect(frame.locator('[data-ob-form-title]')).toHaveText('MASK***');
  return frame;
}
async function accept(frame) { await frame.getByRole('button', {name:'Accept',exact:true}).click(); }
async function lastResult(page) {
  await expect(results(page).last()).toBeVisible();
  const frame = await (await results(page).last().elementHandle()).contentFrame();
  await expect(frame.locator('input[name="DisplayNumber"]')).not.toHaveValue('');
  return frame;
}

test('source really disappears and own result command opens a card then closes self', async ({page}) => {
  const frame = await order(page);
  await accept(frame);
  const mini = await lastResult(page);
  await expect(origins(page)).toHaveCount(0);
  expect(frame.isDetached()).toBe(true);
  await expect(mini.locator('input[name="DisplayNumber"]')).toHaveValue('REAL-42');
  await mini.getByRole('button', {name:'Next',exact:true}).click();
  await expect(results(page)).toHaveCount(0);
  await expect(origins(page)).toHaveCount(1);
  const next = await (await origins(page).first().elementHandle()).contentFrame();
  await expect(next.locator('input[name="Номер"]')).toHaveValue('NEXT');
});

test('Escape focused INSIDE the result iframe uses managed Close/Cancel', async ({page}) => {
  await accept(await order(page, 'ESCAPE-43'));
  const mini = await lastResult(page);
  await mini.locator('input[name="DisplayNumber"]').focus();
  await mini.locator('input[name="DisplayNumber"]').press('Escape');
  await expect(results(page)).toHaveCount(0);
  await expect(origins(page)).toHaveCount(0);
});

test('origin BeforeClose veto prevents false success and preserves source', async ({page}) => {
  const frame = await order(page, 'VETO-44');
  await frame.locator('select[name="Block"]').selectOption('true');
  const response = page.waitForResponse(r => r.url().includes('/Orders/form-close-intent'));
  await accept(frame);
  const data = await (await response).json();
  expect(data.close.allowed).toBe(false);
  await expect(results(page)).toHaveCount(0);
  await expect(origins(page)).toHaveCount(1);
  expect(frame.isDetached()).toBe(false);
});

test('unsaved entity mutation in BeforeClose prevents result handoff', async ({page}) => {
  const frame = await order(page, 'CLOSE-MUTATION-50', 'mutate-on-close');
  const response = page.waitForResponse(r => r.url().includes('/Orders/form-close-intent'));
  await accept(frame);
  const data = await (await response).json();
  expect(data.dirty).toBe(true);
  await expect(frame.locator('input[name="Комментарий"]')).toHaveValue('unsaved close mutation');
  await expect.poll(() => frame.evaluate(() => window._obFormDirty)).toBe(true);
  await expect(origins(page)).toHaveCount(1);
  await expect(results(page)).toHaveCount(0);
});

test('input during pending close decision is not consumed as display state', async ({page}) => {
  const frame = await order(page, 'CLOSE-LATE-51');
  let arrived, release;
  const reached = new Promise(resolve => { arrived = resolve; });
  const barrier = new Promise(resolve => { release = resolve; });
  await page.route('**/ui/document/Orders/form-close-intent', async route => {
    const response = await route.fetch();
    arrived();
    await barrier;
    await route.fulfill({response});
  });
  await accept(frame);
  await reached;
  await frame.locator('input[name="Комментарий"]').fill('late during close');
  release();
  await expect.poll(() => frame.evaluate(() => window._obFormDirty)).toBe(true);
  await expect(frame.locator('input[name="Комментарий"]')).toHaveValue('late during close');
  await expect(origins(page)).toHaveCount(1);
  await expect(results(page)).toHaveCount(0);
});

test('two results of one processor: close only current, and honor its own veto', async ({page}) => {
  await accept(await order(page, 'FIRST-45', 'veto'));
  const first = await lastResult(page);
  await accept(await order(page, 'SECOND-46'));
  const second = await lastResult(page);
  await expect(results(page)).toHaveCount(2);
  await second.getByRole('button', {name:'Close',exact:true}).click();
  await expect(results(page)).toHaveCount(1);
  expect(second.isDetached()).toBe(true);
  expect(first.isDetached()).toBe(false);
  const response = page.waitForResponse(r => r.url().toLowerCase().includes('/completion/form-close-intent'));
  await page.locator('.ob-result-form button[aria-label="Close"]').click();
  expect((await (await response).json()).close.allowed).toBe(false);
  await expect(results(page)).toHaveCount(1);
  await expect(origins(page)).toHaveCount(0);
});

async function holdAccept(page) {
  let data, release;
  const barrier = new Promise(resolve => { release = resolve; });
  await page.route('**/ui/document/Orders/form-event', async route => {
    if (new URLSearchParams(route.request().postData()).get('_element') !== 'Accept') return route.continue();
    const response = await route.fetch();
    data = await response.json();
    await barrier;
    try { await route.fulfill({response}); } catch (_) { /* source may have navigated */ }
  });
  return {data:()=>data, release};
}

test('late edit after committed save prevents result handoff', async ({page}) => {
  const frame = await order(page, 'LATE-47');
  const held = await holdAccept(page);
  await accept(frame);
  await expect.poll(() => held.data()?.resultForm?.token).toBeTruthy();
  await frame.locator('input[name="Комментарий"]').fill('late edit must survive');
  held.release();
  await expect.poll(() => frame.evaluate(() => window._obFormDirty)).toBe(true);
  await expect(results(page)).toHaveCount(0);
  await expect(origins(page)).toHaveCount(1);
  await expect(frame.locator('input[name="Комментарий"]')).toHaveValue('late edit must survive');
});

test('stale source documentToken cannot close a reloaded source instance', async ({page}) => {
  const frame = await order(page, 'STALE-48');
  const oldToken = await frame.evaluate(() => window.obFrameCloseDocumentToken);
  const held = await holdAccept(page);
  await accept(frame);
  await expect.poll(() => held.data()?.resultForm?.token).toBeTruthy();
  const payload = held.data().resultForm;
  await frame.goto('/ui/document/Orders/new');
  expect(await frame.evaluate(() => window.obFrameCloseDocumentToken)).not.toBe(oldToken);
  held.release();
  await frame.evaluate(({payload,oldToken}) => window.parent.postMessage({source:'obFormResult', result:payload, sourceURL:location.href, documentToken:oldToken},location.origin), {payload,oldToken});
  await expect(results(page)).toHaveCount(0);
  await expect(origins(page)).toHaveCount(1);
});


test('inactive loading frame has a real token when it sends its initial title', async ({page}) => {
  await page.evaluate(() => {
    window.observedTitleTokens=[];
    window.addEventListener('message', ev => {
      if(ev.data?.source==='obSetTitle') window.observedTitleTokens.push({token:ev.data.documentToken,actual:ev.source.obFrameCloseDocumentToken,title:ev.data.title});
    });
    window.obOpenTab('/ui/document/Orders/new','unmasked first',{allowDup:true});
    window.obOpenTab('/ui/document/Orders/new','unmasked second',{allowDup:true});
  });
  await expect(page.locator('.ob-tab-label')).toHaveText(['MASK***','MASK***']);
  const messages=await page.evaluate(()=>window.observedTitleTokens);
  expect(messages.length).toBeGreaterThanOrEqual(2);
  expect(messages.every(m=>m.token && m.token===m.actual)).toBe(true);
});

function traceDisplayTitles() {
  window.displayTitleTrace = [];
  const scan = () => {
    const values = [document.title];
    document.querySelectorAll('.ob-tab').forEach(tab => values.push(tab.title, tab.querySelector('.ob-tab-label')?.textContent || ''));
    document.querySelectorAll('[data-ob-form-title]').forEach(label => values.push(label.textContent));
    window.displayTitleTrace.push(values);
  };
  new MutationObserver(scan).observe(document, {subtree:true, childList:true, characterData:true, attributes:true, attributeFilter:['title']});
  document.addEventListener('DOMContentLoaded', scan);
  scan();
}

test('no raw title flashes during opening, save, reopening or shell restore', async ({page}) => {
  await page.addInitScript(traceDisplayTitles);
  await page.evaluate(traceDisplayTitles);
  const frame = await order(page, 'SECRET-49');
  const response = page.waitForResponse(r => r.url().endsWith('/Orders/form-event') && new URLSearchParams(r.request().postData()).get('_element') === 'Accept');
  await accept(frame);
  const saved = await (await response).json();
  const mini = await lastResult(page);
  await mini.getByRole('button', {name:'Close',exact:true}).click();
  await expect(results(page)).toHaveCount(0);
  const savedURL = '/ui/document/Orders/' + saved.savedId;
  const initial = await page.evaluate(url => {
    const tab = window.obOpenTab(url,'RAW NUMBER MUST NOT FLASH');
    return [tab.title,tab.label.textContent,tab.btn.title];
  }, savedURL);
  expect(initial.every(value => !value.includes('RAW NUMBER MUST NOT FLASH') && !value.includes('SECRET-49'))).toBe(true);
  const reopened = await (await origins(page).last().elementHandle()).contentFrame();
  await expect(reopened.locator('[data-ob-form-title]')).toHaveText('MASK***');
  await expect(reopened.locator('input[name="Номер"]')).toHaveValue('SECRET-49');
  const trace = await page.evaluate(() => window.displayTitleTrace.flat());
  expect(trace.every(value => !value.includes('RAW NUMBER MUST NOT FLASH') && !value.includes('SECRET-49'))).toBe(true);
  await page.reload({waitUntil:'domcontentloaded'});
  await expect(page.locator('.ob-tab-label')).toHaveText(['MASK***']);
  expect((await page.evaluate(() => window.displayTitleTrace.flat())).every(value => !value.includes('RAW NUMBER MUST NOT FLASH') && !value.includes('SECRET-49'))).toBe(true);
});
