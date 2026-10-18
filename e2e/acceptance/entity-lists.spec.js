const { test, expect } = require('@playwright/test');
const ids = JSON.parse(process.env.OB_LIST_IDS || '{}');
const source = page => page.locator('select[name="Направление"]');
const list = page => page.locator('[data-ob-list-id="history"]');
let errors;
test.beforeEach(async ({ page }) => { errors=[];page.on('pageerror',e=>errors.push(e.message)); });
test.afterEach(() => expect(errors).toEqual([]));
async function open(page, id=ids.owner) {
 await page.goto(`/ui/document/Заявка/${id}`);
 await expect(list(page)).toHaveAttribute('data-ob-list-state','ready');
 await expect(list(page).locator('tbody tr')).toHaveCount(2);
}
for (const abort of [true,false]) test(`list A→B→A, reverse replies, no AbortController=${!abort}`,async ({page})=>{
 await page.addInitScript(value=>{if(!value)window.AbortController=undefined;},abort);
 await open(page);
 const cell=page.locator('[name="tp.Items.0.Text"]'); await cell.fill("dirty row");
 const held=[];
 await page.route('**/form-list-refresh',async route=>{
  const response=await route.fetch();const body=await response.body();
  let release;const barrier=new Promise(resolve=>release=resolve);const entry={release,source:new URLSearchParams(route.request().postData()).get('sources')};held.push(entry);
  await barrier;try{await route.fulfill({response,body});}catch(e){if(!route.request().failure())throw e;}entry.delivered=true;
 });
 try {
  await source(page).selectOption(ids.b);await expect.poll(()=>held.length).toBe(1);
  await expect(list(page).locator('tbody tr')).toHaveCount(0);
  await source(page).selectOption(ids.a);await expect.poll(()=>held.length).toBe(2);
  held[1].release();await expect(list(page)).toHaveAttribute('data-ob-list-state','ready');
  const newest=await list(page).locator('tbody tr').allTextContents();expect(newest.join(' ')).not.toContain(' B');
  held[0].release();await expect.poll(()=>held[0].delivered).toBeTruthy();
  await expect(list(page).locator('tbody tr')).toHaveText(newest);
  await expect(cell).toHaveValue("dirty row");
  expect(await page.evaluate(()=>window._obFormDirty)).toBe(true);
 }finally{held.forEach(x=>x.release());}
});
test('empty, failure, recovery and safe navigation preserve unsaved owner',async ({page,context})=>{
 await page.goto('/ui/document/Заявка/new');
 await expect(list(page)).toHaveAttribute('data-ob-list-state','empty');
 await source(page).selectOption(ids.a);await expect(list(page)).toHaveAttribute('data-ob-list-state','ready');
 const popupPromise=page.waitForEvent('popup');await list(page).locator('a').first().press('Enter');const popup=await popupPromise;await popup.waitForLoadState('domcontentloaded');
 expect(page.url()).toContain('/new');expect(await page.evaluate(()=>window._obFormDirty)).toBe(true);
 await popup.close();
});
test('empty/network failure clear previous rows and recover without saving',async({page})=>{
 await open(page);
 await source(page).selectOption('');await expect(list(page)).toHaveAttribute('data-ob-list-state','empty');await expect(list(page).locator('tbody tr')).toHaveCount(0);
 await page.route('**/form-list-refresh',route=>route.abort('failed'));
 await source(page).selectOption(ids.b);await expect(list(page)).toHaveAttribute('data-ob-list-state','error');await expect(list(page).locator('tbody tr')).toHaveCount(0);
 await page.unroute('**/form-list-refresh');await source(page).selectOption(ids.a);await expect(list(page)).toHaveAttribute('data-ob-list-state','ready');
 const before=await page.request.get(`/ui/document/Заявка/${ids.owner}`);
 expect(before.status()).toBe(200);expect(await before.text()).toContain(ids.a);
 const a=list(page).locator('a').first();expect(await a.getAttribute('href')).toMatch(/^\/ui\/catalog\/[^/]+\/[0-9a-f-]{36}$/);await expect(a).toHaveAttribute('target','_blank');
});
test('closed DOM instance rejects late response',async({page})=>{
 await open(page);
 let release;let arrived;const ready=new Promise(r=>arrived=r);const barrier=new Promise(r=>release=r);
 await page.route('**/form-list-refresh',async route=>{const response=await route.fetch();const body=await response.body();arrived();await barrier;try{await route.fulfill({response,body});}catch(e){if(!route.request().failure())throw e;}});
 try{await source(page).selectOption(ids.b);await ready;await page.locator('#main-form').evaluate(f=>f.remove());release();await expect(page.locator('.ob-entity-list')).toHaveCount(0);}finally{release&&release();}
});

test('server handler changes source, refresh preserves dirty and the owner is not saved',async({page})=>{
 await open(page);
 const [response]=await Promise.all([page.waitForResponse(r=>r.url().endsWith('/form-event')&&r.request().method()==='POST'),page.getByRole('button',{name:'ВеткаB',exact:true}).click()]);
 const data=await response.json();expect(data.ok).toBe(true);expect(data.entityLists.history.status).toBe('ready');
 await expect(source(page)).toHaveValue(ids.b);await expect(list(page)).toHaveAttribute('data-ob-list-state','ready');await expect(list(page).locator('tbody')).toContainText(' B');
 const persisted=await page.request.get(`/ui/document/Заявка/${ids.owner}`);expect(await persisted.text()).toContain(ids.a);
});

test('late writing event and failed list refresh do not repeat the write',async({page})=>{
 await open(page);
 let release,arrived;const ready=new Promise(r=>arrived=r),barrier=new Promise(r=>release=r);
 let writes=0;
 await page.route('**/form-event',async route=>{writes++;const response=await route.fetch();const body=await response.body();arrived();await barrier;await route.fulfill({response,body});});
 try {
  const writing=page.getByRole('button',{name:'ЗаписатьB',exact:true}).click();await ready;
  await source(page).selectOption(ids.b);await expect(list(page)).toHaveAttribute('data-ob-list-state','ready');
  await page.route('**/form-list-refresh',route=>route.abort('failed'));
  release();await writing;
  await expect(source(page)).toHaveValue(ids.b);
  await expect(list(page)).toHaveAttribute('data-ob-list-state','error');await expect(list(page).locator('tbody tr')).toHaveCount(0);
  expect(writes).toBe(1);expect(await page.evaluate(()=>window._obFormDirty)).toBe(true);
  const persisted=await page.request.get(`/ui/document/Заявка/${ids.owner}`);expect(await persisted.text()).toContain(`value="${ids.b}" selected`);
 }finally{release&&release();}
});
