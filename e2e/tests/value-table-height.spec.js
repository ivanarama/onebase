// Exercise the loaded YAML form, normal HTTP renderer and production table JS.
const { test, expect } = require('@playwright/test');
const { execFileSync, spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');
const net = require('net');

const repo = path.resolve(__dirname, '../..');
const cases = [
  { name: 'Height90', height: 90, width: 640 },
  { name: 'Height180', height: 180, width: 640 },
  { name: 'HeightOnly', height: 180, width: 0 },
  { name: 'WidthOnly', height: 0, width: 640 },
  { name: 'NoSize', height: 0, width: 0 },
  { name: 'Horizontal', height: 180, width: 640, horizontal: true },
];
let work, server, base, output = '';

test.beforeAll(async () => {
  test.setTimeout(120_000);
  work = fs.mkdtempSync(path.join(os.tmpdir(), 'onebase-vt-height-'));
  function write(file, body) {
    const target = path.join(work, 'project', file);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, body, 'utf8');
  }
  write('config/app.yaml', "name: value-table-height\nversion: '1.0'\n");
  for (const scenario of cases) {
    write(`catalogs/${scenario.name}.yaml`, `name: ${scenario.name}\nkind: catalog\nfields:\n  - name: Наименование\n    type: string\n`);
    let elements = `  - kind: ТабличнаяЧасть\n    name: History\n    data_path: Форма.История\n    width: ${scenario.width}\n    height: ${scenario.height}\n`;
    if (scenario.horizontal) {
      elements = '  - kind: ГруппаФормы\n    name: Row\n    orientation: horizontal\n    children:\n'
        + elements.split('\n').filter(Boolean).map(line => `    ${line}`).join('\n') + '\n';
    }
    elements += '  - kind: ПолеВвода\n    name: Following\n    data_path: Объект.Наименование\n';
    write(`forms/${scenario.name.toLowerCase()}/object.form.yaml`, `schema: onebase.form/v1\nform:\n  name: Object\n  kind: object\n  entity: ${scenario.name}\nattributes:\n  - name: Объект\n    type: CatalogRef.${scenario.name}\n    main: true\n    save: true\n  - name: История\n    type: ValueTable\n    columns:\n      - name: Номер\n        type: string\nelements:\n${elements}`);
  }
  for (const name of ['RowsOutput', 'RowsEditable', 'RowsDelete']) {
    const editable = name !== 'RowsOutput';
    write(`catalogs/${name}.yaml`, `name: ${name}
kind: catalog
fields:
  - name: Наименование
    type: string
`);
    write(`forms/${name.toLowerCase()}/object.form.yaml`, `schema: onebase.form/v1
form:
  name: Object
  kind: object
  entity: ${name}
attributes:
  - name: Объект
    type: CatalogRef.${name}
    main: true
    save: true
  - name: Results
    type: ValueTable
    columns:
      - {name: Name, type: string}
      - {name: Flag, type: bool}
      - {name: HiddenFlag, type: bool}
      - {name: Amount, type: number}
elements:
  - kind: ТабличнаяЧасть
    name: History
    data_path: Форма.Results
    rows: 5
    show_count: true
    empty_text: No records
${name === 'RowsDelete' ? '' : '    events: {ПриАктивизацииСтроки: Selected}'}
    children:
      - {kind: Колонка, name: NameColumn, data_path: Name, readonly: ${!editable}}
      - {kind: Колонка, name: FlagColumn, data_path: Flag, readonly: true}
      - {kind: Колонка, name: AmountColumn, data_path: Amount, readonly: true}
  - kind: ТабличнаяЧасть
    name: ReadonlyHistory
    data_path: Форма.Results
    readonly: true
    children:
      - {kind: Колонка, name: ReadonlyName, data_path: Name, readonly: true}
      - {kind: Колонка, name: ReadonlyAmount, data_path: Amount, readonly: true}
  - {kind: Кнопка, name: Load, events: {Нажатие: LoadRows}}
  - {kind: Кнопка, name: Clear, events: {Нажатие: ClearRows}}
  - {kind: Кнопка, name: Check, events: {Нажатие: CheckRows}}
`);
    write(`forms/${name.toLowerCase()}/object.form.os`, `Процедура LoadRows()
 Results.Очистить();
 Для Счетчик = 1 По 30 Цикл
  Стр = Results.Добавить();
  Стр.Name = "row " + Строка(Счетчик);
  Стр.Flag = Истина;
  Стр.HiddenFlag = Истина;
  Стр.Amount = 7;
 КонецЦикла;
КонецПроцедуры
Процедура ClearRows()
 Results.Очистить();
КонецПроцедуры
Процедура Selected()
 Если ТекущаяСтрока.Flag И ТекущаяСтрока.HiddenFlag Тогда
  Сообщить("kept-true");
 ИначеЕсли НЕ ТекущаяСтрока.Flag И НЕ ТекущаяСтрока.HiddenFlag Тогда
  Сообщить("kept-false");
 Иначе
  ВызватьИсключение("bool payload lost");
 КонецЕсли;
КонецПроцедуры
Процедура CheckRows()
 Сообщить("received");
КонецПроцедуры
`);
  }
  const binary = process.env.OB_BIN || path.join(work, process.platform === 'win32' ? 'onebase.exe' : 'onebase');
  if (!process.env.OB_BIN) {
    execFileSync('go', ['build', '-o', binary, './cmd/onebase'], { cwd: repo, timeout: 90_000 });
  }
  const db = path.join(work, 'test.db');
  const common = ['--project', path.join(work, 'project'), '--sqlite', db];
  execFileSync(binary, ['migrate', ...common], { cwd: work, timeout: 30_000 });
  // Reserve a free loopback port; this server and database belong to this suite.
  const reservation = net.createServer();
  await new Promise(resolve => reservation.listen(0, '127.0.0.1', resolve));
  const port = reservation.address().port;
  await new Promise(resolve => reservation.close(resolve));
  base = `http://127.0.0.1:${port}`;
  server = spawn(binary, ['run', ...common, '--host', '127.0.0.1', '--port', String(port)], {
    cwd: work, env: { ...process.env, ONEBASE_NO_GUI: '1' }, stdio: ['ignore', 'pipe', 'pipe'],
  });
  server.stdout.on('data', data => { output += data; });
  server.stderr.on('data', data => { output += data; });
  await expect.poll(async () => {
    if (server.exitCode !== null) throw new Error(output);
    try { return (await fetch(`${base}/healthz`)).status; } catch { return 0; }
  }, { timeout: 30_000 }).toBe(200);
});

