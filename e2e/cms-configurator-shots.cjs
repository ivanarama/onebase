// Capture the actual local configurator. No source or DOM is changed for screenshots.
const assert=require('node:assert/strict');
const {chromium,expect}=require('@playwright/test');const fs=require('fs'),path=require('path');
const launch=process.env.CMS_LAUNCHER_URL;const id=process.env.CMS_BASE_ID;
if(!launch||!id||!process.env.CMS_ADMIN_PASSWORD)throw Error('Set CMS_LAUNCHER_URL, CMS_BASE_ID and CMS_ADMIN_PASSWORD');
const shots=path.resolve(process.env.CMS_DEMO_SCREENSHOTS||'e2e/test-results/cms-configurator');fs.mkdirSync(shots,{recursive:true});
(async()=>{const edge='C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe';const executablePath=process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH||(process.platform==='win32'&&fs.existsSync(edge)?edge:undefined);const b=await chromium.launch({headless:true,executablePath});try{
const p=await b.newPage({viewport:{width:1100,height:900},deviceScaleFactor:2});const errors=[];p.on('pageerror',e=>errors.push(e.message));
const origin=new URL(launch);origin.hostname='127.0.0.1';await p.goto(origin.origin+'/launcher-cookie-migration');
const url=launch+'/bases/'+id+'/configurator';
if(process.env.CMS_DEMO_DB){await p.goto(launch+'/bases/'+id+'/edit');await p.locator('[name=db_path]').fill(path.resolve(process.env.CMS_DEMO_DB));await p.getByRole('button',{name:'Сохранить',exact:true}).click();}
await p.goto(url);await p.locator('[name=login]').fill('admin');await p.locator('[name=password]').fill(process.env.CMS_ADMIN_PASSWORD);await p.locator('button[type=submit]').click();await p.waitForFunction(()=>typeof selItem==='function');
async function shot(name,locator=p.locator('body')){await expect(locator).toBeVisible();await p.evaluate(()=>document.fonts.ready.then(()=>true));await p.mouse.move(1,1);await locator.screenshot({path:path.join(shots,name),animations:'disabled',caret:'hide'});}
await p.locator('[data-group=catalogs] summary').click();await p.locator('[data-id="e-АдресаДоставки"]').click();await expect(p.locator('[id="e-АдресаДоставки"]')).toBeVisible();await expect(p.locator('[id="e-АдресаДоставки"] select[name=owner]')).toHaveValue('Покупатели');await shot('cms-config-owner.png',p.locator('.cfg-body'));
await p.goto(url+'/forms/edit?entity='+encodeURIComponent('ЗаказПокупателя')+'&name='+encodeURIComponent('объекта'));await expect(p.locator('#canvas-host .fc-group').first()).toBeVisible();
await p.locator('#canvas-host legend').filter({hasText:'Как передать заказ'}).click();await expect(p.locator('#prop-body')).toContainText('Фон');await shot('cms-config-colors.png',p.locator('#editor-grid'));
await p.locator('#canvas-host label').filter({hasText:'Адрес покупателя'}).click();await expect(p.locator('#prop-body')).toContainText('Зависимый подбор');
await expect.poll(()=>p.locator('#prop-body input').evaluateAll(es=>es.map(e=>e.value))).toContain('Владелец');await p.locator('#prop-body .prop-choice-card').scrollIntoViewIfNeeded();await shot('cms-config-choice.png',p.locator('#editor-grid'));
async function select(panelID){
 await p.goto(url);await p.waitForFunction(()=>typeof selItem==='function');
 if(await p.locator('#cfg-sidebar').evaluate(e=>e.classList.contains('collapsed')))await p.locator('#sidebar-toggle').click();
 const item=p.locator('[data-id="'+panelID+'"]');
 for(const parent of await item.locator('xpath=ancestor::details').all()){if(await parent.getAttribute('open')===null)await parent.locator(':scope > summary').click();}
 await item.click();await expect(p.locator('[id="'+panelID+'"]')).toBeVisible();
}
await p.goto(url+'/navigation?subsystem='+encodeURIComponent('Заказы'));await expect(p.locator('body')).toContainText('Покупатели и адреса');await shot('cms-config-navigation.png');
await select('mkt-ЗаказСайта');await expect(p.locator('[id="mkt-ЗаказСайта"]')).toContainText('Из Excel');await p.locator('#sidebar-toggle').click();await p.locator('[id="yamltgl-ЗаказСайта"]').click();await shot('cms-config-print.png',p.locator('.cfg-body'));
await select('mod-РаботаСЗаказами');await shot('cms-config-module.png',p.locator('.cfg-body'));await p.getByRole('button',{name:/Синтакс-помощник/}).click();await p.locator('#syntax-ref-search').fill('КонецДня');await p.locator('#syntax-ref-tree').getByText('КонецДня',{exact:true}).first().click();const ref=await p.locator('#syntax-ref-panel').boundingBox(),bottom=await p.locator('#syntax-ref-detail button').boundingBox();await p.screenshot({path:path.join(shots,'cms-config-language.png'),clip:{x:ref.x,y:ref.y,width:ref.width,height:bottom.y+bottom.height+18-ref.y},caret:'hide',animations:'disabled'});
await select('mod-Рендер');await shot('cms-config-templates.png',p.locator('.cfg-body'));
await p.getByRole('button',{name:'Меню',exact:false}).first().click();await p.getByRole('link',{name:'Параметры базы',exact:true}).click();await expect(p.locator('#admin-overlay')).toBeVisible();await p.locator('#admin-overlay').getByRole('button',{name:'Сохранить политику паролей',exact:true}).scrollIntoViewIfNeeded();const box=await p.locator('#admin-overlay > div').first().boundingBox(),sec=await p.locator('#admin-overlay').getByText('Безопасность',{exact:true}).boundingBox(),save=await p.locator('#admin-overlay').getByRole('button',{name:'Сохранить политику паролей',exact:true}).boundingBox();await p.screenshot({path:path.join(shots,'cms-config-settings.png'),clip:{x:box.x+12,y:sec.y-8,width:box.width-24,height:save.y+save.height+12-sec.y+8},caret:'hide',animations:'disabled'});
fs.writeFileSync(path.join(shots,'config-errors.json'),JSON.stringify(errors));assert.deepEqual(errors,[],'Configurator JavaScript errors');console.log('Screenshots:',shots);
}finally{await b.close()}})().catch(e=>{console.error(e.message);process.exitCode=1});
