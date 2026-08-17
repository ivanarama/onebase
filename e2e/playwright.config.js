// Браузерные smoke-тесты критических путей (#791).
//
// Живут в e2e/, а не в корне репозитория: package.json в корне сделал бы
// каталог npm-проектом и мог бы задеть другие джобы CI, которые ходят по
// репозиторию (frontend-gate, vendored-ассеты).
const { defineConfig, devices } = require('@playwright/test');

const PORT = process.env.OB_PORT || '18080';
const BASE = `http://127.0.0.1:${PORT}`;

module.exports = defineConfig({
  testDir: './tests',
  // Прогоны делят один сервер и одну базу, поэтому строго последовательно:
  // параллельные сценарии писали бы в общие данные и мешали друг другу.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : [['list']],
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: BASE,
    // Артефакты только при падении: разбирать упавший прогон без них — гадание,
    // а хранить для зелёных незачем.
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    locale: 'ru-RU',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: '../scripts/e2e-server.sh',
    // /healthz, а не /health: первый реально пингует базу, второй отвечает
    // всегда и сказал бы «готов» на мёртвой БД.
    url: `${BASE}/healthz`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