// Each browser context shares this suite's server-side anonymous message log.
test.beforeEach(async ({ page }) => {
  const cleared = await page.request.post(`${base}/ui/messages/clear`);
  expect(cleared.status()).toBe(204);
  const listed = await page.request.get(`${base}/ui/messages`);
  expect(listed.status()).toBe(200);
  expect((await listed.json()).messages).toEqual([]);
});

test.afterAll(async () => {
  if (server && server.exitCode === null && server.signalCode === null) {
    const exited = new Promise(resolve => server.once('exit', resolve));
    server.kill();
    await exited;
  }
  if (work) fs.rmSync(work, { recursive: true, force: true });
});

for (const width of [1000, 360]) {
  for (const scenario of cases) {
    test(`${width}: ${scenario.name} keeps rows, header and following field usable`, async ({ page }) => {
      test.setTimeout(60_000);
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.setViewportSize({ width, height: 1000 });
      await page.goto(`${base}/ui/catalog/${scenario.name}/new`);
      const table = page.locator('[data-vt="История"]');
      const rows = table.locator('tbody tr');
      const add = page.locator('[data-ob-add-vt="История"]');
      for (let i = 0; i < 30; i++) await add.click();
      await expect(rows).toHaveCount(30);
      const firstInput = rows.first().locator('input');
      await firstInput.fill('first row');
      await expect(firstInput).toHaveValue('first row');
      const following = page.locator('[data-ob-el="Following"]');
      if (scenario.height) {
        const box = page.locator('.managed-vt-layout[data-ob-el="History"]');
        await expect(box).toHaveAttribute('data-ob-el-path', scenario.horizontal ? '0.0' : '0');
        const geometry = await box.evaluate(el => ({
          height: el.getBoundingClientRect().height, client: el.clientHeight, total: el.scrollHeight,
        }));
        expect(geometry.height).toBeCloseTo(scenario.height, 0);
        expect(geometry.total).toBeGreaterThan(geometry.client);
        await box.evaluate(el => { el.scrollTop = 0; });
        await box.hover();
        await page.mouse.wheel(0, 280);
        await expect.poll(() => box.evaluate(el => el.scrollTop)).toBeGreaterThan(20);
        const header = table.locator('thead th').first();
        const b = await box.boundingBox();
        const h = await header.boundingBox();
        expect(Math.abs(h.y - b.y)).toBeLessThanOrEqual(2);
        const f = await following.boundingBox();
        expect(f.y).toBeGreaterThanOrEqual(b.y + b.height - 1);
        await expect(header).toBeVisible();
      } else {
        await expect(page.locator('.managed-vt-scroll')).toHaveCount(0);
        const t = await table.boundingBox();
        const f = await following.boundingBox();
        expect(f.y).toBeGreaterThan(t.y + t.height);
      }
      // Playwright scrolls the real block to its bottom to reach the last row
      // and the add button; this catches content hidden behind a sibling.
      await rows.last().locator('[data-ob-remove-row]').click();
      await expect(rows).toHaveCount(29);
      await add.click();
      await expect(rows).toHaveCount(30);
      await following.locator('input').fill('following field');
      await expect(following.locator('input')).toHaveValue('following field');
      expect(errors).toEqual([]);
    });
  }
}

