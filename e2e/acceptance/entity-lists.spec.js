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
test('case-insensitive source path binds the rendered control and refreshes B',async({page})=>{
 for(const order of [['second-history','history'],['history','second-history']]){
  await open(page);
  expect(JSON.parse(await list(page).getAttribute('data-ob-list-sources'))).toEqual({'объект.направление':'Направление'});
  const held=new Map();
  const delivered=[];
  const responses={};
  const intercept=async route=>{
   const element=new URLSearchParams(route.request().postData()).get('element');
   if(!order.includes(element))return route.continue();
   const response=await route.fetch();
   const body=await response.body();
   let release;const barrier=new Promise(resolve=>release=resolve);
   const delivery=barrier.then(()=>route.fulfill({response,body}));
   held.set(element,{release,delivery});
   await delivery;
  };
  await page.route('**/form-list-refresh',intercept);
  // Each waiter belongs to its own widget and precedes the source change. Hold real
  // server responses so either alias can arrive first without timing sleeps.
  for(const element of order){
   responses[element]=page.waitForResponse(r=>r.url().endsWith('/form-list-refresh')&&r.request().method()==='POST'&&new URLSearchParams(r.request().postData()).get('element')===element)
    .then(response=>{delivered.push(element);return response;});
  }
  try{
   await source(page).selectOption(ids.b);
   await expect.poll(()=>held.size).toBe(2);
   await expect(list(page).locator('tbody tr')).toHaveCount(0);
   for(const element of order){
    held.get(element).release();
    const response=await responses[element];
    expect(new URLSearchParams(response.request().postData()).get('element')).toBe(element);
    await expect(page.locator(`[data-ob-list-id="${element}"]`)).toHaveAttribute('data-ob-list-state','ready');
    if(element===order[0])expect(delivered).toEqual([element]);
   }
   const response=await responses.history;
   expect(JSON.parse(new URLSearchParams(response.request().postData()).get('sources'))).toEqual({'объект.направление':ids.b});
   const secondResponse=await responses['second-history'];
   expect(JSON.parse(new URLSearchParams(secondResponse.request().postData()).get('sources'))).toEqual({'Объект.Направление':ids.b});
   expect(delivered).toEqual(order);
   await expect(list(page)).toHaveAttribute('data-ob-list-state','ready');
   await expect(list(page).locator('tbody tr')).toHaveCount(2);
   await expect(list(page).locator('tbody')).toContainText(' B');
   const second=page.locator('[data-ob-list-id="second-history"]');
   await expect(second.locator('tbody tr')).toHaveCount(2);
   await expect(second.locator('tbody')).toContainText(' B');
  }finally{
   held.forEach(entry=>entry.release());
   await Promise.all([...held.values()].map(entry=>entry.delivery));
   await page.unroute('**/form-list-refresh',intercept);
  }
 }
});
for (const abort of [true,false]) test(`list A→B→A, reverse replies, no AbortController=${!abort}`,async ({page})=>{
 await page.addInitScript(value=>{if(!value)window.AbortController=undefined;},abort);
 await open(page);
 const cell=page.locator('[name="tp.Items.0.Text"]'); await cell.fill("dirty row");
 const held=[];
 await page.route('**/form-list-refresh',async route=>{
  if(new URLSearchParams(route.request().postData()).get('element')!=='history')return route.continue();
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
  const second=page.locator('[data-ob-list-id="second-history"]');
  await expect(second).toHaveAttribute('data-ob-list-state','ready');
  await expect(second.locator('tbody')).not.toContainText(' B');
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
 await page.route('**/form-list-refresh',async route=>{if(new URLSearchParams(route.request().postData()).get('element')!=='history')return route.continue();const response=await route.fetch();const body=await response.body();arrived();await barrier;try{await route.fulfill({response,body});}catch(e){if(!route.request().failure())throw e;}});
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

test('two lists refresh their own exact aliases, clear together and preserve dirty table data',async({page})=>{
 await open(page);
 const persistedSourceBefore=await source(page).inputValue();
 const second=page.locator('[data-ob-list-id="second-history"]');
 await expect(second).toHaveAttribute('data-ob-list-state','ready');
 expect(JSON.parse(await second.getAttribute('data-ob-list-sources'))).toEqual({'Объект.Направление':'Направление'});
 const cell=page.locator('[name="tp.Items.0.Text"]');await cell.fill('two-list dirty');
 const dirtyBefore=await page.evaluate(()=>window._obFormDirty);
 const payloads=[];
 page.on('request',r=>{if(r.url().endsWith('/form-list-refresh'))payloads.push(new URLSearchParams(r.postData()));});
 for(const value of [ids.b,ids.a,'',ids.b]){
  await source(page).selectOption(value);
  for(const widget of [list(page),second]){
   await expect(widget).toHaveAttribute('data-ob-list-state',value?'ready':'empty');
   await expect(widget.locator('tbody tr')).toHaveCount(value?2:0);
   if(value===ids.b)await expect(widget.locator('tbody')).toContainText(' B');
   if(value===ids.a)await expect(widget.locator('tbody')).not.toContainText(' B');
  }
 }
 expect(payloads.length).toBe(8);
 for(const p of payloads){
  const path=p.get('element')==='history'?'объект.направление':'Объект.Направление';
  expect(Object.keys(JSON.parse(p.get('sources')))).toEqual([path]);
 }
 await expect(cell).toHaveValue('two-list dirty');
 expect(await page.evaluate(()=>window._obFormDirty)).toBe(dirtyBefore);
 const persisted=await page.request.get(`/ui/document/Заявка/${ids.owner}`);
 expect(await persisted.text()).toContain(`value="${persistedSourceBefore}" selected`);
});

const postOwner = n => `40000000-0000-0000-0000-${String(n).padStart(12,'0')}`;
const note = page => page.locator('[name="Заметка"]');
const checks = page => page.locator('[name="Проверок"]');
const question = page => page.locator('#_question-modal');
function postingRequests(page) {
 const calls={events:[],writes:[]};
 page.on('request',r=>{
  if(r.method()!=='POST')return;
  if(r.url().endsWith('/form-event'))calls.events.push(new URLSearchParams(r.postData()));
  else if(!r.url().endsWith('/form-list-refresh'))calls.writes.push(r);
 });
 return calls;
}
async function readyLists(page,branch) {
 for(const id of ['history','second-history']){
  const widget=page.locator(`[data-ob-list-id="${id}"]`);
  await expect(widget).toHaveAttribute('data-ob-list-state','ready');
  await expect(widget.locator('tbody tr')).toHaveCount(2);
  if(branch===ids.b)await expect(widget.locator('tbody')).toContainText(' B');
  else await expect(widget.locator('tbody')).not.toContainText(' B');
 }
}
async function persisted(page,id,branch,count,text='keep me') {
 const response=await page.request.get(`/ui/document/Заявка/${id}`);
 expect(response.status()).toBe(200);
 const html=await response.text();
 expect(html).toContain(`value="${branch}" selected`);
 // Inspect actual storage through a fresh public GET, independently of the DOM.
 // Parse the HTTP snapshot in Node: post-and-close may destroy the live page
 // context while this independent storage assertion is running.
 const inputs=html.match(/<input\b[^>]*>/g)||[];
 const value=name=>{
  const input=inputs.find(tag=>tag.includes(`name="${name}"`));
  expect(input,`persisted input ${name}`).toBeTruthy();
  const match=input.match(/\bvalue="([^"]*)"/);
  expect(match,`persisted value ${name}`).toBeTruthy();
  return match[1];
 };
 const values={checks:value('Проверок'),text:value('tp.Items.0.Text')};
 expect(Number(values.checks)).toBe(count);
 expect(values.text).toBe(text);
}
async function holdBeforePost(page) {
 let release,arrived;
 const ready=new Promise(r=>arrived=r),barrier=new Promise(r=>release=r);
 let held=false;
 await page.route('**/form-event',async route=>{
  const body=new URLSearchParams(route.request().postData());
  if(held||body.get('_event')!=='ПередПроведением')return route.continue();
  held=true;
  const response=await route.fetch(),bytes=await response.body();
  arrived();await barrier;await route.fulfill({response,body:bytes});
 });
 return {ready,release:()=>release()};
}
async function post(page,close) {
 await page.locator(close?'[data-ob-form-close="post"]':'button[name="_action"][value="post"]').click();
}
for(const close of [false,true])test(`before-post question merges live sources and table data, close=${close}`,async({page})=>{
 const id=postOwner(close?2:1);await open(page,id);
 const calls=postingRequests(page),held=await holdBeforePost(page);
 try{
  await post(page,close);await held.ready;
  // A→B→A is a real new revision although its final source equals the sent one.
  await source(page).selectOption(ids.b);await source(page).selectOption(ids.a);
  await note(page).fill('новый ввод');
  const cell=page.locator('[name="tp.Items.0.Text"]');await cell.fill('new table data');
  held.release();await expect(question(page)).toBeVisible();
  await expect(source(page)).toHaveValue(ids.a);await readyLists(page,ids.a);
  await expect(note(page)).toHaveValue('новый ввод');await expect(cell).toHaveValue('new table data');
  await expect(checks(page)).toHaveValue('1');
  expect(await page.evaluate(()=>window._obFormDirty)).toBe(true);
  await persisted(page,id,ids.a,0);expect(calls.events).toHaveLength(1);expect(calls.writes).toHaveLength(0);
  await question(page).getByRole('button',{name:'Да',exact:true}).click();
  await expect.poll(()=>calls.writes.length).toBe(1);
  await expect.poll(async()=>{
   const response=await page.request.get(`/ui/document/Заявка/${id}`);
   return (await response.text()).includes('new table data');
  }).toBe(true);
  await persisted(page,id,ids.a,2,'new table data');
  expect(calls.events.map(x=>x.get('_event'))).toEqual(['ПередПроведением','ПередПроведением']);
  expect(calls.events[1].get('_question_answer')).toBe('Да');expect(calls.writes).toHaveLength(1);
 }finally{held.release();}
});
for(const dismiss of [false,true])test(`before-post question ${dismiss?'dismissal':'cancellation'} keeps untouched handler values and never writes`,async({page})=>{
 const id=postOwner(dismiss?4:3);await open(page,id);const calls=postingRequests(page);
 await post(page,true);await expect(question(page)).toBeVisible();
 await expect(source(page)).toHaveValue(ids.b);await readyLists(page,ids.b);
 await expect(note(page)).toHaveValue('правка обработчика');await expect(checks(page)).toHaveValue('1');
 if(dismiss)await question(page).click({position:{x:2,y:2}});
 else{
  const answered=page.waitForResponse(r=>r.url().endsWith('/form-event'));
  await question(page).getByRole('button',{name:'Отмена',exact:true}).click();await answered;
  await expect(checks(page)).toHaveValue('2');await readyLists(page,ids.b);
 }
 await expect(question(page)).toHaveCount(0);await persisted(page,id,ids.a,0);
 expect(calls.events).toHaveLength(dismiss?1:2);expect(calls.writes).toHaveLength(0);
 expect(await page.evaluate(()=>window._obFormDirty)).toBe(true);
});
for(const close of [false,true])test(`changed snapshot without a question needs a fresh check, close=${close}`,async({page})=>{
 const id=postOwner(close?6:5);await open(page,id);await note(page).fill('без вопроса');
 const calls=postingRequests(page),held=await holdBeforePost(page);
 try{
  await post(page,close);await held.ready;
  await source(page).selectOption(ids.b);await source(page).selectOption(ids.a);
  held.release();await expect(checks(page)).toHaveValue('1');await readyLists(page,ids.a);
  await persisted(page,id,ids.a,0);expect(calls.events).toHaveLength(1);expect(calls.writes).toHaveLength(0);
  await post(page,close);await expect.poll(()=>calls.writes.length).toBe(1);
  await expect.poll(async()=>{
   const r=await page.request.get(`/ui/document/Заявка/${id}`);return (await r.text()).includes(`value="${ids.b}" selected`);
  }).toBe(true);
  await persisted(page,id,ids.b,2);expect(calls.events).toHaveLength(2);expect(calls.writes).toHaveLength(1);
 }finally{held.release();}
});
test('ordinary question retains its answer route alongside related lists',async({page})=>{
 await open(page,postOwner(7));const calls=postingRequests(page);
 await page.getByRole('button',{name:'Спросить',exact:true}).click();await expect(question(page)).toBeVisible();
 await readyLists(page,ids.a);
 await question(page).getByRole('button',{name:'Да',exact:true}).click();
 await expect(source(page)).toHaveValue(ids.b);await readyLists(page,ids.b);
 expect(calls.events.map(x=>x.get('_event'))).toEqual(['Нажатие','Ответ']);expect(calls.writes).toHaveLength(0);
 await persisted(page,postOwner(7),ids.a,0);
});
