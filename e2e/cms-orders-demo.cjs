// Live CMS scenario. Requires a separately seeded examples/cms server.
// CMS_DEMO_PASSWORD=... node e2e/cms-orders-demo.cjs
// Screenshots are captured from the real UI, with synthetic fixture data.
const {chromium, expect} = require('@playwright/test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const base = process.env.CMS_DEMO_URL || 'http://127.0.0.1:18330';
const password = process.env.CMS_DEMO_PASSWORD;
if (!password) throw new Error('Set CMS_DEMO_PASSWORD for the seeded local manager account');
const shots = path.resolve(process.env.CMS_DEMO_SCREENSHOTS || 'e2e/test-results/cms-orders');
fs.mkdirSync(shots, {recursive:true});

(async () => {
  const edge = 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe';
  const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ||
    (process.platform === 'win32' && fs.existsSync(edge) ? edge : undefined);
  const browser = await chromium.launch({headless:true, executablePath});
  try {
    const visitor = await browser.newContext({viewport:{width:1440,height:1000}});
    const page = await visitor.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    assert.equal((await page.goto(base+'/hs/site/catalog')).status(), 200);
    await expect(page.getByText('Керамическая кружка', {exact:true})).toBeVisible();
    await page.screenshot({path:path.join(shots,'cms-catalog.png'),fullPage:true});
    await page.goto(base+'/hs/site/product/demo-mug');
    await page.getByRole('button',{name:'В корзину',exact:true}).click();
    await expect(page.getByText('Итого: 790 ₽',{exact:true})).toBeVisible();
    await page.screenshot({path:path.join(shots,'cms-cart.png'),fullPage:true});
    await page.getByRole('link',{name:'Оформить заказ',exact:true}).click();
    await page.locator('[name=name]').fill('Анна Примерова');
    await page.locator('[name=contact]').fill('anna@example.test');
    await page.locator('[name=comment]').fill('Демонстрация: заберу в пункте выдачи');
    await page.getByRole('button',{name:'Оформить заказ',exact:true}).click();
    await expect(page).toHaveURL(/\/hs\/shop\/order\/[\da-f-]+$/);
    const orderURL = page.url();
    const orderID = orderURL.split('/').pop();
    await expect(page.getByText('Принят, ждёт подтверждения',{exact:true})).toBeVisible();

    const office = await browser.newContext({viewport:{width:1440,height:1100}});
    const manager = await office.newPage();
    manager.on('pageerror', error => errors.push(error.message));
    await manager.goto(base+'/login');
    await manager.locator('[name=login]').fill(process.env.CMS_DEMO_USER || 'manager');
    await manager.locator('[name=password]').fill(password);
    await manager.locator('button[type=submit]').click();
    await manager.waitForURL(/\/ui/);
    const formURL = base+'/ui/document/'+encodeURIComponent('ЗаказПокупателя')+'/'+orderID;
    const opened = manager.waitForResponse(r=>r.url().includes('/form-event') && r.request().method()==='POST');
    await manager.goto(formURL);
    assert.equal((await (await opened).json()).ok,true);
    await expect(manager.locator('input[name=Сумма]')).toHaveValue('790');
    // Wait for the initial server event before editing the form.
    await expect(manager.locator('select[name=Статус]')).toHaveValue('Новый');
    async function change(name, value) {
      const response = manager.waitForResponse(r=>r.url().includes('/form-event') && r.request().method()==='POST');
      await manager.locator('select[name="'+name+'"]').selectOption(value);
      const result = await (await response).json();
      assert.equal(result.ok,true,result.error);
    }
    await change('Покупатель',{label:'Анна Примерова'});
    await expect(manager.locator('input[name=Телефон]')).toHaveValue('+7 (000) 000-00-01');
    await expect.poll(()=>manager.locator('input:disabled').evaluateAll(es=>es.map(e=>e.value))).toContain('ЗС-000001');
    await change('СпособПолучения','Доставка');
    const address = manager.locator('select[name=АдресДоставки]');
    await expect(address).toContainText('Дом — Анна');
    await expect(address).not.toContainText('Дом — Павел');
    await address.selectOption({label:'Дом — Анна'});
    await manager.screenshot({path:path.join(shots,'cms-order-delivery.png'),fullPage:true});
    await change('Покупатель',{label:'Павел Демонстрационный'});
    await expect(address).toHaveValue('');
    await expect(address).toContainText('Дом — Павел');
    await expect(address).not.toContainText('Дом — Анна');
    await change('Покупатель',{label:'Анна Примерова'});
    await change('СпособПолучения','Самовывоз');
    await expect(address).toBeHidden();
    await change('Город',{label:'Учебный город'});
    const pickup = manager.locator('select[name=ПунктВыдачи]');
    await pickup.locator('..').locator('[data-ob-ref-picker]').click();
    const choices = manager.locator('#_ref-picker-modal');
    await expect(choices).toContainText('Центральный — Учебный город');
    await expect(choices).not.toContainText('Парковый — Демонстрационный город');
    await manager.screenshot({path:path.join(shots,'cms-pickup-choice.png'),fullPage:true});
    await choices.getByText('Центральный — Учебный город',{exact:true}).click();
    await expect(choices).not.toBeVisible();
    await expect(pickup).not.toHaveValue('');
    await change('Город',{label:'Демонстрационный город'});
    await expect(pickup).toHaveValue('');
    await pickup.locator('..').locator('[data-ob-ref-picker]').click();
    await expect(choices).toContainText('Парковый — Демонстрационный город');
    await expect(choices).not.toContainText('Центральный — Учебный город');
    await choices.getByText('Парковый — Демонстрационный город',{exact:true}).click();

    await manager.getByRole('button',{name:'Добавить товары',exact:true}).click();
    const picker = manager.locator('#_item-picker-modal');
    await expect(picker).toBeVisible();
    await picker.locator('table').first().locator('tbody tr').filter({hasText:'Ежедневник без дат'}).locator('._ip-val').fill('2');
    await manager.screenshot({path:path.join(shots,'cms-product-picker.png'),fullPage:true});
    const selected = manager.waitForResponse(r=>r.url().includes('/form-event') && r.request().method()==='POST');
    await picker.getByRole('button',{name:'Перенести в документ',exact:true}).click();
    const selectedResult = await (await selected).json();
    assert.equal(selectedResult.ok,true,selectedResult.error);
    await expect(manager.locator('input[name=Сумма]')).toHaveValue('3170');
    await manager.locator('select[name=Ответственный]').selectOption({label:'Менеджер демонстрации'});
    await manager.setViewportSize({width:1440,height:2100});
    await manager.screenshot({path:path.join(shots,'cms-order-workspace.png'),fullPage:true});
    await manager.setViewportSize({width:1440,height:1100});
    const accepted = manager.waitForResponse(r=>r.url().includes('/form-event') && r.request().method()==='POST');
    await manager.getByRole('button',{name:'Подтвердить заказ',exact:true}).click();
    const acceptedResult = await (await accepted).json();
    assert.equal(acceptedResult.ok,true,acceptedResult.error);
    await expect(manager.locator('select[name=Статус]')).toHaveValue('ВРаботе');
    await manager.reload();
    await expect(manager.locator('input[name=Сумма]')).toHaveValue('3170');
    await page.goto(orderURL);
    await expect(page.getByText('В работе',{exact:true})).toBeVisible();
    await expect(page.getByText('Итого: 3 170 ₽',{exact:true})).toBeVisible();
    // The manager role cannot write the product catalog.
    assert.equal((await manager.request.get(base+'/ui/catalog/'+encodeURIComponent('Товары')+'/new')).status(),403);

    const links = await manager.locator('a[href*="print"]').evaluateAll(es=>es.map(e=>({text:e.textContent.trim(),url:e.href})));
    fs.writeFileSync(path.join(shots,'print-links.json'),JSON.stringify(links,null,2));
    assert.equal(links.length,2,'HTML and PDF print actions');
    const printPage = await office.newPage();
    assert.equal((await printPage.goto(links.find(link=>link.text==='ЗаказСайта').url)).status(),200);
    await expect(printPage.locator('body')).toContainText('Керамическая кружка');
    await expect(printPage.locator('body')).toContainText('Ежедневник без дат');
    await expect(printPage.locator('body')).toContainText('3170');
    await printPage.locator('table').screenshot({path:path.join(shots,'cms-order-print.png')});
    const pdf = await manager.request.get(links.find(link=>link.text==='PDF').url);
    assert.equal(pdf.status(),200,'PDF export');
    assert.equal((await pdf.body()).subarray(0,5).toString(),'%PDF-');
    const finished = manager.waitForResponse(r=>r.url().includes('/form-event') && r.request().method()==='POST');
    await manager.getByRole('button',{name:'Завершить',exact:true}).click();
    const finishedResult = await (await finished).json();
    assert.equal(finishedResult.ok,true,finishedResult.error);
    await page.goto(orderURL);
    await expect(page.getByText('Выполнен',{exact:true})).toBeVisible();
    await page.screenshot({path:path.join(shots,'cms-order-status.png'),fullPage:true});

    // A second real checkout verifies both answers to the cancellation question.
    await page.goto(base+'/hs/site/product/demo-mug');
    await page.getByRole('button',{name:'В корзину',exact:true}).click();
    await page.getByRole('link',{name:'Оформить заказ',exact:true}).click();
    await page.locator('[name=name]').fill('Павел Демонстрационный');
    await page.locator('[name=contact]').fill('pavel@example.test');
    await page.getByRole('button',{name:'Оформить заказ',exact:true}).click();
    const cancelledURL = page.url();
    const openedCancel = manager.waitForResponse(r=>r.url().includes('/form-event') && r.request().method()==='POST');
    await manager.goto(base+'/ui/document/'+encodeURIComponent('ЗаказПокупателя')+'/'+cancelledURL.split('/').pop());
    assert.equal((await (await openedCancel).json()).ok,true);
    await manager.getByRole('button',{name:'Отменить заказ',exact:true}).click();
    await expect(manager.getByText('Отменить этот заказ?',{exact:true})).toBeVisible();
    await manager.getByRole('button',{name:'Нет',exact:true}).click();
    await expect(manager.locator('select[name=Статус]')).toHaveValue('Новый');
    await manager.getByRole('button',{name:'Отменить заказ',exact:true}).click();
    await expect(manager.getByText('Отменить этот заказ?',{exact:true})).toBeVisible();
    const cancelAnswer = manager.waitForResponse(r=>r.url().includes('/form-event') && r.request().method()==='POST');
    await manager.getByRole('button',{name:'Да',exact:true}).click();
    const cancelResult = await (await cancelAnswer).json();
    assert.equal(cancelResult.ok,true,cancelResult.error);
    await page.goto(cancelledURL);
    await expect(page.getByText('Отменён',{exact:true})).toBeVisible();
    assert.deepEqual(errors,[],'Browser JavaScript errors');
    console.log(JSON.stringify({ok:true,orderID,orderURL,formURL,total:3170,screenshots:shots,printLinks:links},null,2));
  } finally {
    await browser.close();
  }
})().catch(error=>{console.error(String(error.message || error).slice(0,1800));process.exitCode=1;});