// Native FormData and real form-event routes cover both the writable source and
// a readonly duplicate; no hand-built payload can hide a disabled bool here.
for (const name of ['RowsOutput', 'RowsEditable']) {
  test(`${name}: rows geometry, column plan and native payload survive round-trips`, async ({ page }) => {
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(`${base}/ui/catalog/${name}/new`);
    const box = page.locator('.managed-vt-rows');
    const tbody = box.locator('tbody');
    const height = () => box.evaluate(el => el.getBoundingClientRect().height);
    const emptyHeight = await height();
    async function event(button) {
      const [response] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/form-event') && r.request().method() === 'POST'),
        page.locator(`[data-ob-fire-click="${button}"]`).click(),
      ]);
      const result = await response.json();
      expect(result.ok, result.error).toBe(true);
      return result;
    }
    async function payload() {
      return page.evaluate(() => Array.from(new FormData(document.getElementById('main-form')).entries()));
    }
    await event('Load');
    await expect(tbody.locator('[data-ob-vt-row]')).toHaveCount(30);
    expect(await height()).toBe(emptyHeight);
    const pairs = await payload();
    for (const field of ['Name', 'Flag', 'HiddenFlag', 'Amount']) {
      const values = pairs.filter(([key]) => key === `vt.Results.0.${field}`).map(([, value]) => value);
      expect(values).toEqual([field === 'Name' ? 'row 1' : field === 'Amount' ? '7' : 'true']);
    }
    const readonly = page.locator('tbody[data-ob-table-readonly="1"]');
    expect(await readonly.locator('input').evaluateAll(inputs => inputs.every(input => input.disabled))).toBe(true);
    const [selectedResponse] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/form-event') && r.request().method() === 'POST'),
      tbody.locator('[data-ob-vt-row="0"] td').first().click(),
    ]);
    const selected = await selectedResponse.json();
    expect(selected.ok, selected.error).toBe(true);
    expect(selected.messages).toContain('kept-true');
    await event('Check');
    await box.evaluate(el => { el.scrollTop = 160; });
    const b = await box.boundingBox();
    const h = await box.locator('th').first().boundingBox();
    expect(Math.abs(h.y - b.y)).toBeLessThanOrEqual(2);
    await event('Clear');
    await expect(tbody.locator('.ob-vt-empty')).toHaveCount(1);
    expect(await height()).toBe(emptyHeight);
    if (name === 'RowsEditable') {
      await page.locator('[data-ob-add-vt="Results"]').click();
      await expect(tbody.locator('.ob-vt-empty')).toHaveCount(0);
      const row = tbody.locator('[data-ob-vt-row="0"]');
      await expect(row).toHaveCount(1);
      await expect(row.locator('input[name="vt.Results.0.Name"]')).toBeEditable();
      await expect(row.locator('input[name="vt.Results.0.HiddenFlag"][type="checkbox"]')).toBeHidden();
      await expect(row.locator('input[name="vt.Results.0.Flag"][type="checkbox"]')).toBeDisabled();
      expect((await payload()).filter(([key]) => key === 'vt.Results.0.HiddenFlag').map(([, value]) => value)).toEqual(['false']);
      const [response] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/form-event') && r.request().method() === 'POST'),
        row.locator('td').first().click(),
      ]);
      const activated = await response.json();
      expect(activated.ok, activated.error).toBe(true);
      expect(activated.messages).toContain('kept-false');
      await expect(page.locator('[data-ob-vt-count="Results"]').first()).toHaveText('(1)');
      expect(await height()).toBe(emptyHeight);
    }
    expect(errors).toEqual([]);
  });
}

