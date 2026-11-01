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