test('RowsEditable: activate the last remaining row after deleting the first', async ({ page }) => {
  await page.goto(`${base}/ui/catalog/RowsEditable/new`);
  const tbody = page.locator('.managed-vt-rows tbody');
  const [loaded] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/form-event') && r.request().method() === 'POST'),
    page.locator('[data-ob-fire-click="Load"]').click(),
  ]);
  expect((await loaded.json()).ok).toBe(true);
  await tbody.locator('[data-ob-remove-row]').first().click();
  await expect(tbody.locator('tr')).toHaveCount(29);
  const [response] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/form-event') && r.request().method() === 'POST'),
    tbody.locator('tr').last().locator('td').first().click(),
  ]);
  expect(new URLSearchParams(response.request().postData()).get('_tp_row')).toBe('28');
  const selected = await response.json();
  expect(selected.ok, selected.error).toBe(true);
  expect(selected.messages).toContain('kept-true');
});

test('RowsDelete: manual deletion updates count and restores empty_text', async ({ page }) => {
  await page.goto(`${base}/ui/catalog/RowsDelete/new`);
  const tbody = page.locator('.managed-vt-rows tbody');
  const count = page.locator('[data-ob-vt-count="Results"]').first();
  const [loaded] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/form-event') && r.request().method() === 'POST'),
    page.locator('[data-ob-fire-click="Load"]').click(),
  ]);
  expect((await loaded.json()).ok).toBe(true);
  await expect(count).toHaveText('(30)');
  await tbody.locator('[data-ob-remove-row]').first().click();
  await expect(tbody.locator('tr')).toHaveCount(29);
  await expect(count).toHaveText('(29)');
  for (let i = 0; i < 29; i++) await tbody.locator('[data-ob-remove-row]').first().click();
  await expect(count).toHaveText('(0)');
  await expect(tbody.locator('.ob-vt-empty')).toHaveText('No records');
  await page.locator('[data-ob-add-vt="Results"]').click();
  await expect(count).toHaveText('(1)');
  await expect(tbody.locator('.ob-vt-empty')).toHaveCount(0);
  await tbody.locator('[data-ob-remove-row]').click();
  await expect(count).toHaveText('(0)');
  await expect(tbody.locator('.ob-vt-empty')).toHaveCount(1);
});
